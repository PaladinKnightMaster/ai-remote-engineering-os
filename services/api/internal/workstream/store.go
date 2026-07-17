package workstream

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
	"aireos.local/services/api/internal/grant"
)

// ErrNotFound is returned when no workstream has the given id.
var ErrNotFound = errors.New("workstream not found")

// ErrPlanRequestNotFound is returned when no plan request matches (PLAN-1, T-087).
var ErrPlanRequestNotFound = errors.New("plan request not found")

// charterEdge is the bracket edge for workstream lifecycle events — chartering (and
// pausing/retiring, T-074) is a human-agent authorization act (ADR-0063 §2: the ONE
// consent act, paired with the grant's own grant.issued).
const charterEdge = "human-agent"

// CommandSealer seals/opens the operator-authored template command at rest — the same
// contract as the run store's (F3 parity); a nil sealer is the plaintext dev/transition
// fallback, matching the run store's current wiring so both flip together when the key
// is provisioned.
type CommandSealer interface {
	Seal(plaintext string) ([]byte, error)
	Open(b []byte) (string, error)
}

// GrantWriter is the slice of the grant store the charter lifecycle writes through,
// always inside the WORKSTREAM's transaction: CreateInTx issues the charter's grant at
// consent (WS-2 atomicity, T-071); RevokeInTx revokes it at retire (WS-5, T-074 — the
// charter flip + the grant revoke commit or roll back together). Satisfied by
// *grant.Store — one issuance path and one revoke path, each with two callers (SEC-30).
type GrantWriter interface {
	CreateInTx(ctx context.Context, tx pgx.Tx, in grant.Input) (grant.Grant, error)
	RevokeInTx(ctx context.Context, tx pgx.Tx, id string) (bool, error)
}

// selectCols is the shared column list for Get/List (kept in one place so the scan
// order matches). kind/composer (T-087, WS-5) are appended at the end so every existing
// Scan call site keeps its original column positions.
const selectCols = `id, principal, label, objective, grant_id, interval_seconds,
	next_fire_at, status, created_at, updated_at, kind, composer`

// Store persists workstreams + workstream_steps. It takes the pgx pool directly (the
// internal/run pattern) so the package stays extraction-ready.
type Store struct {
	pool   *pgxpool.Pool
	sealer CommandSealer
	grants GrantWriter
	writer outbox.Writer
}

// NewStore builds the workstream store. sealer may be nil (plaintext fallback — see
// CommandSealer). grants issues the charter's grant inside the charter tx (WS-2).
// outboxTable is the api outbox table (e.g. "api_outbox") into which Create enqueues
// workstream.chartered in the same transaction.
func NewStore(pool *pgxpool.Pool, sealer CommandSealer, grants GrantWriter, outboxTable string) *Store {
	return &Store{pool: pool, sealer: sealer, grants: grants, writer: outbox.Writer{Table: outboxTable}}
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

// Create charters a Workstream: ONE transaction inserts the workstream + its immutable
// template steps (sealed) + the charter's auto-issued delegation grant (grantee =
// workstream:<id>, overridden here so a caller can never bind a foreign grantee) and
// enqueues workstream.chartered — the grant's own CreateInTx enqueues grant.issued in
// the SAME tx (WS-2: the one consent act is atomically audited as both events, or
// nothing exists). First fire = now + interval (no immediate fire, ADR-0063 §5).
func (s *Store) Create(ctx context.Context, in Input) (Workstream, error) {
	if in.Principal == "" || in.Objective == "" {
		return Workstream{}, errors.New("workstream requires a principal and an objective")
	}
	if in.IntervalSeconds <= 0 {
		return Workstream{}, errors.New("workstream interval must be positive")
	}
	// The kind matrix (WS-5, ADR-0065 §4): "" defaults to KindTemplate so every
	// pre-v0.9 caller is behavior-preserved. KindTemplate requires no composer and
	// >=1 step; KindAdaptive requires a named composer and zero steps — Create
	// rejects any other combination (the DB CHECK backstops it structurally).
	kind := in.Kind
	if kind == "" {
		kind = KindTemplate
	}
	switch kind {
	case KindTemplate:
		if in.Composer != "" {
			return Workstream{}, errors.New("a template charter must not name a composer")
		}
		if len(in.Steps) == 0 {
			return Workstream{}, errors.New("workstream requires at least one template step")
		}
	case KindAdaptive:
		if in.Composer == "" {
			return Workstream{}, errors.New("an adaptive charter requires a named composer")
		}
		if len(in.Steps) != 0 {
			return Workstream{}, errors.New("an adaptive charter must carry zero template steps")
		}
	default:
		return Workstream{}, fmt.Errorf("unknown workstream kind %q", kind)
	}
	for i, st := range in.Steps {
		if st.ResourceID == "" || st.Command == "" {
			return Workstream{}, fmt.Errorf("template step %d requires resourceId and command", i)
		}
	}
	if s.grants == nil {
		return Workstream{}, errors.New("workstream store has no grant creator")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Workstream{}, fmt.Errorf("mint workstream id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workstream{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// The charter's grant, in the SAME tx (WS-2). Grantee/principal are bound here —
	// never caller-supplied (the SEC-26 posture: the grant is server-side authority).
	// An adaptive charter's grantee is the NAMED COMPOSER (R2, ADR-0065 §4) — the one
	// behavior change Create makes; a template charter's grantee stays
	// workstream:<id>, exactly as before v0.9 (byte-behavior-preserved).
	gin := in.Grant
	gin.Principal = in.Principal
	gin.Agent = "workstream:" + id.String()
	if kind == KindAdaptive {
		gin.Agent = in.Composer
	}
	g, err := s.grants.CreateInTx(ctx, tx, gin)
	if err != nil {
		return Workstream{}, fmt.Errorf("issue charter grant: %w", err)
	}

	var nextFireAt time.Time
	var createdAt, updatedAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO workstreams (id, principal, label, objective, grant_id, interval_seconds, next_fire_at, kind, composer)
		VALUES ($1, $2, $3, $4, $5, $6::int, now() + $6::int * interval '1 second', $7, $8)
		RETURNING next_fire_at, created_at, updated_at`,
		id.String(), in.Principal, in.Label, in.Objective, g.ID, in.IntervalSeconds, kind, in.Composer).
		Scan(&nextFireAt, &createdAt, &updatedAt); err != nil {
		return Workstream{}, fmt.Errorf("insert workstream: %w", err)
	}

	steps := make([]Step, len(in.Steps))
	for i, st := range in.Steps {
		sealed, err := s.seal(st.Command)
		if err != nil {
			return Workstream{}, fmt.Errorf("seal template step %d: %w", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workstream_steps (workstream_id, step_index, resource_id, command)
			VALUES ($1, $2, $3, $4)`,
			id.String(), i, st.ResourceID, sealed); err != nil {
			return Workstream{}, fmt.Errorf("insert template step %d: %w", i, err)
		}
		steps[i] = Step{Index: i, ResourceID: st.ResourceID, Command: st.Command}
	}

	// workstream.chartered in the SAME transaction — content-free (the objective and the
	// template stay off the chain, ADR-0049); the workstream:<id> anchor pairs with the
	// grant's grant.issued from CreateInTx above (AUD-18).
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.Principal, Edge: charterEdge,
		Target: "workstream:" + id.String(), Outcome: "workstream.chartered",
		EvidencePacketID: "workstream:" + id.String(),
	}); err != nil {
		return Workstream{}, fmt.Errorf("enqueue workstream.chartered: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Workstream{}, fmt.Errorf("commit workstream: %w", err)
	}
	return Workstream{
		ID: id.String(), Principal: in.Principal, Label: in.Label, Objective: in.Objective,
		GrantID: g.ID, IntervalSeconds: in.IntervalSeconds, NextFireAt: nextFireAt,
		Status: StatusActive, CreatedAt: createdAt, UpdatedAt: updatedAt, Steps: steps,
		Kind: kind, Composer: in.Composer,
	}, nil
}

// Get returns a workstream with its template (opened), or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Workstream, error) {
	var w Workstream
	err := s.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM workstreams WHERE id = $1`, id).
		Scan(&w.ID, &w.Principal, &w.Label, &w.Objective, &w.GrantID, &w.IntervalSeconds,
			&w.NextFireAt, &w.Status, &w.CreatedAt, &w.UpdatedAt, &w.Kind, &w.Composer)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workstream{}, ErrNotFound
	}
	if err != nil {
		return Workstream{}, fmt.Errorf("get workstream: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT step_index, resource_id, command FROM workstream_steps
		 WHERE workstream_id = $1 ORDER BY step_index ASC`, id)
	if err != nil {
		return Workstream{}, fmt.Errorf("get template: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		var sealed []byte
		if err := rows.Scan(&st.Index, &st.ResourceID, &sealed); err != nil {
			return Workstream{}, err
		}
		if st.Command, err = s.open(sealed); err != nil {
			return Workstream{}, fmt.Errorf("open template step %d: %w", st.Index, err)
		}
		w.Steps = append(w.Steps, st)
	}
	return w, rows.Err()
}

// DueRef is one due charter from the ticker scan: the id plus the EXACT fire time the
// claim CASes on (and the tick's launch key derives from).
type DueRef struct {
	ID         string
	NextFireAt time.Time
}

// DueWorkstreams returns up to limit active charters whose next_fire_at is due — the
// ticker scan (WS-3; served by the workstreams_due_idx partial index).
func (s *Store) DueWorkstreams(ctx context.Context, limit int) ([]DueRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, next_fire_at FROM workstreams
		 WHERE status = 'active' AND next_fire_at <= now()
		 ORDER BY next_fire_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("scan due workstreams: %w", err)
	}
	defer rows.Close()
	var out []DueRef
	for rows.Next() {
		var d DueRef
		if err := rows.Scan(&d.ID, &d.NextFireAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimTick single-winner-claims one due tick (WS-3): a compare-and-swap on the EXACT
// scanned fire time that advances next_fire_at from now() — NOT from the old fire time,
// so missed ticks collapse to one (no backfill after downtime). ok=false → another
// scanner won the CAS, or the charter left 'active' — skip.
//
// The claim advances BEFORE the launch: a crash in the claim→launch window drops that
// one firing and the charter self-heals next interval — the accepted, self-throttling
// trade (the T-073 audit's F1) over launch-first's exactly-once-but-retry-hammering.
// (The fireAt value round-trips losslessly through pgx's microsecond timestamptz codec,
// so the equality is exact — proven by the single-winner integration race.)
func (s *Store) ClaimTick(ctx context.Context, id string, fireAt time.Time) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE workstreams
		   SET next_fire_at = now() + interval_seconds * interval '1 second', updated_at = now()
		 WHERE id = $1 AND status = 'active' AND next_fire_at = $2`, id, fireAt)
	if err != nil {
		return false, fmt.Errorf("claim tick: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// Pause soft-stops an active charter (WS-5, ADR-0063 §7): status → paused, so the
// ticker's claim predicate skips it; in-flight Runs COMPLETE (pause is not a kill
// switch — retire is); the grant stays active. workstream.paused chains in-tx (actor =
// the row's principal — owner-scoped by the handler, so ≡ the calling operator).
// ok=false → not active (nothing changed; the handler answers 409).
func (s *Store) Pause(ctx context.Context, id string) (bool, error) {
	return s.flipWithEvent(ctx, `
		UPDATE workstreams SET status = 'paused', updated_at = now()
		 WHERE id = $1 AND status = 'active' RETURNING principal`, id, "workstream.paused")
}

// Resume re-arms a paused charter (WS-5): status → active with next_fire_at = NOW +
// interval — never the stale pre-pause fire time, so resuming does NOT catch-up-fire
// (the no-backfill rule extended to resume). workstream.resumed chains in-tx. ok=false →
// not paused (409).
func (s *Store) Resume(ctx context.Context, id string) (bool, error) {
	return s.flipWithEvent(ctx, `
		UPDATE workstreams
		   SET status = 'active', next_fire_at = now() + interval_seconds * interval '1 second', updated_at = now()
		 WHERE id = $1 AND status = 'paused' RETURNING principal`, id, "workstream.resumed")
}

// Retire terminally ends a charter (WS-5, the operator kill switch): ONE transaction
// flips the charter retired (from active OR paused — pause is not a shield) AND revokes
// its grant via the shared RevokeInTx, chaining workstream.retired (+ the grant's own
// grant.revoked) from the same tx. RevokeInTx tolerates an already-dead grant (a direct
// revoke raced the retire — ok=false there, no duplicate grant.revoked, the charter flip
// still commits). The in-flight-Run cascade runs in the HANDLER post-tx, best-effort
// (the RevokeGrant mirror; the documented v0.6 non-atomic residual carries). ok=false →
// already retired (409).
func (s *Store) Retire(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var principal, grantID string
	err = tx.QueryRow(ctx, `
		UPDATE workstreams SET status = 'retired', updated_at = now()
		 WHERE id = $1 AND status IN ('active', 'paused') RETURNING principal, grant_id`, id).
		Scan(&principal, &grantID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("retire workstream: %w", err)
	}
	if _, err := s.grants.RevokeInTx(ctx, tx, grantID); err != nil {
		return false, fmt.Errorf("revoke charter grant: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: principal, Edge: charterEdge,
		Target: "workstream:" + id, Outcome: "workstream.retired",
		EvidencePacketID: "workstream:" + id,
	}); err != nil {
		return false, fmt.Errorf("enqueue workstream.retired: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit retire: %w", err)
	}
	return true, nil
}

// flipWithEvent runs one guarded status flip + its lifecycle event in a tx (the
// Pause/Resume shape; Retire has its own body — it also revokes the grant).
func (s *Store) flipWithEvent(ctx context.Context, updateSQL, id, outcome string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var principal string
	err = tx.QueryRow(ctx, updateSQL, id).Scan(&principal)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%s: %w", outcome, err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: principal, Edge: charterEdge,
		Target: "workstream:" + id, Outcome: outcome,
		EvidencePacketID: "workstream:" + id,
	}); err != nil {
		return false, fmt.Errorf("enqueue %s: %w", outcome, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit %s: %w", outcome, err)
	}
	return true, nil
}

// SelfRetire retires an ACTIVE charter whose grant is dead — revoked or expired (the F1
// self-retire, WS-3): without it, every charter outliving its grant TTL would spin
// eternal run.launch.denied every interval. Status → retired with
// workstream.retired:grant-dead chained in the same tx (the approval.denied:timeout
// outcome-qualifier pattern; actor = the workstream — the machine transition; T-074's
// operator verb emits the plain workstream.retired with actor = operator). The charter's
// grant needs no action here: a revoked grant's cascade already canceled its Runs, and an
// expired grant's Runs complete on their own. ok=false → not active (already retired, or
// paused — invisible to the claim predicate anyway).
func (s *Store) SelfRetire(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ct, err := tx.Exec(ctx, `
		UPDATE workstreams SET status = 'retired', updated_at = now()
		 WHERE id = $1 AND status = 'active'`, id)
	if err != nil {
		return false, fmt.Errorf("self-retire: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: "workstream:" + id, Edge: charterEdge,
		Target: "workstream:" + id, Outcome: "workstream.retired:grant-dead",
		EvidencePacketID: "workstream:" + id,
	}); err != nil {
		return false, fmt.Errorf("enqueue workstream.retired: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit self-retire: %w", err)
	}
	return true, nil
}

// ListForOperator returns up to limit of the principal's workstreams in id (≈ creation)
// order with id > after (after="" starts at the head), WITHOUT templates (the runs-list
// pattern). Owner-scoped. The cursor is the last id, or "".
func (s *Store) ListForOperator(ctx context.Context, principal, after string, limit int) ([]Workstream, string, error) {
	const cols = `SELECT ` + selectCols + ` FROM workstreams WHERE principal = $1`
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, cols+` ORDER BY id ASC LIMIT $2`, principal, limit)
	} else {
		rows, err = s.pool.Query(ctx, cols+` AND id > $2 ORDER BY id ASC LIMIT $3`, principal, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Workstream
	var last string
	for rows.Next() {
		var w Workstream
		if err := rows.Scan(&w.ID, &w.Principal, &w.Label, &w.Objective, &w.GrantID, &w.IntervalSeconds,
			&w.NextFireAt, &w.Status, &w.CreatedAt, &w.UpdatedAt, &w.Kind, &w.Composer); err != nil {
			return nil, "", err
		}
		out = append(out, w)
		last = w.ID
	}
	return out, last, rows.Err()
}

// --- Plan requests (PLAN-1, ADR-0065 §5, T-087) -----------------------------------
//
// The inbound plan channel's store primitives: no routes, no ticker branch, no audit
// events here — T-088 creates the `proposed` Run plan.submit produces, T-089 wires the
// ticker's adaptive branch (open) + the expiry sweep + the revocation cascade (close),
// and T-090 wires workstream.poll/plan.submit's outbox events around these calls. These
// methods deliberately run outside any caller-supplied transaction (unlike
// AccrueRunTerminal's tx-param shape) because no consuming caller composes them with
// another write yet; a *InTx variant is the natural T-089 seam if the ticker needs to
// pair an open/expire with its own outbox enqueue atomically — noted, not preempted.

// planRequestSelectCols is the shared column list for plan-request reads. run_id is
// NULL until MarkPlanRequestSubmitted sets it (COALESCE to "" — the run/grant store's
// nullable-FK-as-string convention).
const planRequestSelectCols = `id, workstream_id, launch_key, status, COALESCE(run_id::text, ''), expires_at, created_at, updated_at`

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query, via
// rows.Next()+scan), so every plan-request read shares one scan body (the
// internal/memory Store.scan precedent).
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlanRequest(row rowScanner) (PlanRequest, error) {
	var pr PlanRequest
	if err := row.Scan(&pr.ID, &pr.WorkstreamID, &pr.LaunchKey, &pr.Status, &pr.RunID,
		&pr.ExpiresAt, &pr.CreatedAt, &pr.UpdatedAt); err != nil {
		return PlanRequest{}, err
	}
	return pr, nil
}

// OpenPlanRequest opens a durable, TTL-bounded plan request for an adaptive charter's
// tick (PLAN-1) — the inbound-channel counterpart to the template branch's
// LaunchBounded call. Idempotent, the house flip-guard style: a duplicate open — the
// same launch_key retried, or a second open attempt while the charter already has one
// OPEN (both collide on a unique constraint: launch_key UNIQUE, or the partial
// plan_requests_one_open_idx, C2) — is a NO-OP that returns the charter's current open
// request rather than erroring or minting a second row.
func (s *Store) OpenPlanRequest(ctx context.Context, workstreamID, launchKey string, expiresAt time.Time) (PlanRequest, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return PlanRequest{}, fmt.Errorf("mint plan request id: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO plan_requests (id, workstream_id, launch_key, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING `+planRequestSelectCols,
		id.String(), workstreamID, launchKey, expiresAt)
	pr, err := scanPlanRequest(row)
	if err == nil {
		return pr, nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return PlanRequest{}, fmt.Errorf("open plan request: %w", err)
	}
	existing, gerr := s.openPlanRequestForWorkstream(ctx, workstreamID)
	if gerr != nil {
		return PlanRequest{}, fmt.Errorf("re-fetch after duplicate open: %w", gerr)
	}
	return existing, nil
}

// openPlanRequestForWorkstream returns a charter's current OPEN request — the row
// OpenPlanRequest's duplicate-open path re-fetches (at most one exists, by
// construction: plan_requests_one_open_idx).
func (s *Store) openPlanRequestForWorkstream(ctx context.Context, workstreamID string) (PlanRequest, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+planRequestSelectCols+` FROM plan_requests
		 WHERE workstream_id = $1 AND status = 'open'`, workstreamID)
	pr, err := scanPlanRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanRequest{}, ErrPlanRequestNotFound
	}
	if err != nil {
		return PlanRequest{}, fmt.Errorf("get open plan request: %w", err)
	}
	return pr, nil
}

// GetPlanRequest returns one plan request, or ErrPlanRequestNotFound.
func (s *Store) GetPlanRequest(ctx context.Context, id string) (PlanRequest, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+planRequestSelectCols+` FROM plan_requests WHERE id = $1`, id)
	pr, err := scanPlanRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanRequest{}, ErrPlanRequestNotFound
	}
	if err != nil {
		return PlanRequest{}, fmt.Errorf("get plan request: %w", err)
	}
	return pr, nil
}

// MarkPlanRequestSubmitted flips one OPEN request to submitted, recording the `proposed`
// Run it produced (ADR-0065 §5/§6; plan.submit, T-090 wires the caller). Flip-guarded:
// ok=false when the request is no longer open (already submitted/expired/closed — a
// stale double-submit, or a race with expiry/revocation) so a caller never double-
// attaches a second Run to one request.
func (s *Store) MarkPlanRequestSubmitted(ctx context.Context, id, runID string) (bool, error) {
	if runID == "" {
		return false, errors.New("mark plan request submitted requires a run id")
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE plan_requests SET status = 'submitted', run_id = $2, updated_at = now()
		 WHERE id = $1 AND status = 'open'`, id, runID)
	if err != nil {
		return false, fmt.Errorf("mark plan request submitted: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// ExpirePlanRequestsDue tombstones up to batch OPEN requests whose expires_at has
// passed (DUR-10, PLAN-1: the charter never wedges — the next tick opens a fresh
// request). The FOR UPDATE SKIP LOCKED sub-query claims a bounded batch (the memory
// ExpireDue precedent) so overlapping sweeps never double-claim a row; the guarded
// predicate (status = 'open') is the exactly-once guard — a re-run touches nothing
// already expired/submitted/closed. No events emitted here — T-089's sweep owns
// plan.expired, wired around this call. Returns the count expired this pass.
func (s *Store) ExpirePlanRequestsDue(ctx context.Context, batch int) (int, error) {
	if batch <= 0 || batch > 100 {
		batch = 100
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE plan_requests SET status = 'expired', updated_at = now()
		 WHERE id IN (
		     SELECT id FROM plan_requests
		      WHERE status = 'open' AND expires_at <= now()
		      ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED)`, batch)
	if err != nil {
		return 0, fmt.Errorf("expire plan requests due: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// ClosePlanRequestsForWorkstream closes a charter's open plan request (DUR-10 §7: the
// revocation cascade re-aimed at the new surface) — the T-089 retire/self-retire hook.
// Returns the count closed (0 or 1: at most one is ever open).
func (s *Store) ClosePlanRequestsForWorkstream(ctx context.Context, workstreamID string) (int, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE plan_requests SET status = 'closed', updated_at = now()
		 WHERE workstream_id = $1 AND status = 'open'`, workstreamID)
	if err != nil {
		return 0, fmt.Errorf("close plan requests for workstream: %w", err)
	}
	return int(ct.RowsAffected()), nil
}

// ClosePlanRequestsForGrant closes the open plan request of the charter bound to a
// revoked/expired grant (DUR-10 §7) — the grant-revoke cascade's plan-request leg,
// alongside run.CancelRunsForGrant (T-089 wires the call). A grant binds at most one
// charter (1:1, WS-2), so this closes at most one row, but is grant-scoped (not
// workstream-scoped) to match the cascade's existing entry point (grant id, not
// workstream id, is what a revoke knows).
func (s *Store) ClosePlanRequestsForGrant(ctx context.Context, grantID string) (int, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE plan_requests SET status = 'closed', updated_at = now()
		 WHERE status = 'open' AND workstream_id IN (
		     SELECT id FROM workstreams WHERE grant_id = $1)`, grantID)
	if err != nil {
		return 0, fmt.Errorf("close plan requests for grant: %w", err)
	}
	return int(ct.RowsAffected()), nil
}
