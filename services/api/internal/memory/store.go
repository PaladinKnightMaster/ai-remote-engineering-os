package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// ErrNotFound is returned when no memory has the given (owner-scoped) id.
var ErrNotFound = errors.New("memory not found")

// memoryEdge is the bracket edge for every memory verb (ADR-0064 §4): memory is
// operator-plane only — write, read, redact, pin all ride human-agent (operator
// cookie + CSRF), exactly like every prior operator surface. No agent path exists.
const memoryEdge = "human-agent"

// CommandSealer seals/opens operator-authored memory content at rest — the same
// contract as the Run and Workstream stores' (F3 parity); a nil sealer is the
// plaintext dev/transition fallback, matching their current wiring so all three flip
// together when the command-at-rest key is provisioned.
type CommandSealer interface {
	Seal(plaintext string) ([]byte, error)
	Open(b []byte) (string, error)
}

// selectCols is the shared column list for Get/List (kept in one place so the scan
// order matches).
const selectCols = `id, scope_type, scope_id, key, ref, content, source_trace,
	confidence, pinned, expires_at, redacted_at, permission_scope, created_at, updated_at`

// Store persists memories. It takes the pgx pool directly (the internal/run /
// internal/workstream pattern) so the package stays extraction-ready.
type Store struct {
	pool   *pgxpool.Pool
	sealer CommandSealer
	writer outbox.Writer
}

// NewStore builds the memory store. sealer may be nil (plaintext fallback — see
// CommandSealer). outboxTable is the api outbox table (e.g. "api_outbox") into which
// AccrueRunTerminal enqueues memory.written in the caller's transaction.
func NewStore(pool *pgxpool.Pool, sealer CommandSealer, outboxTable string) *Store {
	return &Store{pool: pool, sealer: sealer, writer: outbox.Writer{Table: outboxTable}}
}

// open is nil-safe (see CommandSealer): a nil sealer is the plaintext fallback. There
// is no seal counterpart yet — no authored-write path exists until T-080 adds
// POST /v1/memories; content only ever arrives via a direct row (tests) until then.
func (s *Store) open(b []byte) (string, error) {
	if s.sealer == nil {
		return string(b), nil
	}
	return s.sealer.Open(b)
}

// eventActor picks a memory event's actor from (operator, actor): the accruing Run's
// agent/workstream actor when set, else the operator — mirroring internal/run's
// eventActor (ADR-0064 §8: "actor = the accruing Run's actor ... for accrual").
func eventActor(operator, actor string) string {
	if actor != "" {
		return actor
	}
	return operator
}

// scopeForActor derives an accrued memory's scope from the terminal Run's actor
// (ADR-0064 §1/MEM-2): a workstream-fired Run (actor = "workstream:<id>") scopes to
// its charter; every other Run (agent actor, or "" for an operator-direct Run) scopes
// to itself.
func scopeForActor(actor, runID string) (scopeType, scopeID string) {
	if id, ok := strings.CutPrefix(actor, "workstream:"); ok {
		return ScopeWorkstream, id
	}
	return ScopeRun, runID
}

// AccrueRunTerminal writes ONE content-free memory pointer for a Run that just
// reached a terminal status, in the SAME transaction as the Run's terminal event
// (ADR-0064 §2/MEM-2 — the reference-based accrual core; F-a: the shared once-only
// terminal-flip seam, so a no-op flip never double-accrues and the flip + the memory
// commit or roll back together). ref/source_trace carry only correlation ids — never
// command text or stdout (content stays NULL). Scope: workstream when the Run was
// fired by a charter (actor = "workstream:<id>"), else the Run itself. The owning
// operator is permission_scope. grantID is accepted for interface parity with the
// terminal-flip seam but not yet persisted — MEM-1's schema carries no grant column at
// v0.8 (recorded, not a gap: no consuming read exists yet).
func (s *Store) AccrueRunTerminal(ctx context.Context, tx pgx.Tx, runID, status, operator, actor, grantID string) error {
	_ = grantID
	id := uuid.Must(uuid.NewV7())

	scopeType, scopeID := scopeForActor(actor, runID)
	ref := "run:" + runID
	sourceTrace := ref + ":" + status

	if _, err := tx.Exec(ctx, `
		INSERT INTO memories (id, scope_type, scope_id, ref, source_trace, permission_scope)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id.String(), scopeType, scopeID, ref, sourceTrace, operator); err != nil {
		return fmt.Errorf("insert accrued memory: %w", err)
	}

	// memory.written in the SAME transaction — content-free (ADR-0049): the fact, the
	// scope, the actor, target memory:<id>; never content, never command text.
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: eventActor(operator, actor), Edge: memoryEdge,
		Target: "memory:" + id.String(), Outcome: "memory.written",
		EvidencePacketID: ref,
	}); err != nil {
		return fmt.Errorf("enqueue memory.written: %w", err)
	}
	return nil
}

// List returns owner-scoped memories for one scope in id (≈ creation) order.
// Non-owner rows never appear — the caller's owner param IS the filter (no separate
// authorization check needed; a foreign scope simply returns no rows).
func (s *Store) List(ctx context.Context, owner, scopeType, scopeID string) ([]Memory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+selectCols+` FROM memories
		 WHERE permission_scope = $1 AND scope_type = $2 AND scope_id = $3
		 ORDER BY id ASC`, owner, scopeType, scopeID)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	var out []Memory
	for rows.Next() {
		m, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns one owner-scoped memory, or ErrNotFound (including when the id exists
// but belongs to a different operator — the owner-404 house pattern, SEC-31/N3).
func (s *Store) Get(ctx context.Context, owner, id string) (Memory, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+selectCols+` FROM memories WHERE id = $1 AND permission_scope = $2`, id, owner)
	m, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, fmt.Errorf("get memory: %w", err)
	}
	return m, nil
}

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query, via
// rows.Next()+scan), so Get and List share one scan body.
type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scan(row rowScanner) (Memory, error) {
	var m Memory
	var content []byte
	if err := row.Scan(&m.ID, &m.ScopeType, &m.ScopeID, &m.Key, &m.Ref, &content,
		&m.SourceTrace, &m.Confidence, &m.Pinned, &m.ExpiresAt, &m.RedactedAt,
		&m.PermissionScope, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return Memory{}, err
	}
	if content != nil {
		opened, err := s.open(content)
		if err != nil {
			return Memory{}, fmt.Errorf("open memory content: %w", err)
		}
		m.Content = opened
		m.HasContent = true
	}
	return m, nil
}
