//go:build integration

// Integration tests for SearchForAgent (T-098, CTX-4/SEC-33/AUD-21/K4) against a real
// Postgres — the same harness as store_integration_test.go.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/ctxindex/...
package ctxindex

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/workstream"
)

// TestSearchForAgent — CTX-4/SEC-33/AUD-21/K4, the live-PG proof of the agent-plane
// context read plane: a query hitting the caller's OWN charter-bound source's chunks
// is returned (ranked); the SAME agent can never reach a foreign charter's source
// (N1, cross-grant containment — structural, not filtered); a workstreamId naming the
// foreign charter (or an unknown id) answers empty, never an error (no oracle); an
// empty/whitespace query answers empty; a revoked/expired grant answers empty (N3).
// Exactly ONE content-free context.read event chains per call, on the agent-workspace
// edge, target listing exactly the returned chunk ids. Re-runnable against a dirty DB
// (T-073): every id is freshly minted.
func TestSearchForAgent(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	gs := grant.NewStore(pool, "api_outbox")
	wsStore := workstream.NewStore(pool, nil, gs, "api_outbox")

	composerA := "agent:ctx-composer-a-" + freshID(t)
	wsA, err := wsStore.Create(ctx, workstream.Input{
		Principal: "operator:ctx-read", Label: "adaptive-a", Objective: "compose over context A",
		IntervalSeconds: 3600, Kind: workstream.KindAdaptive, Composer: composerA,
		Grant: grant.Input{
			Resources: []string{"res-a"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10,
		},
	})
	if err != nil {
		t.Fatalf("create charter A: %v", err)
	}

	// A second, UNRELATED composer + charter + source — the cross-grant containment
	// fixture (N1): none of this must ever surface in composerA's search, regardless
	// of query or workstreamId.
	composerB := "agent:ctx-composer-b-" + freshID(t)
	wsB, err := wsStore.Create(ctx, workstream.Input{
		Principal: "operator:ctx-read-2", Label: "adaptive-b", Objective: "compose over context B",
		IntervalSeconds: 3600, Kind: workstream.KindAdaptive, Composer: composerB,
		Grant: grant.Input{
			Resources: []string{"res-b"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10,
		},
	})
	if err != nil {
		t.Fatalf("create charter B: %v", err)
	}

	srcA, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:ctx-read", RootPath: "/srv/ctx-a", ScopeType: ScopeWorkstream, ScopeID: wsA.ID,
	})
	if err != nil {
		t.Fatalf("create source A: %v", err)
	}
	srcB, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:ctx-read-2", RootPath: "/srv/ctx-b", ScopeType: ScopeWorkstream, ScopeID: wsB.ID,
	})
	if err != nil {
		t.Fatalf("create source B: %v", err)
	}

	if err := s.ReindexFile(ctx, srcA.ID, "readme.md", time.Now(), 32, "hash-a", []Chunk{
		{Locator: "intro", Content: "the alpha runbook describes the alpha deployment"},
	}); err != nil {
		t.Fatalf("index source A: %v", err)
	}
	if err := s.ReindexFile(ctx, srcB.ID, "readme.md", time.Now(), 31, "hash-b", []Chunk{
		{Locator: "intro", Content: "the bravo runbook describes the bravo deployment"},
	}); err != nil {
		t.Fatalf("index source B: %v", err)
	}

	// composerA searching its own charter's term: returns srcA's chunk.
	got, err := s.SearchForAgent(ctx, composerA, "", "alpha", 0)
	if err != nil {
		t.Fatalf("search alpha: %v", err)
	}
	if len(got) != 1 || got[0].SourceID != srcA.ID || !strings.Contains(got[0].Content, "alpha") {
		t.Fatalf("search alpha = %+v, want exactly srcA's chunk", got)
	}

	// N1: composerA searching for B's term returns nothing — cross-grant containment,
	// not merely "no match."
	if rows, err := s.SearchForAgent(ctx, composerA, "", "bravo", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search bravo (foreign term) by composerA: rows=%d err=%v, want 0", len(rows), err)
	}

	// N1/no-oracle: a workstreamId naming the FOREIGN charter answers empty even for
	// composerA's own term — the foreign id is no oracle, same as unknown.
	if rows, err := s.SearchForAgent(ctx, composerA, wsB.ID, "alpha", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search alpha filtered to foreign charter: rows=%d err=%v, want 0", len(rows), err)
	}
	// An unknown workstreamId is likewise empty, never an error.
	if rows, err := s.SearchForAgent(ctx, composerA, "00000000-0000-0000-0000-000000000000", "alpha", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search alpha filtered to unknown id: rows=%d err=%v, want 0", len(rows), err)
	}
	// Filtered to composerA's OWN charter: still returns srcA's chunk.
	if rows, err := s.SearchForAgent(ctx, composerA, wsA.ID, "alpha", 0); err != nil || len(rows) != 1 {
		t.Fatalf("search alpha filtered to own charter: rows=%d err=%v, want 1", len(rows), err)
	}

	// An empty (or whitespace-only) query answers empty, never an error.
	if rows, err := s.SearchForAgent(ctx, composerA, "", "", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search empty query: rows=%d err=%v, want 0", len(rows), err)
	}
	if rows, err := s.SearchForAgent(ctx, composerA, "", "   ", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search whitespace query: rows=%d err=%v, want 0", len(rows), err)
	}

	// N4: exactly one context.read event per call so far (7 calls above), content-free,
	// actor = composerA, edge = agent-workspace. The first (successful, non-empty)
	// call's target lists exactly the returned chunk id.
	if n := countOutcomeByActor(t, pool, composerA, "context.read"); n != 7 {
		t.Fatalf("context.read count for composerA = %d, want 7 (one per call)", n)
	}
	tgt := eventTarget(t, pool, composerA, "context.read", got[0].ChunkID)
	if tgt != "context:"+got[0].ChunkID {
		t.Fatalf("context.read target = %q, want %q", tgt, "context:"+got[0].ChunkID)
	}
	if strings.Contains(tgt, "alpha") || strings.Contains(tgt, "runbook") {
		t.Fatal("context.read event leaked chunk content — must be content-free")
	}
	if edge := eventEdge(t, pool, composerA, "context.read", got[0].ChunkID); edge != agentWorkspaceEdge {
		t.Fatalf("context.read edge = %q, want %q", edge, agentWorkspaceEdge)
	}
	// The empty-result calls each still chained their own event, target "context:".
	if n := countTargetExact(t, pool, composerA, "context.read", "context:"); n == 0 {
		t.Fatal("an empty-result search must still chain its own context.read event (fail-loud, N4)")
	}

	// N3: after the grant backing wsA is revoked, composerA's search answers empty.
	if _, err := gs.Revoke(ctx, wsA.GrantID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	if rows, err := s.SearchForAgent(ctx, composerA, "", "alpha", 0); err != nil || len(rows) != 0 {
		t.Fatalf("search after grant revoke: rows=%d err=%v, want 0", len(rows), err)
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

func countTargetExact(t *testing.T, pool *pgxpool.Pool, actor, outcome, target string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'actor' = $1 AND payload->>'outcome' = $2 AND payload->>'target' = $3`,
		actor, outcome, target).Scan(&n); err != nil {
		t.Fatalf("count target exact: %v", err)
	}
	return n
}

// eventTarget returns the target of the ONE context.read event for actor whose target
// contains chunkID (there may be several context.read rows per actor across a test's
// calls; this disambiguates the one this assertion cares about).
func eventTarget(t *testing.T, pool *pgxpool.Pool, actor, outcome, chunkID string) string {
	t.Helper()
	var target string
	if err := pool.QueryRow(context.Background(), `
		SELECT payload->>'target' FROM api_outbox
		 WHERE payload->>'actor' = $1 AND payload->>'outcome' = $2 AND payload->>'target' LIKE '%' || $3 || '%'
		 LIMIT 1`, actor, outcome, chunkID).Scan(&target); err != nil {
		t.Fatalf("event target: %v", err)
	}
	return target
}

// eventEdge is eventTarget's sibling for the edge column (the SEC-33 assertion: the
// context.read event must carry the reserved agent-workspace edge, never any other).
func eventEdge(t *testing.T, pool *pgxpool.Pool, actor, outcome, chunkID string) string {
	t.Helper()
	var edge string
	if err := pool.QueryRow(context.Background(), `
		SELECT payload->>'edge' FROM api_outbox
		 WHERE payload->>'actor' = $1 AND payload->>'outcome' = $2 AND payload->>'target' LIKE '%' || $3 || '%'
		 LIMIT 1`, actor, outcome, chunkID).Scan(&edge); err != nil {
		t.Fatalf("event edge: %v", err)
	}
	return edge
}
