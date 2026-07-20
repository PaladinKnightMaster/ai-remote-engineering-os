//go:build integration

// Integration tests for the memory store against a real Postgres (T-079 G1).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/memory/...
package memory

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/run"
	"aireos.local/services/api/internal/workstream"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("API_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool, nil, "api_outbox"), pool
}

// freshID mints a fresh UUID v7 per test run (the T-073 lesson: fixed ids are not
// re-runnable against a dirty DB).
func freshID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint id: %v", err)
	}
	return id.String()
}

// TestAccrueRunTerminal — the T-079 G1 live-PG proof of the accrual core (MEM-2): each
// of the three terminal outcomes writes exactly ONE content-free row in the SAME
// transaction as its memory.written event; the row carries no content and a
// run:<id>:<status> source_trace; a rolled-back tx accrues nothing (atomicity).
func TestAccrueRunTerminal(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	for _, status := range []string{"completed", "failed", "canceled"} {
		t.Run(status, func(t *testing.T) {
			runID := freshID(t)
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			if err := s.AccrueRunTerminal(ctx, tx, runID, status, "operator:alice", "", ""); err != nil {
				t.Fatalf("accrue: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}

			rows, err := s.List(ctx, "operator:alice", ScopeRun, runID, 0)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("accrued rows = %d, want 1", len(rows))
			}
			m := rows[0]
			if m.HasContent || m.Content != "" {
				t.Fatalf("accrual must be content-free, got HasContent=%v Content=%q", m.HasContent, m.Content)
			}
			if m.Ref != "run:"+runID {
				t.Fatalf("ref = %q, want run:%s", m.Ref, runID)
			}
			if want := "run:" + runID + ":" + status; m.SourceTrace != want {
				t.Fatalf("source_trace = %q, want %q", m.SourceTrace, want)
			}
			if m.PermissionScope != "operator:alice" {
				t.Fatalf("permission_scope = %q, want operator:alice", m.PermissionScope)
			}

			if n := countOutcome(t, pool, "run:"+runID, "memory.written"); n != 1 {
				t.Fatalf("memory.written count = %d, want 1", n)
			}
			if n := countActor(t, pool, "memory:"+m.ID, "operator:alice"); n != 1 {
				t.Fatalf("memory.written actor mismatch (count=%d)", n)
			}
		})
	}
}

// TestAccrueScopeDerivation — MEM-2's scope rule, live: a workstream-fired Run (actor =
// "workstream:<id>") accrues to the charter's scope with the workstream as the event
// actor; an agent-delegated Run accrues to the Run itself with the agent as actor.
func TestAccrueScopeDerivation(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	t.Run("workstream-fired", func(t *testing.T) {
		runID, wsID := freshID(t), freshID(t)
		actor := "workstream:" + wsID
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := s.AccrueRunTerminal(ctx, tx, runID, "completed", "operator:bob", actor, ""); err != nil {
			t.Fatalf("accrue: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}

		rows, err := s.List(ctx, "operator:bob", ScopeWorkstream, wsID, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("workstream-scoped rows = %d, want 1", len(rows))
		}
		if n := countActor(t, pool, "memory:"+rows[0].ID, actor); n != 1 {
			t.Fatalf("memory.written actor should be the workstream (count=%d)", n)
		}
	})

	t.Run("agent-delegated", func(t *testing.T) {
		runID := freshID(t)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if err := s.AccrueRunTerminal(ctx, tx, runID, "completed", "operator:carol", "agent:bot-1", "some-grant"); err != nil {
			t.Fatalf("accrue: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}

		rows, err := s.List(ctx, "operator:carol", ScopeRun, runID, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("run-scoped rows = %d, want 1", len(rows))
		}
		if n := countActor(t, pool, "memory:"+rows[0].ID, "agent:bot-1"); n != 1 {
			t.Fatalf("memory.written actor should be the agent (count=%d)", n)
		}
	})
}

// TestOwnerScopedReads — SEC-31/N3: List/Get never return another operator's rows; Get
// on a foreign or unknown id is ErrNotFound (the owner-404 house pattern).
func TestOwnerScopedReads(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	runID := freshID(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.AccrueRunTerminal(ctx, tx, runID, "completed", "operator:owner", "", ""); err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	rows, err := s.List(ctx, "operator:owner", ScopeRun, runID, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("owner list: rows=%d err=%v, want 1", len(rows), err)
	}
	id := rows[0].ID

	if _, err := s.Get(ctx, "operator:owner", id); err != nil {
		t.Fatalf("owner get: %v", err)
	}
	if _, err := s.Get(ctx, "operator:stranger", id); err != ErrNotFound {
		t.Fatalf("stranger get: got %v, want ErrNotFound", err)
	}
	if others, err := s.List(ctx, "operator:stranger", ScopeRun, runID, 0); err != nil || len(others) != 0 {
		t.Fatalf("stranger list: rows=%d err=%v, want 0", len(others), err)
	}
	if _, err := s.Get(ctx, "operator:owner", "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("unknown id: got %v, want ErrNotFound", err)
	}
}

// TestSealedContentRoundTrip — F3 parity: a directly-inserted authored row (the shape
// T-080's write route will produce) seals content at rest and Get/List open it back;
// a nil sealer (today's plaintext dev/transition wiring) round-trips too.
func TestSealedContentRoundTrip(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	sealed := stubSealer{}
	sealedStore := NewStore(pool, sealed, "api_outbox")

	id := freshID(t)
	scopeID := freshID(t)
	const plaintext = "operator note: the nightly job flakes on Tuesdays"
	ciphertext, err := sealed.Seal(plaintext)
	if err != nil {
		t.Fatalf("seal fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memories (id, scope_type, scope_id, key, content, source_trace, permission_scope)
		VALUES ($1, $2, $3, 'note', $4, 'operator', 'operator:author')`,
		id, ScopeOperator, scopeID, ciphertext); err != nil {
		t.Fatalf("direct insert: %v", err)
	}

	got, err := sealedStore.Get(ctx, "operator:author", id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.HasContent || got.Content != plaintext {
		t.Fatalf("sealed round-trip: HasContent=%v Content=%q, want %q", got.HasContent, got.Content, plaintext)
	}

	// The unsealed store must NOT open sealed bytes as if they were plaintext — it would
	// return garbage, not the original note (proving the round-trip is the sealer's, not
	// an accident of nil-safety).
	if plain, err := s.Get(ctx, "operator:author", id); err != nil || plain.Content == plaintext {
		t.Fatalf("plaintext-mode store unexpectedly round-tripped sealed content: %q (err %v)", plain.Content, err)
	}
}

// TestCreateAuthoredSealedRoundTrip — T-080 G1: CreateAuthored seals content via the
// store's own seal/open pair (not a direct insert), enqueues memory.written in the SAME
// transaction (actor = the authoring operator, content-free), and mints a fresh uuid v7
// id every call (dirty-DB re-run safety, the T-073 lesson).
func TestCreateAuthoredSealedRoundTrip(t *testing.T) {
	_, pool := testStore(t)
	sealedStore := NewStore(pool, stubSealer{}, "api_outbox")
	scopeID := freshID(t)
	const plaintext = "operator note: the nightly job flakes on Tuesdays"

	created, err := sealedStore.CreateAuthored(context.Background(), CreateInput{
		Owner: "operator:author", ScopeType: ScopeOperator, ScopeID: scopeID,
		Key: "note", Content: plaintext, SourceTrace: "operator",
	})
	if err != nil {
		t.Fatalf("create authored: %v", err)
	}
	if !created.HasContent || created.Content != plaintext {
		t.Fatalf("sealed round-trip on create: HasContent=%v Content=%q, want %q", created.HasContent, created.Content, plaintext)
	}
	if created.Ref != "" {
		t.Fatalf("authored row must carry no run anchor, got ref=%q", created.Ref)
	}
	if created.Confidence != 1.0 {
		t.Fatalf("confidence default = %v, want 1.0 (omitted Confidence)", created.Confidence)
	}

	// Re-fetch through the same store to prove the persisted bytes (not just the
	// in-memory return value) are sealed and open back correctly.
	got, err := sealedStore.Get(context.Background(), "operator:author", created.ID)
	if err != nil || got.Content != plaintext {
		t.Fatalf("get after create: content=%q err=%v, want %q", got.Content, err, plaintext)
	}

	if n := countOutcome(t, pool, ScopeOperator+":"+scopeID, "memory.written"); n != 1 {
		t.Fatalf("memory.written count = %d, want 1", n)
	}
	if n := countActor(t, pool, "memory:"+created.ID, "operator:author"); n != 1 {
		t.Fatalf("memory.written actor mismatch (count=%d)", n)
	}

	// A second CreateAuthored call (even with the same scope) mints a distinct id.
	again, err := sealedStore.CreateAuthored(context.Background(), CreateInput{
		Owner: "operator:author", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "a second, unrelated note",
	})
	if err != nil {
		t.Fatalf("create authored (second): %v", err)
	}
	if again.ID == created.ID {
		t.Fatalf("CreateAuthored must mint a fresh id per call, got a repeat: %s", again.ID)
	}
}

// TestListLimitClamping — D3/PERF-9: List's pushed-down limit clamps to [1, 200],
// defaulting to 50 when the caller passes 0; a limit smaller than the row count
// truncates the (id-ASC-ordered) result.
func TestListLimitClamping(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	scopeID := freshID(t)

	for i := 0; i < 4; i++ {
		if _, err := s.CreateAuthored(ctx, CreateInput{
			Owner: "operator:limiter", ScopeType: ScopeOperator, ScopeID: scopeID,
			Content: fmt.Sprintf("note %d", i),
		}); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	all, err := s.List(ctx, "operator:limiter", ScopeOperator, scopeID, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("default limit: rows=%d err=%v, want 4", len(all), err)
	}

	limited, err := s.List(ctx, "operator:limiter", ScopeOperator, scopeID, 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit=2: rows=%d err=%v, want 2", len(limited), err)
	}
	// id-ASC: the first 2 of the 4 seeded rows (uuid v7 ids are time-ordered).
	if limited[0].ID != all[0].ID || limited[1].ID != all[1].ID {
		t.Fatalf("limit=2 did not return the first 2 id-ASC rows")
	}

	// Out-of-range values clamp rather than error.
	if over, err := s.List(ctx, "operator:limiter", ScopeOperator, scopeID, 500); err != nil || len(over) != 4 {
		t.Fatalf("limit=500 (clamped to 200, still only 4 rows exist): rows=%d err=%v", len(over), err)
	}
	if neg, err := s.List(ctx, "operator:limiter", ScopeOperator, scopeID, -1); err != nil || len(neg) != 4 {
		t.Fatalf("limit=-1 (defaults to 50): rows=%d err=%v", len(neg), err)
	}
}

// TestRedact — T-081 G1: the guarded, idempotent tombstone flip. The first redact
// erases content and chains memory.redacted exactly once; a second (idempotent)
// redact makes no further change and enqueues no second event; a foreign owner is
// ErrNotFound (SEC-31/N3).
func TestRedact(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	scopeID := freshID(t)
	const plaintext = "operator note: redact me"

	created, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:redactor", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: plaintext, SourceTrace: "operator",
	})
	if err != nil {
		t.Fatalf("create authored: %v", err)
	}

	redacted, err := s.Redact(ctx, "operator:redactor", created.ID)
	if err != nil {
		t.Fatalf("redact: %v", err)
	}
	if redacted.HasContent || redacted.Content != "" {
		t.Fatalf("redacted row must be content-free, got HasContent=%v Content=%q", redacted.HasContent, redacted.Content)
	}
	if redacted.RedactedAt == nil {
		t.Fatal("redactedAt must be set")
	}
	if n := countOutcome(t, pool, "memory:"+created.ID, "memory.redacted"); n != 1 {
		t.Fatalf("memory.redacted count = %d, want 1", n)
	}
	if n := countActorForOutcome(t, pool, "memory:"+created.ID, "operator:redactor", "memory.redacted"); n != 1 {
		t.Fatalf("memory.redacted actor mismatch (count=%d)", n)
	}

	// Re-redact: idempotent no-op — still returns the tombstoned view, no second event.
	again, err := s.Redact(ctx, "operator:redactor", created.ID)
	if err != nil {
		t.Fatalf("re-redact: %v", err)
	}
	if again.RedactedAt == nil {
		t.Fatal("re-redact must still return the tombstoned view")
	}
	if n := countOutcome(t, pool, "memory:"+created.ID, "memory.redacted"); n != 1 {
		t.Fatalf("memory.redacted count after re-redact = %d, want 1 (no duplicate)", n)
	}

	// Foreign owner → ErrNotFound (never a silent success on someone else's row).
	if _, err := s.Redact(ctx, "operator:stranger", created.ID); err != ErrNotFound {
		t.Fatalf("stranger redact: got %v, want ErrNotFound", err)
	}
}

// TestSetPinned — T-081: pin/unpin are guarded, idempotent flips; each direction
// chains its own event exactly once per actual flip.
func TestSetPinned(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	scopeID := freshID(t)

	created, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:pinner", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "a pinnable note",
	})
	if err != nil {
		t.Fatalf("create authored: %v", err)
	}

	pinned, err := s.SetPinned(ctx, "operator:pinner", created.ID, true)
	if err != nil {
		t.Fatalf("pin: %v", err)
	}
	if !pinned.Pinned {
		t.Fatal("pinned must be true")
	}
	if n := countOutcome(t, pool, "memory:"+created.ID, "memory.pinned"); n != 1 {
		t.Fatalf("memory.pinned count = %d, want 1", n)
	}

	// Idempotent re-pin: no second event.
	if _, err := s.SetPinned(ctx, "operator:pinner", created.ID, true); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	if n := countOutcome(t, pool, "memory:"+created.ID, "memory.pinned"); n != 1 {
		t.Fatalf("memory.pinned count after re-pin = %d, want 1", n)
	}

	unpinned, err := s.SetPinned(ctx, "operator:pinner", created.ID, false)
	if err != nil {
		t.Fatalf("unpin: %v", err)
	}
	if unpinned.Pinned {
		t.Fatal("pinned must be false after unpin")
	}
	if n := countOutcome(t, pool, "memory:"+created.ID, "memory.unpinned"); n != 1 {
		t.Fatalf("memory.unpinned count = %d, want 1", n)
	}

	// Foreign owner → ErrNotFound.
	if _, err := s.SetPinned(ctx, "operator:stranger", created.ID, true); err != ErrNotFound {
		t.Fatalf("stranger pin: got %v, want ErrNotFound", err)
	}
}

// TestExpireDue — T-081 G1/DUR-9: the sweep tombstones exactly the expired-and-unpinned
// rows, spares pinned and unexpired rows, skips a row already redacted before its
// expiry (the guard, not the timer, is what makes this exactly-once), and a second
// pass expires nothing further.
func TestExpireDue(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	scopeID := freshID(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	// Row A: expired, unpinned — the ONLY row the sweep should touch.
	expiredUnpinned, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:sweeper", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "expired unpinned", ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("seed expired-unpinned: %v", err)
	}

	// Row B: expired but PINNED — expiry-exempt (ADR-0064 §3).
	expiredPinned, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:sweeper", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "expired pinned", ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("seed expired-pinned: %v", err)
	}
	if _, err := s.SetPinned(ctx, "operator:sweeper", expiredPinned.ID, true); err != nil {
		t.Fatalf("pin expired row: %v", err)
	}

	// Row C: not yet expired — untouched.
	unexpired, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:sweeper", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "not yet expired", ExpiresAt: &future,
	})
	if err != nil {
		t.Fatalf("seed unexpired: %v", err)
	}

	// Row D: already redacted before its expiry — the sweep's guard (redacted_at IS
	// NULL) must skip it; a redact-then-expire row is untouched by the sweep.
	alreadyRedacted, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:sweeper", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "redact then expire", ExpiresAt: &past,
	})
	if err != nil {
		t.Fatalf("seed already-redacted: %v", err)
	}
	if _, err := s.Redact(ctx, "operator:sweeper", alreadyRedacted.ID); err != nil {
		t.Fatalf("pre-redact: %v", err)
	}

	// First pass: expires exactly the unpinned expired row.
	n, err := s.ExpireDue(ctx, 100)
	if err != nil {
		t.Fatalf("expire due (pass 1): %v", err)
	}
	if n != 1 {
		t.Fatalf("expired count (pass 1) = %d, want 1", n)
	}

	got, err := s.Get(ctx, "operator:sweeper", expiredUnpinned.ID)
	if err != nil {
		t.Fatalf("get expired row: %v", err)
	}
	if got.RedactedAt == nil || got.HasContent {
		t.Fatalf("expired row must be tombstoned + content-free, got RedactedAt=%v HasContent=%v", got.RedactedAt, got.HasContent)
	}
	if n := countOutcome(t, pool, "memory:"+expiredUnpinned.ID, "memory.expired"); n != 1 {
		t.Fatalf("memory.expired count = %d, want 1", n)
	}
	if n := countActorForOutcome(t, pool, "memory:"+expiredUnpinned.ID, "operator:sweeper", "memory.expired"); n != 1 {
		t.Fatalf("memory.expired actor should be the row's own owner (count=%d)", n)
	}

	stillPinned, err := s.Get(ctx, "operator:sweeper", expiredPinned.ID)
	if err != nil || stillPinned.RedactedAt != nil {
		t.Fatalf("pinned row must be spared: RedactedAt=%v err=%v", stillPinned.RedactedAt, err)
	}
	stillUnexpired, err := s.Get(ctx, "operator:sweeper", unexpired.ID)
	if err != nil || stillUnexpired.RedactedAt != nil {
		t.Fatalf("unexpired row must be untouched: RedactedAt=%v err=%v", stillUnexpired.RedactedAt, err)
	}
	if n := countOutcome(t, pool, "memory:"+alreadyRedacted.ID, "memory.expired"); n != 0 {
		t.Fatalf("already-redacted row must not accrue memory.expired, got %d", n)
	}

	// Second pass: exactly-once — nothing left to expire.
	n2, err := s.ExpireDue(ctx, 100)
	if err != nil {
		t.Fatalf("expire due (pass 2): %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expired count (pass 2) = %d, want 0 (exactly-once)", n2)
	}
}

// TestAgentVisibleDefaultAndRoundTrip — T-087 (MEM-6 schema half; migration 0013):
// every row (accrued or authored) defaults agent_visible=false — the share flip's
// invisible-by-default posture (ADR-0065 §3) — and the column round-trips through
// Get/List. This ticket lands the column + the struct field ONLY: no verb/route sets
// it, so the flip here goes straight at the DB (T-091 lands memory.shared/unshared).
func TestAgentVisibleDefaultAndRoundTrip(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	scopeID := freshID(t)

	// Authored row: default false.
	authored, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:share", ScopeType: ScopeOperator, ScopeID: scopeID,
		Content: "not yet shared with any agent",
	})
	if err != nil {
		t.Fatalf("create authored: %v", err)
	}
	if authored.AgentVisible {
		t.Fatalf("authored row must default agent_visible=false, got true")
	}

	// Accrued row: default false too (the column applies uniformly, T-087's "harmless
	// default" note in migration 0013).
	runID := freshID(t)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.AccrueRunTerminal(ctx, tx, runID, "completed", "operator:share", "", ""); err != nil {
		t.Fatalf("accrue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	accruedRows, err := s.List(ctx, "operator:share", ScopeRun, runID, 0)
	if err != nil || len(accruedRows) != 1 {
		t.Fatalf("list accrued: rows=%d err=%v", len(accruedRows), err)
	}
	if accruedRows[0].AgentVisible {
		t.Fatalf("accrued row must default agent_visible=false, got true")
	}

	// Flip it directly at the DB (no verb exists yet at T-087) and prove Get/List
	// round-trip the new value — the column, not just the default, is wired end to end.
	if _, err := pool.Exec(ctx, `UPDATE memories SET agent_visible = true WHERE id = $1`, authored.ID); err != nil {
		t.Fatalf("flip agent_visible: %v", err)
	}
	got, err := s.Get(ctx, "operator:share", authored.ID)
	if err != nil || !got.AgentVisible {
		t.Fatalf("get after flip: AgentVisible=%v err=%v, want true", got.AgentVisible, err)
	}
	listed, err := s.List(ctx, "operator:share", ScopeOperator, scopeID, 0)
	if err != nil {
		t.Fatalf("list after flip: %v", err)
	}
	found := false
	for _, m := range listed {
		if m.ID == authored.ID {
			found = true
			if !m.AgentVisible {
				t.Fatalf("list did not round-trip agent_visible=true")
			}
		}
	}
	if !found {
		t.Fatal("flipped row missing from list")
	}
}

// TestReadForAgent — MEM-5/SEC-32/R4 (ADR-0065 §3, T-090), the live-PG proof of the
// agent-memory read plane: an accrued (content-free) row is ALWAYS readable; an
// authored row is readable ONLY once agent_visible is flipped (N2, the share gate); a
// tombstoned (redacted) row is EXCLUDED even though it would otherwise be in scope
// (R2); a SECOND composer's charter+grant is completely unreachable (N1, cross-grant
// containment — no filtering, structural); a workstreamId filter narrows to that one
// charter's grant (still including its Runs) and an unrelated/unknown id answers empty
// (no oracle); the result clamps; and after the grant is revoked, the read answers
// empty (N3). Exactly ONE memory.read event chains per call, content-free (row ids
// only), actor = the reading agent, edge = agent-memory.
func TestReadForAgent(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	gs := grant.NewStore(pool, "api_outbox")
	wsStore := workstream.NewStore(pool, nil, gs, "api_outbox")
	runStore := run.NewStore(pool, nil, "api_outbox", nil)

	composer := "agent:mem-composer-" + freshID(t)
	ws, err := wsStore.Create(ctx, workstream.Input{
		Principal: "operator:mem-read", Label: "adaptive", Objective: "learn and adapt",
		IntervalSeconds: 3600, Kind: workstream.KindAdaptive, Composer: composer,
		Grant: grant.Input{
			Resources: []string{"res-a"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10,
		},
	})
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}

	launchedRun, err := runStore.Create(ctx, run.Input{
		Operator: "operator:mem-read", Actor: composer, GrantID: ws.GrantID,
		Steps: []run.StepInput{{ResourceID: "res-a", Command: "uptime"}},
	})
	if err != nil {
		t.Fatalf("create bound run: %v", err)
	}

	// A second, UNRELATED composer + charter — the cross-grant containment fixture
	// (N1): none of this must ever surface in composer's read.
	otherComposer := "agent:mem-composer-other-" + freshID(t)
	otherWS, err := wsStore.Create(ctx, workstream.Input{
		Principal: "operator:mem-read-2", Label: "adaptive-2", Objective: "a foreign charter",
		IntervalSeconds: 3600, Kind: workstream.KindAdaptive, Composer: otherComposer,
		Grant: grant.Input{
			Resources: []string{"res-b"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10,
		},
	})
	if err != nil {
		t.Fatalf("create foreign charter: %v", err)
	}

	insertAccrued := func(scopeType, scopeID string) string {
		id := freshID(t)
		if _, err := pool.Exec(ctx, `
			INSERT INTO memories (id, scope_type, scope_id, ref, source_trace, permission_scope)
			VALUES ($1, $2, $3, 'run:x', 'run:x:completed', 'operator:mem-read')`,
			id, scopeType, scopeID); err != nil {
			t.Fatalf("insert accrued fixture: %v", err)
		}
		return id
	}
	wsAccrued := insertAccrued(ScopeWorkstream, ws.ID)
	runAccrued := insertAccrued(ScopeRun, launchedRun.ID)
	foreignAccrued := insertAccrued(ScopeWorkstream, otherWS.ID)

	unshared, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:mem-read", ScopeType: ScopeWorkstream, ScopeID: ws.ID,
		Content: "not yet shared with the composer",
	})
	if err != nil {
		t.Fatalf("create unshared authored: %v", err)
	}
	shared, err := s.CreateAuthored(ctx, CreateInput{
		Owner: "operator:mem-read", ScopeType: ScopeRun, ScopeID: launchedRun.ID,
		Content: "shared with the composer",
	})
	if err != nil {
		t.Fatalf("create shared authored: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE memories SET agent_visible = true WHERE id = $1`, shared.ID); err != nil {
		t.Fatalf("flip agent_visible (no verb exists yet, T-091): %v", err)
	}

	// A tombstoned accrued row: in scope by scope_type/scope_id, but redacted — must be
	// EXCLUDED (R2).
	tombstoned := insertAccrued(ScopeWorkstream, ws.ID)
	if _, err := pool.Exec(ctx, `UPDATE memories SET redacted_at = now() WHERE id = $1`, tombstoned); err != nil {
		t.Fatalf("tombstone fixture: %v", err)
	}

	got, err := s.ReadForAgent(ctx, composer, "", 0)
	if err != nil {
		t.Fatalf("read for agent: %v", err)
	}
	byID := map[string]Memory{}
	for _, m := range got {
		byID[m.ID] = m
	}
	if _, ok := byID[wsAccrued]; !ok {
		t.Fatal("workstream-scoped accrued row missing (always-readable)")
	}
	if _, ok := byID[runAccrued]; !ok {
		t.Fatal("run-scoped accrued row missing (always-readable, via runs.grant_id)")
	}
	if _, ok := byID[shared.ID]; !ok || byID[shared.ID].Content != "shared with the composer" {
		t.Fatalf("shared authored row missing or content not opened: %+v", byID[shared.ID])
	}
	if _, ok := byID[unshared.ID]; ok {
		t.Fatal("unshared authored row must be invisible (N2, the share gate)")
	}
	if _, ok := byID[tombstoned]; ok {
		t.Fatal("tombstoned row must be excluded even though otherwise in scope (R2)")
	}
	if _, ok := byID[foreignAccrued]; ok {
		t.Fatal("a foreign charter's row must be structurally unreachable (N1)")
	}
	if len(got) != 3 {
		t.Fatalf("read for agent = %d rows, want exactly 3 (ws-accrued, run-accrued, shared-authored): %+v", len(got), got)
	}

	// Exactly ONE memory.read event, content-free (no row's content appears in the
	// target), actor = the composer, edge = agent-memory.
	if n := countOutcomeByActor(t, pool, composer, "memory.read"); n != 1 {
		t.Fatalf("memory.read count for composer = %d, want 1", n)
	}
	tgt := eventTarget(t, pool, composer, "memory.read")
	if !strings.Contains(tgt, wsAccrued) || !strings.Contains(tgt, runAccrued) || !strings.Contains(tgt, shared.ID) {
		t.Fatalf("memory.read target = %q, want it to list all 3 returned row ids", tgt)
	}
	if strings.Contains(tgt, "shared with the composer") {
		t.Fatal("memory.read event leaked content — must be content-free")
	}

	// workstreamId filter: narrowed to ws.ID still includes its Runs (same grant).
	filtered, err := s.ReadForAgent(ctx, composer, ws.ID, 0)
	if err != nil || len(filtered) != 3 {
		t.Fatalf("read filtered to own charter: rows=%d err=%v, want 3", len(filtered), err)
	}
	// Filtered to the foreign charter, or an unknown id: empty, never an error (no
	// oracle).
	if rows, err := s.ReadForAgent(ctx, composer, otherWS.ID, 0); err != nil || len(rows) != 0 {
		t.Fatalf("read filtered to a foreign charter: rows=%d err=%v, want 0", len(rows), err)
	}
	if rows, err := s.ReadForAgent(ctx, composer, "00000000-0000-0000-0000-000000000000", 0); err != nil || len(rows) != 0 {
		t.Fatalf("read filtered to an unknown id: rows=%d err=%v, want 0", len(rows), err)
	}

	// Clamp: limit=1 truncates id-ASC.
	if clamped, err := s.ReadForAgent(ctx, composer, "", 1); err != nil || len(clamped) != 1 {
		t.Fatalf("read clamped to 1: rows=%d err=%v, want 1", len(clamped), err)
	}

	// N3: after the grant is revoked, the composer's read answers empty.
	if _, err := gs.Revoke(ctx, ws.GrantID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	if rows, err := s.ReadForAgent(ctx, composer, "", 0); err != nil || len(rows) != 0 {
		t.Fatalf("read after grant revoke: rows=%d err=%v, want 0", len(rows), err)
	}
}

func countOutcomeByActor(t *testing.T, pool *pgxpool.Pool, actor, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'actor' = $1 AND payload->>'outcome' = $2`,
		actor, outcome).Scan(&n); err != nil {
		t.Fatalf("count outcome by actor: %v", err)
	}
	return n
}

func eventTarget(t *testing.T, pool *pgxpool.Pool, actor, outcome string) string {
	t.Helper()
	var target string
	if err := pool.QueryRow(context.Background(), `
		SELECT payload->>'target' FROM api_outbox WHERE payload->>'actor' = $1 AND payload->>'outcome' = $2`,
		actor, outcome).Scan(&target); err != nil {
		t.Fatalf("event target: %v", err)
	}
	return target
}

// stubSealer is a reversible test fixture (NOT real encryption) — it only needs to
// prove the store calls Seal/Open, not that a specific cipher is used (the real
// AES-256-GCM sealer is exercised by packages/go/auth's own tests).
type stubSealer struct{}

func (stubSealer) Seal(plaintext string) ([]byte, error) {
	b := []byte(plaintext)
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5A
	}
	return out, nil
}

func (stubSealer) Open(b []byte) (string, error) {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5A
	}
	return string(out), nil
}

func countOutcome(t *testing.T, pool *pgxpool.Pool, evidence, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
		evidence, outcome).Scan(&n); err != nil {
		t.Fatalf("count outcome: %v", err)
	}
	return n
}

func countActor(t *testing.T, pool *pgxpool.Pool, target, actor string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'target' = $1 AND payload->>'actor' = $2`,
		target, actor).Scan(&n); err != nil {
		t.Fatalf("count actor: %v", err)
	}
	return n
}

// countActorForOutcome is countActor narrowed to one outcome — needed once a memory
// row accrues MORE than one event over its life (e.g. memory.written then
// memory.redacted both share the same target+actor), so counting by target+actor
// alone would double-count across outcomes.
func countActorForOutcome(t *testing.T, pool *pgxpool.Pool, target, actor, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'target' = $1 AND payload->>'actor' = $2 AND payload->>'outcome' = $3`,
		target, actor, outcome).Scan(&n); err != nil {
		t.Fatalf("count actor for outcome: %v", err)
	}
	return n
}
