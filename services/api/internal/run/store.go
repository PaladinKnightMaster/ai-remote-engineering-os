package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// ErrNotFound is returned when no run has the given id.
var ErrNotFound = errors.New("run not found")

// ErrDuplicateLaunch is returned by Create when a delegated launch's (grant_id, launch_key)
// already exists — an agent retry of run.launch. The caller re-fetches the original Run via
// GetByLaunchKey (the idempotent re-attach, ADR-0062 T-065a) so a retry never double-launches.
var ErrDuplicateLaunch = errors.New("duplicate delegated launch")

// launchEdge is the bracket edge for the run.launched lifecycle event — the operator
// launch is a human-agent authorization act (ADR-0061 GOV-1).
const launchEdge = "human-agent"

// launchActor is the audit actor for a Run's lifecycle events: the agent that drove a
// delegated Run, else the launching operator (behavior-preserving, ADR-0062 T-064). The
// principal/owner is Operator, recorded on the row for the evidence packet (AUD-17).
func launchActor(in Input) string {
	if in.Actor != "" {
		return in.Actor
	}
	return in.Operator
}

// eventActor picks a Run lifecycle event's actor from the row's (operator, actor): the
// agent for a delegated Run, else the operator (behavior-preserving).
func eventActor(operator, actor string) string {
	if actor != "" {
		return actor
	}
	return operator
}

// CommandSealer seals/opens the operator-authored step command at rest (F3 parity
// with the gateway, ADR-0059). A nil sealer is the plaintext dev/transition fallback
// until T-057 provisions the AES-256-GCM key (and promotes the gateway's cmdcrypt to
// a shared package); the store's seal/open helpers are nil-safe.
type CommandSealer interface {
	Seal(plaintext string) ([]byte, error)
	Open(b []byte) (string, error)
}

// Store persists runs + run_steps for the Run engine. It takes the pgx pool directly
// (the gateway-store pattern) so the package stays extraction-ready (RUN-1).
type Store struct {
	pool   *pgxpool.Pool
	sealer CommandSealer
	writer outbox.Writer
}

// NewStore builds the Run store. sealer may be nil (plaintext fallback — see
// CommandSealer). outboxTable is the api outbox table (e.g. "api_outbox") into which
// Create enqueues run.launched in the same transaction (AUD-9); the caller passes the
// table name so this package stays free of api-internal imports (the extraction seam).
func NewStore(pool *pgxpool.Pool, sealer CommandSealer, outboxTable string) *Store {
	return &Store{pool: pool, sealer: sealer, writer: outbox.Writer{Table: outboxTable}}
}

func (s *Store) seal(cmd string) ([]byte, error) {
	if s.sealer == nil {
		return []byte(cmd), nil
	}
	return s.sealer.Seal(cmd)
}

func (s *Store) open(b []byte) (string, error) {
	if s.sealer == nil {
		return string(b), nil
	}
	return s.sealer.Open(b)
}

// Create inserts a new Run and its ordered steps in one transaction. The run starts
// 'pending' (the sequencer claims + drives it later, T-055b); each step starts
// 'pending' with its command sealed at rest. It validates the plan is non-empty and
// every step carries a resource + command; the bounded-plan-size cap (PERF-6) is a
// launch-time check that lands with the launch route (T-057).
func (s *Store) Create(ctx context.Context, in Input) (Run, error) {
	if len(in.Steps) == 0 {
		return Run{}, errors.New("run requires at least one step")
	}
	for i, st := range in.Steps {
		if st.ResourceID == "" || st.Command == "" {
			return Run{}, fmt.Errorf("step %d requires resourceId and command", i)
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Run{}, fmt.Errorf("mint run id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// grant_id/actor are NULL for an operator-direct Run (NULLIF on ""), set for a
	// delegated Run (T-065). The owner/principal is always operator.
	if _, err := tx.Exec(ctx, `
		INSERT INTO runs (id, operator, grant_id, actor, launch_key)
		VALUES ($1, $2, NULLIF($3, '')::uuid, NULLIF($4, ''), $5)`,
		id.String(), in.Operator, in.GrantID, in.Actor, in.LaunchKey); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique (grant_id, launch_key)
			return Run{}, ErrDuplicateLaunch
		}
		return Run{}, fmt.Errorf("insert run: %w", err)
	}

	steps := make([]Step, len(in.Steps))
	for i, st := range in.Steps {
		sealed, err := s.seal(st.Command)
		if err != nil {
			return Run{}, fmt.Errorf("seal step %d command: %w", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO run_steps (run_id, step_index, resource_id, command)
			VALUES ($1, $2, $3, $4)`,
			id.String(), i, st.ResourceID, sealed); err != nil {
			return Run{}, fmt.Errorf("insert step %d: %w", i, err)
		}
		steps[i] = Step{Index: i, ResourceID: st.ResourceID, Command: st.Command, Status: StepPending}
	}

	// run.launched in the SAME transaction as the Run insert (AUD-9): a launched Run
	// always has its launch audited. Content-free — the actor + the run:<id> correlation
	// anchor, never the commands (ADR-0049/0061 GOV-1). The actor is the agent for a
	// delegated Run, else the launching operator (behavior-preserving); the principal is
	// the owner (in.Operator), queryable for the evidence packet (AUD-17, ADR-0062).
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: launchActor(in), Edge: launchEdge,
		Target: "run:" + id.String(), Outcome: "run.launched",
		EvidencePacketID: "run:" + id.String(),
	}); err != nil {
		return Run{}, fmt.Errorf("enqueue run.launched: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit run: %w", err)
	}
	return Run{ID: id.String(), Operator: in.Operator, Actor: in.Actor, GrantID: in.GrantID, Status: StatusPending, Steps: steps}, nil
}

// GetByLaunchKey returns the Run a prior delegated launch created for (grantID, launchKey),
// or ErrNotFound — the idempotent re-attach path the handler uses after Create reports
// ErrDuplicateLaunch (an agent retry of run.launch, ADR-0062 T-065a).
func (s *Store) GetByLaunchKey(ctx context.Context, grantID, launchKey string) (Run, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM runs WHERE grant_id = $1::uuid AND launch_key = $2`, grantID, launchKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run by launch key: %w", err)
	}
	return s.Get(ctx, id)
}

// CountActiveForOperator counts an operator's not-yet-terminal Runs (pending or
// running) — the per-operator concurrency cap input at launch (PERF-6).
func (s *Store) CountActiveForOperator(ctx context.Context, operator string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM runs WHERE operator = $1 AND status IN ('pending', 'running')`, operator).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active runs: %w", err)
	}
	return n, nil
}

// CountActiveForGrant counts a grant's not-yet-terminal Runs — the per-grant concurrency
// input at delegated launch (PERF-7, ADR-0062).
func (s *Store) CountActiveForGrant(ctx context.Context, grantID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM runs WHERE grant_id = $1::uuid AND status IN ('pending', 'running')`, grantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active grant runs: %w", err)
	}
	return n, nil
}

// CountStepsForGrant counts the total PLANNED steps across ALL of a grant's Runs — the
// step-budget consumption input (PERF-7). A failed/canceled Run still consumed its planned
// steps (the ratified semantics), so this counts every run_steps row for the grant's Runs.
func (s *Store) CountStepsForGrant(ctx context.Context, grantID string) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM run_steps rs JOIN runs r ON r.id = rs.run_id
		 WHERE r.grant_id = $1::uuid`, grantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count grant steps: %w", err)
	}
	return n, nil
}

// CancelRunsForGrant cancels every not-yet-terminal Run bound to a grant (DUR-7, ADR-0062):
// when an operator revokes a grant, its in-flight Runs stop. Each is moved to canceled with a
// run.canceled event (reusing CancelRun/setRunTerminal); the sequencer's between-step
// RunStatus check then halts further dispatch. Returns the number canceled.
func (s *Store) CancelRunsForGrant(ctx context.Context, grantID string) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM runs WHERE grant_id = $1::uuid AND status IN ('pending', 'running')`, grantID)
	if err != nil {
		return 0, fmt.Errorf("scan grant runs: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		ok, err := s.CancelRun(ctx, id) // setRunTerminal(canceled) + run.canceled in-tx
		if err != nil {
			return n, err
		}
		if ok {
			n++
		}
	}
	return n, nil
}

// Get returns a Run with its steps in execution order, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	var r Run
	err := s.pool.QueryRow(ctx, `
		SELECT id, operator, COALESCE(grant_id::text, ''), COALESCE(actor, ''), status, created_at, updated_at
		  FROM runs WHERE id = $1`, id).
		Scan(&r.ID, &r.Operator, &r.GrantID, &r.Actor, &r.Status, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("get run: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT step_index, resource_id, command, COALESCE(invocation_id::text, ''),
		       status, outcome, created_at, updated_at
		  FROM run_steps WHERE run_id = $1 ORDER BY step_index`, id)
	if err != nil {
		return Run{}, fmt.Errorf("get run steps: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		var cmd []byte
		if err := rows.Scan(&st.Index, &st.ResourceID, &cmd, &st.InvocationID,
			&st.Status, &st.Outcome, &st.CreatedAt, &st.UpdatedAt); err != nil {
			return Run{}, err
		}
		if st.Command, err = s.open(cmd); err != nil {
			return Run{}, fmt.Errorf("open step %d command: %w", st.Index, err)
		}
		r.Steps = append(r.Steps, st)
	}
	if err := rows.Err(); err != nil {
		return Run{}, err
	}
	return r, nil
}

// List returns up to limit runs in id (≈ creation) order with id > after
// (after="" starts at the head), without their steps (steps load on Get). The cursor
// is the last id, or "" when empty.
// It is operator-scoped (owner-only, matching GetRun): an operator never lists another
// operator's runs (T-059a).
func (s *Store) ListForOperator(ctx context.Context, operator, after string, limit int) ([]Run, string, error) {
	const cols = `SELECT id, operator, status, created_at, updated_at FROM runs WHERE operator = $1`
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, cols+` ORDER BY id ASC LIMIT $2`, operator, limit)
	} else {
		rows, err = s.pool.Query(ctx, cols+` AND id > $2 ORDER BY id ASC LIMIT $3`, operator, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Run
	var last string
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Operator, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, r)
		last = r.ID
	}
	return out, last, rows.Err()
}

// NextClaimable returns up to limit run ids the sequencer may drive — a fresh
// 'pending' Run OR a 'running' Run whose lease has expired (a crashed driver, T-056) —
// in id order (uses the runs_active_idx partial index; claimed_until is post-filtered
// on the small active set).
func (s *Store) NextClaimable(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM runs
		 WHERE status = 'pending'
		    OR (status = 'running' AND (claimed_until IS NULL OR claimed_until < now()))
		 ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("scan claimable runs: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClaimRun atomically takes a Run for driving — a 'pending' Run or a 'running' Run with
// an EXPIRED lease (the reclaimable single-winner claim, DUR-2) — moving it to running
// with a fresh lease (claimed_until = now()+leaseSecs). claimed=false means another
// driver holds a live lease, or the Run left the claimable set.
func (s *Store) ClaimRun(ctx context.Context, id string, leaseSecs int) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE runs SET status = 'running', claimed_until = now() + $2 * interval '1 second', updated_at = now()
		 WHERE id = $1
		   AND (status = 'pending'
		     OR (status = 'running' AND (claimed_until IS NULL OR claimed_until < now())))`, id, leaseSecs)
	if err != nil {
		return false, fmt.Errorf("claim run: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// RenewClaim extends a running Run's lease so a live driver is not reclaimed mid-step
// (T-056); the sequencer calls it before each step. Best-effort: 0 rows (the Run left
// 'running') is not an error.
func (s *Store) RenewClaim(ctx context.Context, id string, leaseSecs int) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE runs SET claimed_until = now() + $2 * interval '1 second', updated_at = now()
		 WHERE id = $1 AND status = 'running'`, id, leaseSecs); err != nil {
		return fmt.Errorf("renew claim: %w", err)
	}
	return nil
}

// SetStepResult records a step's status (+ a bounded outcome): 'running' at dispatch,
// then a terminal status. invocationID is the gateway invocation id (the run→invocation
// correlation anchor, AUD-15) — recorded at the terminal set when the held /v1/steps
// call returns it; "" leaves the existing value (NULL pre-dispatch). The UUID column
// rejects "", so NULLIF guards it. updated_at is bumped explicitly (no DB trigger).
// ErrNotFound if the (run, index) pair is absent.
func (s *Store) SetStepResult(ctx context.Context, runID string, index int, status, outcome, invocationID string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE run_steps
		   SET status = $3, outcome = $4,
		       invocation_id = COALESCE(NULLIF($5, '')::uuid, invocation_id), updated_at = now()
		 WHERE run_id = $1 AND step_index = $2`, runID, index, status, outcome, invocationID)
	if err != nil {
		return fmt.Errorf("set step result: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// runTerminalOutcome maps a Run's terminal status to its audit lifecycle outcome
// (AUD-14): completed/failed/canceled → run.completed/aborted/canceled.
var runTerminalOutcome = map[string]string{
	StatusCompleted: "run.completed",
	StatusFailed:    "run.aborted",
	StatusCanceled:  "run.canceled",
}

// RunStatus returns just a Run's status — the sequencer's lightweight between-step
// cancellation check (cheaper than Get, which loads the steps). ErrNotFound if absent.
func (s *Store) RunStatus(ctx context.Context, id string) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM runs WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("run status: %w", err)
	}
	return status, nil
}

// FinishRun moves a RUNNING Run to a terminal status (completed | failed) and emits the
// run.* event in-tx (AUD-14). The status='running' guard makes it a no-op when the Run
// already left running (an operator cancel won the race) — the sequencer ignores the
// result, so a no-op is silent (no spurious run.completed over a canceled Run).
func (s *Store) FinishRun(ctx context.Context, id, status string) error {
	_, err := s.setRunTerminal(ctx, id, status, "running")
	return err
}

// CancelRun cancels a Run mid-flight (DUR-5): pending|running → canceled + run.canceled
// in-tx. ok=false when the Run is no longer cancelable (already terminal) — the handler
// maps that to 409.
func (s *Store) CancelRun(ctx context.Context, id string) (bool, error) {
	return s.setRunTerminal(ctx, id, StatusCanceled, "pending", "running")
}

// setRunTerminal moves a Run to toStatus, guarded to fromStatuses, and emits the mapped
// run.* lifecycle event in the SAME transaction (AUD-9/14): actor=operator, anchored
// run:<id>, content-free. ok=false (no-op) if the row already left fromStatuses — the
// shared guard behind FinishRun (from running) and CancelRun (from pending|running) that
// makes a cancel/finish race safe.
func (s *Store) setRunTerminal(ctx context.Context, id, toStatus string, fromStatuses ...string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var operator, actor string
	err = tx.QueryRow(ctx, `
		UPDATE runs SET status = $2, updated_at = now()
		 WHERE id = $1 AND status = ANY($3) RETURNING operator, COALESCE(actor, '')`,
		id, toStatus, fromStatuses).Scan(&operator, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("set run terminal: %w", err)
	}
	if outcome, ok := runTerminalOutcome[toStatus]; ok {
		if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
			Timestamp: time.Now(), Actor: eventActor(operator, actor), Edge: launchEdge,
			Target: "run:" + id, Outcome: outcome, EvidencePacketID: "run:" + id,
		}); err != nil {
			return false, fmt.Errorf("enqueue %s: %w", outcome, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit run terminal: %w", err)
	}
	return true, nil
}
