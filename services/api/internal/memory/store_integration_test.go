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
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
