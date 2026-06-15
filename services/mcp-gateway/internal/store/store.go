// Package store is the mcp-gateway's Postgres access layer (ADR-0057). It records
// each agent tool-call as an `invocations` row and — in the SAME transaction —
// enqueues the tool.invoked / tool.result evidence into the gateway outbox
// (ADR-0045), which the relay drains into the audit chain. Idempotency (a
// client-supplied key dedups retries, PERF-2) and the per-agent in-flight count
// (the concurrency cap, PERF-3) live here; the governed orchestration that calls
// these methods is the catalog Service (T-039b).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

const (
	// OutboxTable / OutboxChannel are the gateway's own outbox names (ADR-0048
	// shared DB; avoid collision with the other producers' tables).
	OutboxTable   = "gateway_outbox"
	OutboxChannel = "gateway_outbox_new"

	// auditEdge is the agent→gateway edge for tool.invoked / tool.result (ADR-0046).
	auditEdge = "agent-tool"
)

// ErrNotFound is returned when an invocation id / idempotency key is absent.
var ErrNotFound = errors.New("invocation not found")

// Invocation is the gateway's view of an invocations row. Command is the persisted
// approval payload (ADR-0059) — operator-only, never returned on the agent plane.
type Invocation struct {
	ID             string
	IdempotencyKey string
	AgentSubject   string
	ResourceID     string
	Tool           string
	Command        string
	Status         string
	Decision       string
}

// CreateInput is a new invocation request. IdempotencyKey "" disables dedup.
type CreateInput struct {
	AgentSubject   string
	ResourceID     string
	Tool           string
	Command        string
	IdempotencyKey string
}

// Store wraps the gateway's pgx pool and the shared outbox Writer.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: OutboxTable}}
}

// Create records a new invocation (status pending_approval) and enqueues
// tool.invoked in the same transaction (AUD-9). With a non-empty IdempotencyKey, a
// retry for the same (agent, key) reconciles to the original invocation
// (created=false, no second tool.invoked) — PERF-2. A keyless call always creates.
func (s *Store) Create(ctx context.Context, in CreateInput) (inv Invocation, created bool, err error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Invocation{}, false, fmt.Errorf("mint invocation id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Invocation{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var keyArg any // NULL when no idempotency key (the partial index excludes NULLs)
	if in.IdempotencyKey != "" {
		keyArg = in.IdempotencyKey
	}
	ct, err := tx.Exec(ctx, `
		INSERT INTO invocations (id, idempotency_key, agent_subject, resource_id, tool, command, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending_approval')
		ON CONFLICT (agent_subject, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`,
		id.String(), keyArg, in.AgentSubject, in.ResourceID, in.Tool, in.Command)
	if err != nil {
		return Invocation{}, false, fmt.Errorf("insert invocation: %w", err)
	}

	if ct.RowsAffected() == 0 {
		// Idempotent hit: return the existing invocation, no new tool.invoked.
		existing, ferr := getByKey(ctx, tx, in.AgentSubject, in.IdempotencyKey)
		if ferr != nil {
			return Invocation{}, false, ferr
		}
		if cerr := tx.Commit(ctx); cerr != nil {
			return Invocation{}, false, cerr
		}
		return existing, false, nil
	}

	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.AgentSubject, Edge: auditEdge,
		Target: "resource:" + in.ResourceID, Outcome: "tool.invoked",
		EvidencePacketID: "invocation:" + id.String(),
	}); err != nil {
		return Invocation{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Invocation{}, false, fmt.Errorf("commit create: %w", err)
	}
	return Invocation{
		ID: id.String(), IdempotencyKey: in.IdempotencyKey, AgentSubject: in.AgentSubject,
		ResourceID: in.ResourceID, Tool: in.Tool, Command: in.Command, Status: "pending_approval",
	}, true, nil
}

func getByKey(ctx context.Context, tx pgx.Tx, agentSubject, key string) (Invocation, error) {
	var inv Invocation
	err := tx.QueryRow(ctx, `
		SELECT id, COALESCE(idempotency_key, ''), agent_subject, resource_id, tool, command, status, decision
		  FROM invocations WHERE agent_subject = $1 AND idempotency_key = $2`,
		agentSubject, key).Scan(&inv.ID, &inv.IdempotencyKey, &inv.AgentSubject,
		&inv.ResourceID, &inv.Tool, &inv.Command, &inv.Status, &inv.Decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrNotFound
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("load invocation by key: %w", err)
	}
	return inv, nil
}

// Get loads an invocation by id — the block-and-wait poll (the orchestrator) and the
// operator pending-list read (T-040b) both use it. ErrNotFound for an unknown id.
func (s *Store) Get(ctx context.Context, id string) (Invocation, error) {
	var inv Invocation
	err := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(idempotency_key, ''), agent_subject, resource_id, tool, command, status, decision
		  FROM invocations WHERE id = $1`, id).Scan(&inv.ID, &inv.IdempotencyKey, &inv.AgentSubject,
		&inv.ResourceID, &inv.Tool, &inv.Command, &inv.Status, &inv.Decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, ErrNotFound
	}
	if err != nil {
		return Invocation{}, fmt.Errorf("get invocation: %w", err)
	}
	return inv, nil
}

// ClaimForExec atomically moves a GRANTED (decision=allow) parked invocation to
// running and returns it — the single-winner gate that prevents a double-exec when
// more than one waiter races a re-attach (PERF-2). claimed=false means the row was
// not (yet) grantable, or another caller already claimed it; the caller keeps
// polling. The returned Command is the persisted approval payload (ADR-0059) — the
// exec runs what the operator approved.
func (s *Store) ClaimForExec(ctx context.Context, id string) (Invocation, bool, error) {
	var inv Invocation
	err := s.pool.QueryRow(ctx, `
		UPDATE invocations SET status = 'running', updated_at = now()
		 WHERE id = $1 AND status = 'pending_approval' AND decision = 'allow'
		 RETURNING id, COALESCE(idempotency_key, ''), agent_subject, resource_id, tool, command, status, decision`,
		id).Scan(&inv.ID, &inv.IdempotencyKey, &inv.AgentSubject, &inv.ResourceID,
		&inv.Tool, &inv.Command, &inv.Status, &inv.Decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return Invocation{}, false, nil
	}
	if err != nil {
		return Invocation{}, false, fmt.Errorf("claim for exec: %w", err)
	}
	return inv, true, nil
}

// ReapStale finalizes parked invocations whose approval window has elapsed (ADR-0059):
// a never-resolved pending_approval row older than olderThan is moved to failed —
// freeing the PERF-3 concurrency slot and bounding command retention — and emits
// tool.result:error in the same transaction. It is the backstop for an agent that
// disconnected mid-wait; a connected agent's own block-and-wait deadline finalizes
// first. Returns the number reaped.
func (s *Store) ReapStale(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().Add(-olderThan)
	rows, err := s.pool.Query(ctx, `
		SELECT id FROM invocations WHERE status = 'pending_approval' AND updated_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("scan stale invocations: %w", err)
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
		reaped, err := s.reapOne(ctx, id, cutoff)
		if err != nil {
			return n, err
		}
		if reaped {
			n++
		}
	}
	return n, nil
}

// reapOne finalizes one stale parked invocation. The status+cutoff guard means a row
// resolved or claimed between the scan and here is skipped (returns false), so the
// reaper never clobbers an in-flight grant.
func (s *Store) reapOne(ctx context.Context, id string, cutoff time.Time) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var agent, resource string
	err = tx.QueryRow(ctx, `
		UPDATE invocations SET status = 'failed', updated_at = now()
		 WHERE id = $1 AND status = 'pending_approval' AND updated_at < $2
		 RETURNING agent_subject, resource_id`, id, cutoff).Scan(&agent, &resource)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reap invocation: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: agent, Edge: auditEdge,
		Target: "resource:" + resource, Outcome: "tool.result:error",
		EvidencePacketID: "invocation:" + id,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit reap: %w", err)
	}
	return true, nil
}

// RequiresApproval reads the resource's requires_approval label (ADR-0057) — the
// same resources.labels read the broker does for SSH coords (no cross-service
// call; resources live in the shared cockpit DB). ErrNotFound for an unknown id.
func (s *Store) RequiresApproval(ctx context.Context, resourceID string) (bool, error) {
	var labelsRaw []byte
	err := s.pool.QueryRow(ctx, `SELECT labels FROM resources WHERE id = $1`, resourceID).Scan(&labelsRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("read resource labels: %w", err)
	}
	labels := map[string]string{}
	if len(labelsRaw) > 0 {
		if err := json.Unmarshal(labelsRaw, &labels); err != nil {
			return false, fmt.Errorf("unmarshal labels: %w", err)
		}
	}
	return labels["requires_approval"] == "true", nil
}

// CountInFlight returns the number of not-yet-terminal invocations for an agent —
// the per-agent concurrency cap's input (PERF-3).
func (s *Store) CountInFlight(ctx context.Context, agentSubject string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM invocations
		 WHERE agent_subject = $1 AND status IN ('pending_approval', 'running')`, agentSubject).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count in-flight: %w", err)
	}
	return n, nil
}

// SetStatus transitions an invocation to a non-terminal state (pending_approval →
// running, etc.) and optionally records the policy decision. No audit event — the
// decision/approval events come from policy-service; tool.invoked/result bracket
// the call (Create / Finish).
func (s *Store) SetStatus(ctx context.Context, id, status, decision string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE invocations
		   SET status = $2, decision = COALESCE(NULLIF($3, ''), decision), updated_at = now()
		 WHERE id = $1`, id, status, decision)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Finish moves an invocation to a terminal status (completed / failed / denied) and
// enqueues tool.result in the same transaction (AUD-9). outcome is "tool.result"
// (success) or "tool.result:error"; the event anchors invocation:<id>.
func (s *Store) Finish(ctx context.Context, id, status, outcome string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var agent, resource string
	err = tx.QueryRow(ctx, `
		UPDATE invocations SET status = $2, updated_at = now()
		 WHERE id = $1 RETURNING agent_subject, resource_id`, id, status).Scan(&agent, &resource)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("finish invocation: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: agent, Edge: auditEdge,
		Target: "resource:" + resource, Outcome: outcome,
		EvidencePacketID: "invocation:" + id,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finish: %w", err)
	}
	return nil
}
