package run

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no run has the given id.
var ErrNotFound = errors.New("run not found")

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
}

// NewStore builds the Run store. sealer may be nil (plaintext fallback — see
// CommandSealer).
func NewStore(pool *pgxpool.Pool, sealer CommandSealer) *Store {
	return &Store{pool: pool, sealer: sealer}
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

	if _, err := tx.Exec(ctx, `INSERT INTO runs (id, operator) VALUES ($1, $2)`,
		id.String(), in.Operator); err != nil {
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

	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("commit run: %w", err)
	}
	return Run{ID: id.String(), Operator: in.Operator, Status: StatusPending, Steps: steps}, nil
}

// Get returns a Run with its steps in execution order, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Run, error) {
	var r Run
	err := s.pool.QueryRow(ctx, `
		SELECT id, operator, status, created_at, updated_at FROM runs WHERE id = $1`, id).
		Scan(&r.ID, &r.Operator, &r.Status, &r.CreatedAt, &r.UpdatedAt)
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
func (s *Store) List(ctx context.Context, after string, limit int) ([]Run, string, error) {
	const cols = `SELECT id, operator, status, created_at, updated_at FROM runs`
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, cols+` ORDER BY id ASC LIMIT $1`, limit)
	} else {
		rows, err = s.pool.Query(ctx, cols+` WHERE id > $1 ORDER BY id ASC LIMIT $2`, after, limit)
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
// then a terminal status. updated_at is bumped explicitly (no DB trigger). ErrNotFound
// if the (run, index) pair is absent.
func (s *Store) SetStepResult(ctx context.Context, runID string, index int, status, outcome string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE run_steps SET status = $3, outcome = $4, updated_at = now()
		 WHERE run_id = $1 AND step_index = $2`, runID, index, status, outcome)
	if err != nil {
		return fmt.Errorf("set step result: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FinishRun moves a Run to a terminal status (completed | failed | canceled).
// ErrNotFound if no row matched.
func (s *Store) FinishRun(ctx context.Context, id, status string) error {
	ct, err := s.pool.Exec(ctx, `UPDATE runs SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
