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
// order matches). agent_visible (T-087, migration 0013) is appended at the end so
// every existing Scan call site keeps its original column positions.
const selectCols = `id, scope_type, scope_id, key, ref, content, source_trace,
	confidence, pinned, expires_at, redacted_at, permission_scope, created_at, updated_at,
	agent_visible`

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

// open is nil-safe (see CommandSealer): a nil sealer is the plaintext fallback.
func (s *Store) open(b []byte) (string, error) {
	if s.sealer == nil {
		return string(b), nil
	}
	return s.sealer.Open(b)
}

// seal is open's symmetric counterpart (the workstream/run store's shape): a nil
// sealer is the plaintext dev/transition fallback, so authored content round-trips
// even before the command-at-rest key is provisioned.
func (s *Store) seal(plaintext string) ([]byte, error) {
	if s.sealer == nil {
		return []byte(plaintext), nil
	}
	return s.sealer.Seal(plaintext)
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

// CreateAuthored writes ONE operator-authored memory (ADR-0064 §2 secondary path;
// T-080, POST /v1/memories), its OWN transaction (no terminal flip to ride, unlike
// AccrueRunTerminal). Content is sealed at rest via seal (F3 parity); the row has no
// run anchor, so ref stays "" (authored rows are named by key, not a pointer).
// memory.written enqueues in the SAME transaction — content-free (ADR-0049): actor =
// the authoring operator (in.Owner), edge human-agent, target memory:<id>.
// EvidencePacketID carries the scope anchor ("<scopeType>:<scopeId>", the
// house-consistent non-empty-pointer choice AccrueRunTerminal also makes with
// "run:<id>") rather than "" — an authored note's evidence trail is the scope it was
// written against.
func (s *Store) CreateAuthored(ctx context.Context, in CreateInput) (Memory, error) {
	if in.Owner == "" || in.ScopeType == "" || in.ScopeID == "" || in.Content == "" {
		return Memory{}, errors.New("memory requires an owner, scope, and content")
	}
	id := uuid.Must(uuid.NewV7())
	sealed, err := s.seal(in.Content)
	if err != nil {
		return Memory{}, fmt.Errorf("seal memory content: %w", err)
	}
	confidence := float32(1.0)
	if in.Confidence != nil {
		confidence = *in.Confidence
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Memory{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	row := tx.QueryRow(ctx, `
		INSERT INTO memories (id, scope_type, scope_id, key, content, source_trace, confidence, expires_at, permission_scope)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING `+selectCols,
		id.String(), in.ScopeType, in.ScopeID, in.Key, sealed, in.SourceTrace, confidence, in.ExpiresAt, in.Owner)
	m, err := s.scan(row)
	if err != nil {
		return Memory{}, fmt.Errorf("insert authored memory: %w", err)
	}

	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.Owner, Edge: memoryEdge,
		Target: "memory:" + id.String(), Outcome: "memory.written",
		EvidencePacketID: in.ScopeType + ":" + in.ScopeID,
	}); err != nil {
		return Memory{}, fmt.Errorf("enqueue memory.written: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Memory{}, fmt.Errorf("commit: %w", err)
	}
	return m, nil
}

// defaultListLimit/maxListLimit bound List's pushed-down limit (PERF-9/D3): default
// 50 when the caller passes 0, clamped to [1, 200] otherwise — the same bounds the
// workstream/grant/run list routes enforce at the handler; here it lives in the store
// so every caller (the route, tests, future callers) gets the clamp for free.
const (
	defaultListLimit = 50
	maxListLimit     = 200
)

// List returns owner-scoped memories for one scope in id (≈ creation) order, bounded
// to limit rows (clamped to [1, maxListLimit]; 0 or an out-of-range value defaults to
// defaultListLimit — D3's pushed-down limit). Non-owner rows never appear — the
// caller's owner param IS the filter (no separate authorization check needed; a
// foreign scope simply returns no rows).
func (s *Store) List(ctx context.Context, owner, scopeType, scopeID string, limit int) ([]Memory, error) {
	switch {
	case limit <= 0:
		limit = defaultListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+selectCols+` FROM memories
		 WHERE permission_scope = $1 AND scope_type = $2 AND scope_id = $3
		 ORDER BY id ASC
		 LIMIT $4`, owner, scopeType, scopeID, limit)
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

// flip runs ONE guarded flip in its own tx (T-081, ADR-0064 §3): exec applies an
// UPDATE whose WHERE clause already carries the once-only guard (e.g.
// "redacted_at IS NULL"), so a no-op flip (the row is already in the desired end
// state, or the id/owner does not match) reports 0 rows affected. When exactly one
// row flips, outcome enqueues content-free in the SAME tx (the flipWithEvent
// precedent — event-only-when-flipped). Either way the current owner-scoped row is
// re-fetched INSIDE the tx and returned — ErrNotFound when the id does not exist or
// belongs to a different owner, so a genuine no-op (row exists, already in the
// target state) still answers 200 with the current view (D1: idempotent, not 409).
func (s *Store) flip(ctx context.Context, owner, id string, exec func(pgx.Tx) (int64, error), outcome string) (Memory, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Memory{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	n, err := exec(tx)
	if err != nil {
		return Memory{}, fmt.Errorf("%s: %w", outcome, err)
	}
	if n == 1 {
		if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
			Timestamp: time.Now(), Actor: owner, Edge: memoryEdge,
			Target: "memory:" + id, Outcome: outcome,
			EvidencePacketID: "memory:" + id,
		}); err != nil {
			return Memory{}, fmt.Errorf("enqueue %s: %w", outcome, err)
		}
	}

	row := tx.QueryRow(ctx, `SELECT `+selectCols+` FROM memories WHERE id = $1 AND permission_scope = $2`, id, owner)
	m, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Memory{}, ErrNotFound
	}
	if err != nil {
		return Memory{}, fmt.Errorf("refetch after %s: %w", outcome, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Memory{}, fmt.Errorf("commit %s: %w", outcome, err)
	}
	return m, nil
}

// Redact tombstones one owner-scoped memory (ADR-0064 §3/MEM-3): content is erased,
// redacted_at flips once, chained as memory.redacted (content-free) — the pointer
// skeleton remains for provenance. Idempotent: a re-redact is a silent no-op (no
// second event) that still returns the current (already-tombstoned) view.
// ErrNotFound when id does not exist or belongs to a different owner (SEC-31/N3).
func (s *Store) Redact(ctx context.Context, owner, id string) (Memory, error) {
	return s.flip(ctx, owner, id, func(tx pgx.Tx) (int64, error) {
		ct, err := tx.Exec(ctx, `
			UPDATE memories SET redacted_at = now(), content = NULL, updated_at = now()
			 WHERE id = $1 AND permission_scope = $2 AND redacted_at IS NULL`, id, owner)
		if err != nil {
			return 0, err
		}
		return ct.RowsAffected(), nil
	}, "memory.redacted")
}

// SetPinned flips one owner-scoped memory's pinned flag (ADR-0064 §3/MEM-3): pinned
// rows are exempt from the expiry sweep (ExpireDue). Idempotent per direction: pinning
// an already-pinned row (or unpinning an already-unpinned row) is a silent no-op — no
// duplicate memory.pinned/memory.unpinned. ErrNotFound when id does not exist or
// belongs to a different owner (SEC-31/N3).
func (s *Store) SetPinned(ctx context.Context, owner, id string, pinned bool) (Memory, error) {
	outcome := "memory.pinned"
	if !pinned {
		outcome = "memory.unpinned"
	}
	return s.flip(ctx, owner, id, func(tx pgx.Tx) (int64, error) {
		ct, err := tx.Exec(ctx, `
			UPDATE memories SET pinned = $3, updated_at = now()
			 WHERE id = $1 AND permission_scope = $2 AND pinned != $3`, id, owner, pinned)
		if err != nil {
			return 0, err
		}
		return ct.RowsAffected(), nil
	}, outcome)
}

// SetShared flips one owner-scoped memory's agent_visible flag (MEM-6, ADR-0065 §3;
// R3, the ratified pin/unpin precedent applied to sharing): default-invisible
// operator-authored content becomes readable by the agent plane (ReadForAgent) only
// once shared. Idempotent per direction, same as SetPinned — a re-share/re-unshare is
// a silent no-op (no duplicate memory.shared/memory.unshared). The flip predicate ALSO
// requires redacted_at IS NULL (D1): a tombstoned row can never be (re)shared or
// (re)unshared by the flip itself — but the caller (ShareMemory/UnshareMemory,
// memories.go) checks RedactedAt on the Get-then-flip row BEFORE calling this, so a
// tombstoned target answers 409 rather than a silently-absorbed no-op 200 (the D1
// distinction: idempotent-200 for "already in the desired state," 409 for "can never
// reach it"). ErrNotFound when id does not exist or belongs to a different owner
// (SEC-31/N3).
func (s *Store) SetShared(ctx context.Context, owner, id string, shared bool) (Memory, error) {
	outcome := "memory.shared"
	if !shared {
		outcome = "memory.unshared"
	}
	return s.flip(ctx, owner, id, func(tx pgx.Tx) (int64, error) {
		ct, err := tx.Exec(ctx, `
			UPDATE memories SET agent_visible = $3, updated_at = now()
			 WHERE id = $1 AND permission_scope = $2 AND agent_visible != $3 AND redacted_at IS NULL`, id, owner, shared)
		if err != nil {
			return 0, err
		}
		return ct.RowsAffected(), nil
	}, outcome)
}

// ExpireDue tombstones up to batch unpinned, not-yet-redacted rows whose expires_at
// has passed (ADR-0064 §3/DUR-9, F-b — rides the api's existing reaper loop, T-081).
// ONE tx: the SELECT ... FOR UPDATE SKIP LOCKED sub-query claims a bounded batch
// (PERF-9(3): capped to 100) so concurrent sweeps (a slow tick overlapping the next)
// never double-claim a row; the guarded predicate (redacted_at IS NULL AND NOT
// pinned) is the exactly-once guard — a re-run touches nothing already tombstoned or
// pinned, so expiry is exactly-once BY THE FLIP, not the timer (DUR-9). One
// memory.expired chains per expired row in the SAME tx, content-free, actor = the
// row's own owning operator (permission_scope) — the sweep has no operator caller of
// its own. Returns the count of rows expired this pass.
func (s *Store) ExpireDue(ctx context.Context, batch int) (int, error) {
	if batch <= 0 || batch > 100 {
		batch = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	rows, err := tx.Query(ctx, `
		UPDATE memories SET redacted_at = now(), content = NULL, updated_at = now()
		 WHERE id IN (
		     SELECT id FROM memories
		      WHERE expires_at IS NOT NULL AND expires_at <= now()
		        AND redacted_at IS NULL AND NOT pinned
		      ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED)
		 RETURNING id, permission_scope`, batch)
	if err != nil {
		return 0, fmt.Errorf("expire due: %w", err)
	}
	type expiredRow struct{ id, owner string }
	var due []expiredRow
	for rows.Next() {
		var e expiredRow
		if err := rows.Scan(&e.id, &e.owner); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan expired row: %w", err)
		}
		due = append(due, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("expire due rows: %w", err)
	}

	for _, e := range due {
		if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
			Timestamp: time.Now(), Actor: e.owner, Edge: memoryEdge,
			Target: "memory:" + e.id, Outcome: "memory.expired",
			EvidencePacketID: "memory:" + e.id,
		}); err != nil {
			return 0, fmt.Errorf("enqueue memory.expired: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit expire due: %w", err)
	}
	return len(due), nil
}

// agentMemoryEdge is the ONE lit agent-memory path (SEC-32, ADR-0065 §2/§3, T-090):
// the sole edge value memory.read's event carries — distinct from memoryEdge
// (human-agent) above, which every operator-plane memory verb still rides. Lighting
// this is deliberate enforcement of the reserved seventh edge (ADR-0046), not an
// eighth edge invented.
const agentMemoryEdge = "agent-memory"

// ReadForAgent is the agent-plane read (MEM-5, SEC-32, ADR-0065 §3, T-090's
// memory.read forward) — a SEPARATE, grant-scoped, visibility-filtered read; List/Get
// above (owner-keyed) are BYTE-UNTOUCHED (WD-8 proves it). The readable set is derived
// ENTIRELY server-side from the caller's own ACTIVE, unexpired grants (grant.agent ==
// agentSubject — never a caller-supplied scope, SEC-32 containment BY CONSTRUCTION):
// the union of (a) the grant's own charter (a workstream-scoped row, reached via
// workstreams.grant_id) and (b) that grant's own Runs (a run-scoped row, via
// runs.grant_id). workstreamID narrows the union to the ONE charter it names; a value
// outside the caller's own derived set (unknown, foreign, or a charter this agent does
// not compose for) matches nothing in the CTE below and answers an EMPTY slice — never
// an error (no oracle, N1). Rows are filtered to redacted_at IS NULL (R2: a
// tombstoned row is gone from the agent-feeding surface even if it would otherwise be
// in scope) AND (content IS NULL — an accrued reference row, always readable) OR
// (content IS NOT NULL AND agent_visible — an authored row, readable only once shared,
// R3/N2). Clamped id-ASC (C5, the v0.8 List clamp reused verbatim).
//
// R4/fail-loud: chains EXACTLY ONE memory.read event PER CALL, enqueued INSIDE the
// SAME transaction as the read itself, committed only once the enqueue succeeds — if
// the enqueue fails, the whole call fails (no event, no data reaches the caller). The
// event is content-free: Target lists the returned row ids (the "memory:<id>"
// single-row convention extended to the plural read; "memory:" with no ids when the
// result is empty), Actor is the reading agent, Edge is agent-memory (SEC-32),
// EvidencePacketID is the workstreamId filter's scope anchor when one was given (the
// OpenPlanRequest scope-anchor convention), else "" (an unfiltered read of the
// caller's WHOLE readable set carries no single scope to anchor to).
func (s *Store) ReadForAgent(ctx context.Context, agentSubject, workstreamID string, limit int) ([]Memory, error) {
	switch {
	case limit <= 0:
		limit = defaultListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	rows, err := tx.Query(ctx, `
		WITH live_grants AS (
			SELECT id FROM delegation_grants
			 WHERE agent = $1 AND status = 'active' AND expires_at > now()
		),
		scoped_grants AS (
			SELECT g.id FROM live_grants g
			 WHERE $2 = '' OR g.id IN (SELECT grant_id FROM workstreams WHERE id::text = $2)
		),
		ws_scope AS (
			SELECT id::text AS scope_id FROM workstreams WHERE grant_id IN (SELECT id FROM scoped_grants)
		),
		run_scope AS (
			SELECT id::text AS scope_id FROM runs WHERE grant_id IN (SELECT id FROM scoped_grants)
		)
		SELECT `+selectCols+` FROM memories m
		 WHERE redacted_at IS NULL
		   AND (content IS NULL OR agent_visible)
		   AND (
		       (scope_type = 'workstream' AND scope_id IN (SELECT scope_id FROM ws_scope))
		    OR (scope_type = 'run' AND scope_id IN (SELECT scope_id FROM run_scope))
		   )
		 ORDER BY id ASC
		 LIMIT $3`, agentSubject, workstreamID, limit)
	if err != nil {
		return nil, fmt.Errorf("read for agent: %w", err)
	}
	var out []Memory
	var ids []string
	for rows.Next() {
		m, serr := s.scan(rows)
		if serr != nil {
			rows.Close()
			return nil, serr
		}
		out = append(out, m)
		ids = append(ids, m.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read for agent rows: %w", err)
	}

	evidence := ""
	if workstreamID != "" {
		evidence = "workstream:" + workstreamID
	}
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: agentSubject, Edge: agentMemoryEdge,
		Target: "memory:" + strings.Join(ids, ","), Outcome: "memory.read",
		EvidencePacketID: evidence,
	}); err != nil {
		return nil, fmt.Errorf("enqueue memory.read: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit read for agent: %w", err)
	}
	return out, nil
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
		&m.PermissionScope, &m.CreatedAt, &m.UpdatedAt, &m.AgentVisible); err != nil {
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
