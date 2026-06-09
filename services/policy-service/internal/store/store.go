// Package store is policy-service's Postgres access layer (ADR-0054). It owns the
// approval lifecycle: it parks a session-start that requires approval as a
// pending_approvals row, resolves it on the operator's approve/deny, and
// fail-closes un-actioned approvals past their expiry — and in the SAME
// transaction as each state change it enqueues the decision evidence into
// policy_outbox (ADR-0045 transactional outbox, no dual-write), which the relay
// drains into the audit-service chain authority.
//
// Audit outcomes (ADR-0054 §3; outcome carries kind+reason, the broker
// `closed:<reason>` precedent): policy.decision:requires_approval,
// approval.granted, approval.denied:operator, approval.denied:timeout. Every
// event carries target=resource:<id> and evidencePacketId=session:<id> — no raw
// payloads in the immutable chain (ADR-0049).
package store

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

// outboxTable is policy-service's own outbox table (shares the cockpit DB with
// outbox / api_outbox / connector_outbox, so it must not collide; ADR-0048).
const outboxTable = "policy_outbox"

// Decision values (ADR-0054 §1). deny is not produced at v0.2.0 — there are no
// deny rules until the v0.3.0 rule engine; the operator's resolve produces the
// granted/denied outcome.
const (
	DecisionAllow            = "allow"
	DecisionRequiresApproval = "requires_approval"
)

// timeoutActor is the workload identity attributed to fail-closed auto-denials —
// a timeout is a system action, never silent (cf. connector's system:cast-gc).
const timeoutActor = "system:policy-timeout"

// ErrNotPending is returned when a resolve (or read) finds no still-pending
// approval for the session — already resolved, timed out, or never created.
var ErrNotPending = errors.New("no pending approval for session (already resolved or absent)")

const pendingCols = `id, session_id, subject, COALESCE(resolved_by,''), edge, target, action, status, reason, created_at, expires_at, resolved_at`

// PendingApproval is a pending_approvals row.
type PendingApproval struct {
	ID         string
	SessionID  string
	Subject    string
	ResolvedBy string
	Edge       string
	Target     string
	Action     string
	Status     string
	Reason     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	ResolvedAt *time.Time
}

// CreatePendingInput is the decide RPC's create payload.
type CreatePendingInput struct {
	SessionID string
	Subject   string
	Edge      string
	Target    string
	Action    string
}

// Store wraps the pool and the shared outbox Writer (targeting policy_outbox).
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: outboxTable}}
}

// CreatePending parks a requires_approval session-start: it inserts a pending row
// (expiring at now+ttl) and enqueues policy.decision:requires_approval in one
// transaction. Idempotent on session_id — a duplicate decide returns the existing
// pending approval with NO second decision event (safe for api retries).
func (s *Store) CreatePending(ctx context.Context, in CreatePendingInput, ttl time.Duration) (PendingApproval, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return PendingApproval{}, fmt.Errorf("mint id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PendingApproval{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var pa PendingApproval
	err = scanPending(tx.QueryRow(ctx, `
		INSERT INTO pending_approvals (id, session_id, subject, edge, target, action, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7)
		ON CONFLICT (session_id) DO NOTHING
		RETURNING `+pendingCols,
		id.String(), in.SessionID, in.Subject, in.Edge, in.Target, in.Action, time.Now().Add(ttl)), &pa)
	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict — a pending approval already exists for this session. Return it
		// without a second decision event.
		_ = tx.Rollback(ctx)
		return s.Get(ctx, in.SessionID)
	}
	if err != nil {
		return PendingApproval{}, fmt.Errorf("insert pending: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.Subject, Edge: in.Edge,
		Target: in.Target, Outcome: "policy.decision:requires_approval",
		EvidencePacketID: "session:" + in.SessionID,
	}); err != nil {
		return PendingApproval{}, fmt.Errorf("enqueue decision: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PendingApproval{}, fmt.Errorf("commit decision: %w", err)
	}
	return pa, nil
}

// Resolve records the operator's approve/deny on a still-pending approval and
// enqueues approval.granted / approval.denied:operator in the same transaction.
// ErrNotPending if the session has no pending approval (already resolved or timed
// out) — so a double-resolve or resolving an expired approval cannot flip it.
func (s *Store) Resolve(ctx context.Context, sessionID, decision, resolvedBy string) (PendingApproval, error) {
	if decision != "granted" && decision != "denied" {
		return PendingApproval{}, fmt.Errorf("invalid decision %q", decision)
	}
	outcome := "approval.granted"
	if decision == "denied" {
		outcome = "approval.denied:operator"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PendingApproval{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pa PendingApproval
	err = scanPending(tx.QueryRow(ctx, `
		UPDATE pending_approvals
		   SET status = $2, resolved_by = $3, reason = 'operator', resolved_at = now()
		 WHERE session_id = $1 AND status = 'pending'
		RETURNING `+pendingCols, sessionID, decision, resolvedBy), &pa)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingApproval{}, ErrNotPending
	}
	if err != nil {
		return PendingApproval{}, fmt.Errorf("resolve pending: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: resolvedBy, Edge: pa.Edge,
		Target: pa.Target, Outcome: outcome, EvidencePacketID: "session:" + sessionID,
	}); err != nil {
		return PendingApproval{}, fmt.Errorf("enqueue approval: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return PendingApproval{}, fmt.Errorf("commit approval: %w", err)
	}
	return pa, nil
}

// ExpireTimedOut fail-closes every pending approval whose expiry has passed: it
// flips them to denied/timeout and enqueues one approval.denied:timeout event per
// row, all in one transaction. Returns the number expired. Idempotent — already
// resolved rows are not matched. Run by the serve-time sweeper.
func (s *Store) ExpireTimedOut(ctx context.Context, now time.Time) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		UPDATE pending_approvals
		   SET status = 'denied', reason = 'timeout', resolved_by = $1, resolved_at = now()
		 WHERE status = 'pending' AND expires_at < $2
		RETURNING session_id, edge, target`, timeoutActor, now)
	if err != nil {
		return 0, fmt.Errorf("expire query: %w", err)
	}
	type expired struct{ sessionID, edge, target string }
	var batch []expired
	for rows.Next() {
		var e expired
		if err := rows.Scan(&e.sessionID, &e.edge, &e.target); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired: %w", err)
		}
		batch = append(batch, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	// Rows are fully drained + closed before any further query on the same tx.
	for _, e := range batch {
		if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
			Timestamp: time.Now(), Actor: timeoutActor, Edge: e.edge,
			Target: e.target, Outcome: "approval.denied:timeout",
			EvidencePacketID: "session:" + e.sessionID,
		}); err != nil {
			return 0, fmt.Errorf("enqueue timeout: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit expire: %w", err)
	}
	return len(batch), nil
}

// Get returns the approval for a session, or ErrNotPending if none exists.
func (s *Store) Get(ctx context.Context, sessionID string) (PendingApproval, error) {
	var pa PendingApproval
	err := scanPending(s.pool.QueryRow(ctx,
		`SELECT `+pendingCols+` FROM pending_approvals WHERE session_id = $1`, sessionID), &pa)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingApproval{}, ErrNotPending
	}
	if err != nil {
		return PendingApproval{}, fmt.Errorf("get pending: %w", err)
	}
	return pa, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanPending(row scanner, pa *PendingApproval) error {
	return row.Scan(&pa.ID, &pa.SessionID, &pa.Subject, &pa.ResolvedBy, &pa.Edge,
		&pa.Target, &pa.Action, &pa.Status, &pa.Reason, &pa.CreatedAt, &pa.ExpiresAt, &pa.ResolvedAt)
}
