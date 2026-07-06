package grant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// ErrNotFound is returned when no grant has the given id.
var ErrNotFound = errors.New("grant not found")

// grantEdge is the bracket edge for grant lifecycle events — issuing/revoking a grant is
// a human-agent authorization act (ADR-0062 §4; the per-grant consent that replaces
// informed-launch).
const grantEdge = "human-agent"

// selectCols is the shared column list for Get/List (kept in one place so the scan order
// matches). budget_window_seconds is nullable (WS-4): COALESCE maps NULL → 0 (unwindowed,
// the v0.6 semantics).
const selectCols = `id, principal, agent, label, resources, verbs, expires_at,
	max_steps_per_run, max_concurrent_runs, step_budget, COALESCE(budget_window_seconds, 0),
	status, created_at, updated_at`

// Store persists delegation_grants for the grant engine. It takes the pgx pool directly
// (the internal/run pattern) so the package stays extraction-ready.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

// NewStore builds the grant store. outboxTable is the api outbox table (e.g. "api_outbox")
// into which Create/Revoke enqueue grant.* events in the same transaction (AUD-9/AUD-16);
// the caller passes the table name so this package stays free of api-internal imports (the
// extraction seam).
func NewStore(pool *pgxpool.Pool, outboxTable string) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: outboxTable}}
}

// Create inserts a new grant and enqueues grant.issued in the same transaction (AUD-16):
// an issued grant always has its issuance audited. It owns the transaction; the insert +
// validation body lives in CreateInTx so the workstream charter (ADR-0063 WS-2) can issue
// its grant inside ITS transaction — one issuance path, two callers (SEC-30).
func (s *Store) Create(ctx context.Context, in Input) (Grant, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Grant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	g, err := s.CreateInTx(ctx, tx, in)
	if err != nil {
		return Grant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Grant{}, fmt.Errorf("commit grant: %w", err)
	}
	return g, nil
}

// CreateInTx inserts a new grant + enqueues grant.issued on the CALLER's transaction —
// the T-071 extraction of Create's body (behavior-preserving) so the workstream charter
// can create its grant atomically with the charter row (WS-2). It validates the grant is
// well-formed (an agent, ≥1 resource + verb, a future expiry, positive bounds) —
// fail-loud, no partial row. The caller owns commit/rollback.
func (s *Store) CreateInTx(ctx context.Context, tx pgx.Tx, in Input) (Grant, error) {
	if in.Agent == "" || len(in.Resources) == 0 || len(in.Verbs) == 0 {
		return Grant{}, errors.New("grant requires an agent, at least one resource, and at least one verb")
	}
	if in.MaxStepsPerRun <= 0 || in.MaxConcurrentRuns <= 0 || in.StepBudget <= 0 {
		return Grant{}, errors.New("grant bounds (maxStepsPerRun, maxConcurrentRuns, stepBudget) must be positive")
	}
	if in.BudgetWindowSeconds < 0 {
		return Grant{}, errors.New("grant budget window must not be negative")
	}
	if !in.ExpiresAt.After(time.Now()) {
		return Grant{}, errors.New("grant expiry must be in the future")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Grant{}, fmt.Errorf("mint grant id: %w", err)
	}

	// budget_window_seconds is NULL when unwindowed (WS-4): NULLIF maps 0 → NULL, so a
	// v0.6-shaped Input keeps the v0.6 row shape exactly.
	var createdAt, updatedAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO delegation_grants
		  (id, principal, agent, label, resources, verbs, expires_at,
		   max_steps_per_run, max_concurrent_runs, step_budget, budget_window_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, 0))
		RETURNING created_at, updated_at`,
		id.String(), in.Principal, in.Agent, in.Label, in.Resources, in.Verbs, in.ExpiresAt,
		in.MaxStepsPerRun, in.MaxConcurrentRuns, in.StepBudget, in.BudgetWindowSeconds).
		Scan(&createdAt, &updatedAt); err != nil {
		return Grant{}, fmt.Errorf("insert grant: %w", err)
	}

	// grant.issued in the SAME transaction (AUD-16): content-free — the principal + the
	// grant:<id> anchor, never any command (a grant has none).
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.Principal, Edge: grantEdge,
		Target: "grant:" + id.String(), Outcome: "grant.issued",
		EvidencePacketID: "grant:" + id.String(),
	}); err != nil {
		return Grant{}, fmt.Errorf("enqueue grant.issued: %w", err)
	}

	return Grant{
		ID: id.String(), Principal: in.Principal, Agent: in.Agent, Label: in.Label,
		Resources: in.Resources, Verbs: in.Verbs, ExpiresAt: in.ExpiresAt,
		MaxStepsPerRun: in.MaxStepsPerRun, MaxConcurrentRuns: in.MaxConcurrentRuns,
		StepBudget: in.StepBudget, BudgetWindowSeconds: in.BudgetWindowSeconds,
		Status: StatusActive, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

// Get returns a grant by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Grant, error) {
	var g Grant
	err := s.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM delegation_grants WHERE id = $1`, id).
		Scan(&g.ID, &g.Principal, &g.Agent, &g.Label, &g.Resources, &g.Verbs, &g.ExpiresAt,
			&g.MaxStepsPerRun, &g.MaxConcurrentRuns, &g.StepBudget, &g.BudgetWindowSeconds,
			&g.Status, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, ErrNotFound
	}
	if err != nil {
		return Grant{}, fmt.Errorf("get grant: %w", err)
	}
	return g, nil
}

// ListForOperator returns up to limit of the principal's grants in id (≈ creation) order
// with id > after (after="" starts at the head). Owner-scoped (a principal never lists
// another operator's grants), matching the runs list. The cursor is the last id, or "".
func (s *Store) ListForOperator(ctx context.Context, principal, after string, limit int) ([]Grant, string, error) {
	const cols = `SELECT ` + selectCols + ` FROM delegation_grants WHERE principal = $1`
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
	var out []Grant
	var last string
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.ID, &g.Principal, &g.Agent, &g.Label, &g.Resources, &g.Verbs, &g.ExpiresAt,
			&g.MaxStepsPerRun, &g.MaxConcurrentRuns, &g.StepBudget, &g.BudgetWindowSeconds,
			&g.Status, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, g)
		last = g.ID
	}
	return out, last, rows.Err()
}

// Revoke flips an active grant to revoked and emits grant.revoked in the SAME transaction
// (AUD-16): actor = principal, anchored grant:<id>, content-free. ok=false (no-op) when the
// grant is not active (unknown id OR already revoked) — the handler pre-checks ownership via
// Get (→ 404), so a false here maps to 409. It owns the transaction; the body lives in
// RevokeInTx so the workstream retire (ADR-0063 §7, T-074) can revoke the charter's grant
// atomically with the charter flip — one revoke path, two callers (SEC-30).
func (s *Store) Revoke(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ok, err := s.RevokeInTx(ctx, tx, id)
	if err != nil || !ok {
		return false, err // no-op needs no commit (nothing changed)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit revoke: %w", err)
	}
	return true, nil
}

// RevokeInTx flips an active grant to revoked + enqueues grant.revoked on the CALLER's
// transaction — the T-074 extraction of Revoke's body (behavior-preserving; the CreateInTx
// precedent). ok=false → the grant was not active (unknown OR already revoked — e.g. a
// direct revoke raced the charter's retire): the caller proceeds without a duplicate
// grant.revoked. The caller owns commit/rollback.
func (s *Store) RevokeInTx(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var principal string
	err := tx.QueryRow(ctx, `
		UPDATE delegation_grants SET status = 'revoked', updated_at = now()
		 WHERE id = $1 AND status = 'active' RETURNING principal`, id).Scan(&principal)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("revoke grant: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: principal, Edge: grantEdge,
		Target: "grant:" + id, Outcome: "grant.revoked", EvidencePacketID: "grant:" + id,
	}); err != nil {
		return false, fmt.Errorf("enqueue grant.revoked: %w", err)
	}
	return true, nil
}

// AuditLaunchDenied records a denied delegated launch — a plan-vs-grant rejection (SEC-29,
// ADR-0062 N1): the launch is refused before any Run is created or any egress occurs.
// Content-free: the agent (actor) + the grant:<id> anchor, never a command or the reason
// (the reason is the 403 body + the api log, kept out of the chain like every command).
func (s *Store) AuditLaunchDenied(ctx context.Context, agent, grantID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: agent, Edge: grantEdge,
		Target: "grant:" + grantID, Outcome: "run.launch.denied",
		EvidencePacketID: "grant:" + grantID,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
