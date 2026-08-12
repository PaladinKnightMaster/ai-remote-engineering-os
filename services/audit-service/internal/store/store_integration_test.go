//go:build integration

// Integration tests for the store against a real Postgres. They are the
// ADR-0045 §Verification concurrent-append integrity test and the dedup test.
//
// Run them where the Go toolchain and a Postgres are available:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export AUDIT_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
//	go run ./services/audit-service migrate up   # apply 0001_init
//	go test -tags integration ./services/audit-service/internal/store/...
package store

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("AUDIT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

// ingest builds a valid ingest payload with a fresh UUID v7 idempotency key.
func ingest() audit.Ingest {
	key := uuid.Must(uuid.NewV7()).String()
	return audit.Ingest{
		Timestamp: "2026-05-20T00:00:00Z", Actor: "svc:test",
		Edge: "service-service", Target: "t:" + key, IdempotencyKey: key,
	}
}

// Concurrent appends serialize into a single linear chain with no forks/gaps.
func TestConcurrentAppendIntegrity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, _, err := s.Append(ctx, ingest()); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	events, _, err := s.List(ctx, ListQuery{Direction: Asc, Limit: 1000})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) < n {
		t.Fatalf("expected >= %d events, got %d", n, len(events))
	}
	if idx := chain.Verify(events); idx != -1 {
		t.Fatalf("chain broken at index %d under concurrent append", idx)
	}
}

// A replayed idempotencyKey returns the stored event and does not extend the chain.
func TestDedupReplayIntegrity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := ingest()

	first, deduped1, err := s.Append(ctx, in)
	if err != nil || deduped1 {
		t.Fatalf("first append: deduped=%v err=%v", deduped1, err)
	}
	second, deduped2, err := s.Append(ctx, in)
	if err != nil {
		t.Fatalf("replay append: %v", err)
	}
	if !deduped2 {
		t.Fatal("replay should be flagged deduped")
	}
	if first.ID != second.ID || first.ChainHash != second.ChainHash {
		t.Fatalf("replay returned a different event: %+v vs %+v", first, second)
	}
}

// ---- T-105: the read-layer tests (ADR-0067, v0.11.0 release criteria) ----

// ingestWith builds a valid ingest payload with a fresh idempotency key and
// the given actor/edge/outcome. An empty outcome stores as NULL (Append's
// NULLIF on the outcome column) — the AUD-31 case. Follows the T-073
// dirty-DB lesson:
// callers pass a run-unique actor/marker so a test's assertions cannot be
// confused by rows left behind by other tests or prior runs against the same
// database (audit_events is append-only and never truncated between tests).
func ingestWith(actor, edge, outcome string) audit.Ingest {
	key := uuid.Must(uuid.NewV7()).String()
	return audit.Ingest{
		Timestamp: "2026-05-20T00:00:00Z", Actor: actor, Edge: edge,
		Target: "t:" + key, Outcome: outcome, IdempotencyKey: key,
	}
}

// uniqueMarker returns a run-unique actor value so a test's seeded events can
// be isolated from every other test's and every prior run's data sharing the
// same database.
func uniqueMarker(t *testing.T) string {
	t.Helper()
	return "marker:" + uuid.Must(uuid.NewV7()).String()
}

// listAll pages q to completion, advancing q.Cursor by the boundary List
// returns after each full page, and returns every matching event in walked
// order. Works for either direction: List's returned cursor is always the
// correct boundary to continue in the SAME direction.
func listAll(t *testing.T, s *Store, q ListQuery) []audit.Event {
	t.Helper()
	ctx := context.Background()
	var all []audit.Event
	for {
		page, cursor, err := s.List(ctx, q)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		all = append(all, page...)
		if len(page) < q.Limit {
			break
		}
		q.Cursor = cursor
	}
	return all
}

// TestKeysetStabilityBothDirections proves AUD-22/AUD-23's base invariant: a
// full descending walk is the exact reverse of a full ascending walk over the
// same set — no skips, no duplicates, in either direction — using an
// actor-scoped filter so the assertion is exact regardless of what other
// tests or runs have appended to the shared database.
func TestKeysetStabilityBothDirections(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	const n = 12
	for i := 0; i < n; i++ {
		if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	asc := listAll(t, s, ListQuery{Direction: Asc, Actor: &marker, Limit: 5})
	desc := listAll(t, s, ListQuery{Direction: Desc, Actor: &marker, Limit: 5})

	if len(asc) != n || len(desc) != n {
		t.Fatalf("expected %d events each direction, got asc=%d desc=%d", n, len(asc), len(desc))
	}
	for i := 1; i < len(asc); i++ {
		if asc[i].Seq <= asc[i-1].Seq {
			t.Fatalf("ascending walk not strictly increasing at %d: %d then %d", i, asc[i-1].Seq, asc[i].Seq)
		}
	}
	for i := 1; i < len(desc); i++ {
		if desc[i].Seq >= desc[i-1].Seq {
			t.Fatalf("descending walk not strictly decreasing at %d: %d then %d", i, desc[i-1].Seq, desc[i].Seq)
		}
	}
	for i := range asc {
		if asc[i].ID != desc[len(desc)-1-i].ID {
			t.Fatalf("descending walk is not the exact reverse of ascending at position %d", i)
		}
	}
}

// TestKeysetStableUnderConcurrentAppend is the live AUD-23 test: paging a
// descending window bounded by a fixed cursor is stable under a concurrent
// append landing mid-walk, because keyset (unlike offset) pagination places
// new rows at a higher seq — structurally outside a window already bounded
// below the current head (ADR-0067 red-team #8).
func TestKeysetStableUnderConcurrentAppend(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	const n = 12
	for i := 0; i < n; i++ {
		if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	headBefore, err := s.ChainHead(ctx)
	if err != nil {
		t.Fatalf("chain head: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Races the paged read below. Half share the marker actor, proving
		// they are excluded by the seq boundary, not by coincidence of actor.
		for i := 0; i < 6; i++ {
			_, _, _ = s.Append(ctx, ingestWith(marker, "service-service", "run.completed"))
			_, _, _ = s.Append(ctx, ingestWith("other:"+marker, "service-service", "run.completed"))
		}
	}()

	walked := listAll(t, s, ListQuery{Direction: Desc, Actor: &marker, Cursor: headBefore + 1, Limit: 4})
	wg.Wait()

	if len(walked) != n {
		t.Fatalf("expected exactly the pre-existing %d events under the fixed cursor, got %d "+
			"(a concurrent append leaked into a bounded window)", n, len(walked))
	}
	seen := map[string]bool{}
	for _, e := range walked {
		if seen[e.ID] {
			t.Fatalf("duplicate event %s in descending walk under concurrent append", e.ID)
		}
		seen[e.ID] = true
		if e.Seq > headBefore {
			t.Fatalf("event %s (seq %d) is beyond the fixed cursor %d — window stability violated", e.ID, e.Seq, headBefore)
		}
	}
}

// TestWholeChainFiltering is AUD-25's live test: a filtered read reaches
// matches beyond the first unfiltered page's worth of events — the whole
// chain, not the loaded page (the v0.1.0 rule this ADR answers).
func TestWholeChainFiltering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	noiseActor := "noise:" + uuid.Must(uuid.NewV7()).String()
	marker := uniqueMarker(t)

	headBefore, err := s.ChainHead(ctx)
	if err != nil {
		t.Fatalf("chain head: %v", err)
	}

	const noiseCount = 60
	for i := 0; i < noiseCount; i++ {
		if _, _, err := s.Append(ctx, ingestWith(noiseActor, "service-service", "run.completed")); err != nil {
			t.Fatalf("seed noise %d: %v", i, err)
		}
	}
	const targetCount = 3
	for i := 0; i < targetCount; i++ {
		if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
			t.Fatalf("seed target %d: %v", i, err)
		}
	}

	const pageSize = 50
	unfiltered, _, err := s.List(ctx, ListQuery{Direction: Asc, Cursor: headBefore, Limit: pageSize})
	if err != nil {
		t.Fatalf("unfiltered list: %v", err)
	}
	if len(unfiltered) != pageSize {
		t.Fatalf("expected a full unfiltered page of %d, got %d", pageSize, len(unfiltered))
	}
	for _, e := range unfiltered {
		if e.Actor == marker {
			t.Fatal("a target event appeared in the first unfiltered page — test setup invalid, targets are not deep enough")
		}
	}

	filtered, _, err := s.List(ctx, ListQuery{Direction: Asc, Actor: &marker, Cursor: headBefore, Limit: pageSize})
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered) != targetCount {
		t.Fatalf("expected %d filtered matches drawn from the whole chain, got %d", targetCount, len(filtered))
	}
}

// TestPresetMembershipAndNullGuard is the live AUD-28 + AUD-31 test: the
// governed-actions@1 preset excludes exactly {plan.requested, plan.expired}
// and nothing else — and, block-if-violated, a NULL-outcome event APPEARS
// under the preset (asserting presence, not absence).
func TestPresetMembershipAndNullGuard(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	requested, _, err := s.Append(ctx, ingestWith(marker, "human-agent", "plan.requested"))
	if err != nil {
		t.Fatalf("seed plan.requested: %v", err)
	}
	expired, _, err := s.Append(ctx, ingestWith(marker, "human-agent", "plan.expired"))
	if err != nil {
		t.Fatalf("seed plan.expired: %v", err)
	}
	completed, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed"))
	if err != nil {
		t.Fatalf("seed run.completed: %v", err)
	}
	noOutcome, _, err := s.Append(ctx, ingestWith(marker, "service-service", ""))
	if err != nil {
		t.Fatalf("seed no-outcome: %v", err)
	}

	got := listAll(t, s, ListQuery{Direction: Asc, Actor: &marker, Preset: true, Limit: 10})

	byID := map[string]bool{}
	for _, e := range got {
		byID[e.ID] = true
	}
	if byID[requested.ID] || byID[expired.ID] {
		t.Fatalf("preset failed to exclude a lifecycle event: got %+v", got)
	}
	if !byID[completed.ID] {
		t.Fatal("preset excluded run.completed, which is outside {plan.requested, plan.expired} — membership must be exact")
	}
	if !byID[noOutcome.ID] {
		t.Fatal("AUD-31 violated: a NULL-outcome event was silently excluded by the preset")
	}
	if len(got) != 2 {
		t.Fatalf("expected exactly 2 survivors (run.completed + no-outcome), got %d: %+v", len(got), got)
	}
}

// TestChainHeadAcrossSequenceGap is the live AUD-29 test: ChainHead equals
// MAX(seq) — a position, not a row count — even across a deliberately
// induced sequence gap (seq is BIGSERIAL; nextval is non-transactional, so a
// rolled-back Append burns a value with no row to show for it).
func TestChainHeadAcrossSequenceGap(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	headBefore, err := s.ChainHead(ctx)
	if err != nil {
		t.Fatalf("chain head before: %v", err)
	}

	var seqName string
	if err := s.pool.QueryRow(ctx, `SELECT pg_get_serial_sequence('audit_events', 'seq')`).Scan(&seqName); err != nil {
		t.Fatalf("resolve seq sequence name: %v", err)
	}
	// Burn one value with no row inserted — exactly what a rolled-back Append
	// leaves behind, since nextval() is not rolled back with its transaction.
	if _, err := s.pool.Exec(ctx, `SELECT nextval($1)`, seqName); err != nil {
		t.Fatalf("burn nextval: %v", err)
	}

	e, _, err := s.Append(ctx, ingestWith("gap-test:"+uuid.Must(uuid.NewV7()).String(), "service-service", "run.completed"))
	if err != nil {
		t.Fatalf("append after gap: %v", err)
	}
	if e.Seq != headBefore+2 {
		t.Fatalf("expected the real row at seq %d (headBefore+2, leaving a gap at +1), got %d", headBefore+2, e.Seq)
	}

	headAfter, err := s.ChainHead(ctx)
	if err != nil {
		t.Fatalf("chain head after: %v", err)
	}
	if headAfter != e.Seq {
		t.Fatalf("ChainHead must equal MAX(seq)=%d even across the induced gap, got %d", e.Seq, headAfter)
	}
}

// TestCompositionFilterDirectionCursor proves filter x direction x cursor
// compose in one query: an outcome filter walked descending, paged one row
// at a time, returns exactly the matching subset in the correct order.
func TestCompositionFilterDirectionCursor(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	var wantDesc []audit.Event // outcome-a events, in insertion (ascending) order
	for i := 0; i < 6; i++ {
		outcome := "outcome-b"
		if i%2 == 0 {
			outcome = "outcome-a"
		}
		e, _, err := s.Append(ctx, ingestWith(marker, "service-service", outcome))
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if outcome == "outcome-a" {
			wantDesc = append(wantDesc, e)
		}
	}
	for i, j := 0, len(wantDesc)-1; i < j; i, j = i+1, j-1 {
		wantDesc[i], wantDesc[j] = wantDesc[j], wantDesc[i]
	}

	outcomeA := "outcome-a"
	got := listAll(t, s, ListQuery{Direction: Desc, Actor: &marker, Outcome: &outcomeA, Limit: 1})
	if len(got) != len(wantDesc) {
		t.Fatalf("expected %d composed matches, got %d", len(wantDesc), len(got))
	}
	for i := range got {
		if got[i].ID != wantDesc[i].ID {
			t.Fatalf("composition mismatch at position %d: got %s want %s", i, got[i].ID, wantDesc[i].ID)
		}
	}
}

// TestExplainUsesIndexScan is the live PERF-10 test: on a seeded
// production-shaped chain, EXPLAIN shows Index Scan (never Seq Scan) for the
// unfiltered, preset, and each single-dimension filtered read.
//
// Actor AND outcome cardinality are both deliberately high (250 distinct
// values each over 2000 rows, ~8 rows/value). This was tuned live, not
// assumed: an earlier cut used only 6 distinct outcome values (5 fixed
// lifecycle-shaped strings + 1 rare one) on the theory that the *specific*
// rare value was what mattered — but a forced GENERIC plan cannot fold in
// the specific bound value, so it estimates selectivity from the column's
// overall value population. With only 6 distinct values the "typical value"
// estimate is noisy and sometimes tips toward the composite index, sometimes
// away, depending on ANALYZE's sampling — confirmed flaky across repeated
// live runs against a freshly migrated database. Raising outcome to the same
// 250-way split as actor removes that noise: the generic estimate for
// "typical value" selectivity is then uniformly low regardless of which
// specific value is bound, matching actor's already-robust behavior. Low
// cardinality *per value* is exactly the PERF-7 case a composite index earns
// its keep on (a highly selective equality + ORDER BY seq LIMIT n cannot be
// satisfied cheaply by walking the PK backward with a filter, the way a
// common value can) — but that argument only holds when the *column*, not
// merely one literal, is high-cardinality; see edge_filter_generic's
// documented exception below for the column that stays low-cardinality by
// design. "agent-repo" is never seeded at all, mirroring PERF-7's own
// zero-match rationale.
func TestExplainUsesIndexScan(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	const seedCount = 2000
	const actorCount = 250
	const outcomeCount = 250
	actors := make([]string, actorCount)
	for i := range actors {
		actors[i] = fmt.Sprintf("perf-actor-%d-%s", i, uuid.Must(uuid.NewV7()).String())
	}
	// agent-repo intentionally excluded from seeding — the PERF-7 zero-match case.
	edges := []string{
		"human-agent", "agent-agent", "agent-tool", "agent-workspace",
		"agent-memory", "agent-remote-session", "service-service",
	}
	// A handful of slots carry the real content-free lifecycle outcomes (for
	// the preset test's Filter clause to have something to exclude); the rest
	// are distinct high-cardinality values, same shape as actors above.
	outcomes := make([]string, outcomeCount)
	outcomes[0] = "plan.requested"
	outcomes[1] = "plan.expired"
	outcomes[2] = "" // stores as NULL — exercises the preset's NULL-safety at volume too
	for i := 3; i < outcomeCount; i++ {
		outcomes[i] = fmt.Sprintf("perf-outcome-%d-%s", i, uuid.Must(uuid.NewV7()).String())
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for i := 0; i < seedCount; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			in := ingestWith(actors[i%len(actors)], edges[i%len(edges)], outcomes[i%len(outcomes)])
			if _, _, err := s.Append(ctx, in); err != nil {
				t.Errorf("seed %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	if t.Failed() {
		t.Fatal("seeding failed, aborting before EXPLAIN")
	}

	if _, err := s.pool.Exec(ctx, `ANALYZE audit_events`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	actorFilter := actors[0]
	outcomeFilter := outcomes[3] // a distinct, non-lifecycle, non-NULL value
	edgeFilter := "agent-repo"

	cases := []struct {
		name string
		q    ListQuery
	}{
		{"unfiltered", ListQuery{Direction: Desc, Limit: 50}},
		{"preset", ListQuery{Direction: Desc, Preset: true, Limit: 50}},
		{"actor_filter", ListQuery{Direction: Desc, Actor: &actorFilter, Limit: 50}},
		{"outcome_filter", ListQuery{Direction: Desc, Outcome: &outcomeFilter, Limit: 50}},
		{"edge_filter_zero_match", ListQuery{Direction: Desc, Edge: &edgeFilter, Limit: 50}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args := tc.q.build()
			p := explainPlan(t, s.pool, "EXPLAIN "+query, args...)
			t.Logf("custom plan for %s:\n%s", tc.name, p)
			if strings.Contains(p, "Seq Scan") {
				t.Fatalf("%s: expected no Seq Scan, got plan:\n%s", tc.name, p)
			}
			if !strings.Contains(p, "Index Scan") {
				t.Fatalf("%s: expected an Index Scan, got plan:\n%s", tc.name, p)
			}
		})
	}

	// ---- Generic-plan proof (Commander review, T-105 follow-up) ----
	//
	// The cases above execute each statement for the first time on a fresh
	// connection, so pgx's per-connection statement cache and Postgres's
	// plan_cache_mode=auto both fold in the literal parameter values — a
	// CUSTOM plan. build()'s filter clauses are now appended only when a
	// filter is set (a bare `AND actor = $n`, not an OR-guard), which is what
	// makes the composite indexes reachable under ANY plan, not just a custom
	// one. This block proves that directly: force a GENERIC plan (the
	// planner must commit to a shape before seeing the bound values) via
	// PREPARE + EXPLAIN EXECUTE on one pinned connection, and assert the
	// composite index is actually named in Index Cond — not merely that a
	// Seq Scan is absent, which the pre-fix OR-guarded form also achieved
	// while still ignoring the index (it degraded to a Filter on a backward
	// PK scan instead).
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire pinned connection: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET plan_cache_mode = force_generic_plan`); err != nil {
		t.Fatalf("set plan_cache_mode: %v", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `RESET plan_cache_mode`) }()

	genericCases := []struct {
		name      string
		q         ListQuery
		wantIndex string
		// requireCompositeIndex, when true, asserts Index Cond names
		// wantIndex specifically. When false, only "no Seq Scan" is
		// asserted — see the edge case comment below for why that
		// distinction is itself a documented, diagnosed finding rather than
		// a weakened assertion.
		requireCompositeIndex bool
	}{
		{"actor_filter_generic", ListQuery{Direction: Desc, Actor: &actorFilter, Limit: 50}, "audit_events_actor_seq_idx", true},
		{"outcome_filter_generic", ListQuery{Direction: Desc, Outcome: &outcomeFilter, Limit: 50}, "audit_events_outcome_seq_idx", true},
		// edge is a DIFFERENT, honestly-diagnosed case, not a re-test of the
		// same OR-guard defect (see the long comment below).
		{"edge_filter_generic", ListQuery{Direction: Desc, Edge: &edgeFilter, Limit: 50}, "audit_events_edge_seq_idx", false},
	}

	for i, tc := range genericCases {
		t.Run(tc.name, func(t *testing.T) {
			query, args := tc.q.build()
			stmt := fmt.Sprintf("t105_generic_%d", i)
			if _, err := conn.Exec(ctx, "PREPARE "+stmt+" AS "+query); err != nil {
				t.Fatalf("prepare %s: %v", stmt, err)
			}
			defer func() { _, _ = conn.Exec(context.Background(), "DEALLOCATE "+stmt) }()

			// EXPLAIN EXECUTE's own argument list is a plain SQL utility
			// statement — pgx's extended protocol cannot infer parameter
			// types for a top-level EXECUTE's arguments (confirmed live: it
			// describes zero parameters and rejects bound args here), so the
			// values are embedded as SQL literals via sqlLiteral. This is a
			// diagnostic test probe over test-generated values (UUIDs and
			// fixed strings this test itself constructed, never external
			// input) — it has no bearing on build()'s never-interpolate
			// rule, which governs the store's actual production query path.
			literals := make([]string, len(args))
			for ai, a := range args {
				literals[ai] = sqlLiteral(a)
			}
			explainSQL := fmt.Sprintf("EXPLAIN EXECUTE %s(%s)", stmt, strings.Join(literals, ","))

			p := explainPlan(t, conn, explainSQL)
			t.Logf("FORCED GENERIC plan for %s:\n%s", tc.name, p)
			if strings.Contains(p, "Seq Scan") {
				t.Fatalf("%s: unexpected Seq Scan under a forced generic plan:\n%s", tc.name, p)
			}
			if !tc.requireCompositeIndex {
				return
			}
			if !strings.Contains(p, "Index Cond") || !strings.Contains(p, tc.wantIndex) {
				t.Fatalf("%s: expected an Index Cond naming %s under a FORCED GENERIC plan (proving the "+
					"composite index is reachable independent of custom-vs-generic planner choice), got:\n%s",
					tc.name, tc.wantIndex, p)
			}
		})
	}
}

// Honest finding, diagnosed live and recorded here rather than papered over
// (Commander review follow-up): edge_filter_generic above intentionally does
// NOT assert that the composite audit_events_edge_seq_idx is chosen — under
// a forced generic plan it demonstrably is not, and that is a property of
// Postgres's cost model interacting with edge's inherently bounded
// cardinality (the CHECK constraint caps it to 8 possible values), NOT a
// recurrence of the OR-guard defect this ticket fixes.
//
// The OR-guard fix IS proven: actor_filter_generic and outcome_filter_generic
// both show `Index Cond: (actor|outcome = $n) AND (seq < $1)` naming their
// composite index under a forced generic plan — the identical code path
// (build()'s bare `AND col = $n`, no OR-guard) that edge_filter_generic also
// exercises.
//
// What differs for edge is data distribution, confirmed by direct psql
// diagnosis against this same seeded table: for the full (wide,
// non-index-only) row projection List always selects, a generic-plan
// estimate of ~206 matching rows out of ~2000 (edge's 1-in-8 selectivity —
// bounded low, not rare, because the vocabulary itself is closed) makes
// Postgres's cost model correctly prefer walking audit_events_pkey backward
// with a Filter (cost ~103, already in seq order, no Sort, and it can stop
// at LIMIT 50 without visiting the whole match set) over a Bitmap Heap Scan
// on the composite edge index followed by an explicit Sort (cost ~235,
// because a *generic* plan's Bitmap-based index access can't rely on the
// index's own (edge, seq DESC) ordering the way a plain ordered Index Scan
// could). Forcing the planner off the PK path entirely
// (`SET enable_indexscan = off`) confirms the composite index IS reachable
// and correctly costed — it is just genuinely more expensive here. This
// mirrors the same low-cardinality-per-value tradeoff PERF-7's own written
// rationale already reasons about for actor/outcome (which is why this
// ticket deliberately raised actor cardinality to 250 and used a rare
// outcome value earlier in this file) — it is not a new problem, and it is
// not fixable by reshaping build()'s query text, because the query text is
// already the same fixed, index-friendly shape proven correct above.
// Resolving it (if ever needed) would mean changing the schema or indexing
// strategy — e.g. a partial index — a design decision ADR-0067 §14 already
// declined for a structurally similar reason (coupling the lens to the
// schema), not something to improvise here. The custom-plan case
// (edge_filter_zero_match, above) already covers the scenario PERF-7's
// written rationale actually names — a concrete, known-rare-or-zero edge
// literal — where the composite index wins decisively and is what real
// callers hit in production under plan_cache_mode=auto's default behavior.

// sqlLiteral renders v as a SQL literal for embedding directly into a
// diagnostic statement's text (see the generic-plan probe above). It is NOT
// used anywhere on the store's production query path — build() always binds
// values as $n parameters — and it only ever sees this test's own
// self-generated values (UUIDs, fixed strings, ints), never external input.
func sqlLiteral(v any) string {
	switch t := v.(type) {
	case int64:
		return strconv.FormatInt(t, 10)
	case int:
		return strconv.Itoa(t)
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	case *string:
		if t == nil {
			return "NULL"
		}
		return sqlLiteral(*t)
	case []string:
		if t == nil {
			return "NULL"
		}
		quoted := make([]string, len(t))
		for i, s := range t {
			quoted[i] = strings.ReplaceAll(s, "'", "''")
		}
		return "'{" + strings.Join(quoted, ",") + "}'"
	default:
		panic(fmt.Sprintf("sqlLiteral: unsupported type %T", v))
	}
}

// explainQuerier is the narrow slice of *pgxpool.Pool / *pgxpool.Conn that
// explainPlan needs — both expose Query with the same signature.
type explainQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// explainPlan runs an EXPLAIN-family statement and returns its text plan,
// joined by newlines. Shared by both the custom-plan and forced-generic-plan
// cases so the two prove the same thing against the same assertions.
func explainPlan(t *testing.T, q explainQuerier, sql string, args ...any) string {
	t.Helper()
	rows, err := q.Query(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("explain query %q: %v", sql, err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan explain line: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows: %v", err)
	}
	return plan.String()
}

// ---- T-124a: audit_chain_heads + Ed25519 chain-head signing (ADR-0069) ----

// walkChainFromGenesis pages the WHOLE audit_events table in ascending seq
// order and runs chain.Verify — the same recompute-from-Genesis walk the
// ADR-0069 D6 verifier's step 2 performs — returning the live chain tip only
// if every link independently recomputes. This is what lets the tests below
// claim a signed head attests the REAL chain, not merely a value copied from
// one stored row.
func walkChainFromGenesis(t *testing.T, s *Store) (headSeq int64, headChainHash string) {
	t.Helper()
	events := listAll(t, s, ListQuery{Direction: Asc, Limit: 500})
	if len(events) == 0 {
		t.Fatal("expected at least one event in the live chain")
	}
	if idx := chain.Verify(events); idx != -1 {
		t.Fatalf("live chain failed independent recomputation at index %d", idx)
	}
	last := events[len(events)-1]
	return last.Seq, last.ChainHash
}

// TestChainHeadSigningAttestsLiveChain is the NAMED G1 LIVE TEST for T-124a.
// It appends real events through the real store, independently recomputes
// the live chain tip (walkChainFromGenesis, mirroring the verifier's own
// walk), signs THAT value, round-trips it through InsertHead/LastHead, and
// proves the stored head's chainHash is exactly the value chain.Compute
// produces by walking the real chain from chain.Genesis — i.e. the head
// genuinely attests the live chain, not a mocked or assumed one.
func TestChainHeadSigningAttestsLiveChain(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	const n = 5
	for i := 0; i < n; i++ {
		if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	headSeq, headChainHash := walkChainFromGenesis(t, s)

	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	h, err := chain.SignHead(sk, headSeq, headChainHash, ts)
	if err != nil {
		t.Fatalf("sign head: %v", err)
	}
	if err := s.InsertHead(ctx, h); err != nil {
		t.Fatalf("insert head: %v", err)
	}

	got, ok, err := s.LastHead(ctx)
	if err != nil {
		t.Fatalf("last head: %v", err)
	}
	if !ok {
		t.Fatal("expected a head to exist after InsertHead")
	}
	if got.Seq != headSeq || got.ChainHash != headChainHash {
		t.Fatalf("stored head (seq=%d chainHash=%s) does not match the independently recomputed live chain tip (seq=%d chainHash=%s)",
			got.Seq, got.ChainHash, headSeq, headChainHash)
	}
	if err := chain.VerifyHead(pub, got); err != nil {
		t.Fatalf("stored head failed to verify against the signing key: %v", err)
	}
}

// TestVerifyHeadRejectsSwappedChainState proves a head's signature attests
// (seq, chainHash) as a bound pair over LIVE, store-derived chain states —
// not either field independently. A signature captured at an earlier real
// chain position must fail once its content is swapped for a later real
// position's, exactly the divergence a verifier must catch after a
// truncation or rewrite.
func TestVerifyHeadRejectsSwappedChainState(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	seqA, hashA := walkChainFromGenesis(t, s)
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	headA, err := chain.SignHead(sk, seqA, hashA, ts)
	if err != nil {
		t.Fatalf("sign head A: %v", err)
	}
	if err := chain.VerifyHead(pub, headA); err != nil {
		t.Fatalf("head A must verify against its own signing key: %v", err)
	}

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed second: %v", err)
	}
	seqB, hashB := walkChainFromGenesis(t, s)
	if seqB == seqA || hashB == hashA {
		t.Fatal("expected the live chain to have advanced between the two checkpoints")
	}

	swapped := headA
	swapped.Seq = seqB
	swapped.ChainHash = hashB
	if err := chain.VerifyHead(pub, swapped); err == nil {
		t.Fatal("expected a signature captured at an earlier live chain state to fail verification against a later one")
	}
}

// TestEventCountSinceAcrossAppend is the live test for EventCountSince: zero
// before any append past the baseline, exactly the appended count after.
func TestEventCountSinceAcrossAppend(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	headBefore, err := s.ChainHead(ctx)
	if err != nil {
		t.Fatalf("chain head: %v", err)
	}
	if n, err := s.EventCountSince(ctx, headBefore); err != nil {
		t.Fatalf("event count since (baseline): %v", err)
	} else if n != 0 {
		t.Fatalf("expected 0 events since the current head, got %d", n)
	}

	const appended = 7
	for i := 0; i < appended; i++ {
		if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	n, err := s.EventCountSince(ctx, headBefore)
	if err != nil {
		t.Fatalf("event count since (after append): %v", err)
	}
	if n != appended {
		t.Fatalf("expected %d events since headBefore, got %d", appended, n)
	}
}

// TestChainHeadsAppendOnlyTrigger proves audit_chain_heads sits behind the
// same append-only bar as audit_events: both UPDATE and DELETE are rejected
// by the trigger the migration adds.
func TestChainHeadsAppendOnlyTrigger(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seq, hash := walkChainFromGenesis(t, s)
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	h, err := chain.SignHead(sk, seq, hash, ts)
	if err != nil {
		t.Fatalf("sign head: %v", err)
	}
	if err := s.InsertHead(ctx, h); err != nil {
		t.Fatalf("insert head: %v", err)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE audit_chain_heads SET key_id = 'tampered' WHERE seq = $1`, seq); err == nil {
		t.Fatal("expected UPDATE on audit_chain_heads to be rejected by the append-only trigger")
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM audit_chain_heads WHERE seq = $1`, seq); err == nil {
		t.Fatal("expected DELETE on audit_chain_heads to be rejected by the append-only trigger")
	}

	// The row must still be exactly as inserted — neither statement partially
	// applied before the trigger raised.
	got, ok, err := s.LastHead(ctx)
	if err != nil {
		t.Fatalf("last head after rejected mutations: %v", err)
	}
	if !ok || got.Seq != seq || got.KeyID != h.KeyID {
		t.Fatalf("expected the head at seq %d to survive unmutated, got ok=%v %+v", seq, ok, got)
	}
}

// TestWithCheckpointLockSingleWriter proves WithCheckpointLock is
// non-blocking single-writer: a concurrent second caller gets
// acquired=false immediately (fn never runs) while the first holder's
// transaction is still open, rather than waiting for it to release.
func TestWithCheckpointLockSingleWriter(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	holding := make(chan struct{})
	release := make(chan struct{})
	doneFirst := make(chan struct{})

	go func() {
		defer close(doneFirst)
		acquired, err := s.WithCheckpointLock(ctx, func(CheckpointTx) error {
			close(holding)
			<-release
			return nil
		})
		if err != nil {
			t.Errorf("first checkpoint: %v", err)
		}
		if !acquired {
			t.Error("expected the first caller to acquire the checkpoint lock")
		}
	}()

	<-holding // first holder now holds the advisory lock inside a still-open tx

	ran := false
	acquired, err := s.WithCheckpointLock(ctx, func(CheckpointTx) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("second checkpoint attempt: %v", err)
	}
	if acquired {
		t.Fatal("expected the second concurrent caller to fail to acquire (acquired=false), not block")
	}
	if ran {
		t.Fatal("fn must not run for a caller that did not acquire the lock")
	}

	close(release)
	<-doneFirst
}

// ---- T-125b: HeadsSince (ADR-0069 §4 startup re-sync's catch-up read) ----

// TestHeadsSinceAscendingAfterSeq proves HeadsSince returns exactly the
// heads with seq > since, in ascending order — signing two real,
// independently-recomputed live chain heads and checking both the exact
// zero-result boundary (since == the newest head's own seq) and the
// one-result case (since == the older head's seq).
func TestHeadsSinceAscendingAfterSeq(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	seqA, hashA := walkChainFromGenesis(t, s)
	headA, err := chain.SignHead(sk, seqA, hashA, ts)
	if err != nil {
		t.Fatalf("sign head A: %v", err)
	}
	if err := s.InsertHead(ctx, headA); err != nil {
		t.Fatalf("insert head A: %v", err)
	}

	// Nothing new since head A's own seq.
	before, err := s.HeadsSince(ctx, seqA)
	if err != nil {
		t.Fatalf("heads since seqA (before B exists): %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("expected zero heads since seqA=%d itself, got %d: %+v", seqA, len(before), before)
	}

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed 2: %v", err)
	}
	seqB, hashB := walkChainFromGenesis(t, s)
	headB, err := chain.SignHead(sk, seqB, hashB, ts)
	if err != nil {
		t.Fatalf("sign head B: %v", err)
	}
	if err := s.InsertHead(ctx, headB); err != nil {
		t.Fatalf("insert head B: %v", err)
	}

	got, err := s.HeadsSince(ctx, seqA)
	if err != nil {
		t.Fatalf("heads since seqA: %v", err)
	}
	if len(got) != 1 || got[0].Seq != seqB || got[0].ChainHash != hashB {
		t.Fatalf("expected exactly head B (seq=%d hash=%s) since seqA=%d, got %+v", seqB, hashB, seqA, got)
	}

	// Ascending order holds over a wider window too (shared table across
	// this run's other tests — assert relative order, not an exact count,
	// per the T-073 dirty-DB lesson).
	all, err := s.HeadsSince(ctx, -1)
	if err != nil {
		t.Fatalf("heads since -1: %v", err)
	}
	foundA, foundB := -1, -1
	for i, h := range all {
		if h.Seq == seqA {
			foundA = i
		}
		if h.Seq == seqB {
			foundB = i
		}
	}
	if foundA == -1 || foundB == -1 || foundA >= foundB {
		t.Fatalf("expected head A before head B in ascending HeadsSince(-1) order, got foundA=%d foundB=%d (n=%d)", foundA, foundB, len(all))
	}
}

// TestCheckpointTxAtomicReadWriteAndIsolation is the FINDING 1 fix's live
// proof (Commander review of 93f3e39). It shows the read-then-write a
// checkpoint needs actually happens on ONE locked connection — ck.LastHead
// then ck.InsertHead, both via CheckpointTx, never s.pool — and that this
// has two independently necessary consequences: a concurrent second
// checkpoint fails to acquire (single-writer, already covered above), AND a
// plain read from OUTSIDE the holding transaction cannot observe the new
// head while it is still uncommitted (transactional isolation — the
// property that makes it safe for a second, non-checkpoint caller to call
// s.LastHead at any time without seeing half-finished checkpoint state).
//
// audit_chain_heads is shared and never truncated across this test binary's
// run (the same T-073 dirty-DB lesson the rest of this file already follows,
// e.g. uniqueMarker), so a prior test in this run (TestChainHeadSigning...,
// TestChainHeadsAppendOnlyTrigger) may already have inserted a head. Every
// assertion below therefore diffs against a captured BASELINE rather than
// assuming an empty table — asserting "ok == false" here would be exactly
// the dirty-DB mistake this file already documents guarding against.
//
// The blocking goroutine's release channel is closed via a deferred,
// once-guarded release() so a t.Fatal anywhere in the main test goroutine
// cannot leak a goroutine parked forever inside an open transaction — the
// bug an earlier version of this test had (Commander review confirmed the
// live run hung on exactly this).
func TestCheckpointTxAtomicReadWriteAndIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	marker := uniqueMarker(t)

	baseline, baselineOK, err := s.LastHead(ctx)
	if err != nil {
		t.Fatalf("baseline last head: %v", err)
	}

	if _, _, err := s.Append(ctx, ingestWith(marker, "service-service", "run.completed")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seq, hash := walkChainFromGenesis(t, s)
	_, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339Nano)

	insertedUncommitted := make(chan struct{})
	proceed := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(proceed) }) }
	defer release() // never leak the holding goroutine, even on t.Fatal below

	doneFirst := make(chan struct{})

	go func() {
		defer close(doneFirst)
		acquired, err := s.WithCheckpointLock(ctx, func(ck CheckpointTx) error {
			// The read half, on the locked connection: must see exactly the
			// pre-test baseline, not "empty" and not some other test's head.
			got, ok, err := ck.LastHead(ctx)
			if err != nil {
				return err
			}
			if ok != baselineOK || (ok && got.Seq != baseline.Seq) {
				t.Errorf("expected LastHead inside the checkpoint to match the baseline (ok=%v seq=%v), got ok=%v %+v",
					baselineOK, baseline.Seq, ok, got)
			}
			// The write half, on the SAME locked connection.
			h, err := chain.SignHead(sk, seq, hash, ts)
			if err != nil {
				return err
			}
			if err := ck.InsertHead(ctx, h); err != nil {
				return err
			}
			close(insertedUncommitted) // inserted, but the tx has not committed yet
			<-proceed
			return nil
		})
		if err != nil {
			t.Errorf("first checkpoint: %v", err)
		}
		if !acquired {
			t.Error("expected the first caller to acquire the checkpoint lock")
		}
	}()

	<-insertedUncommitted

	// A concurrent second checkpoint must fail to acquire and never run fn.
	ran := false
	acquired, err := s.WithCheckpointLock(ctx, func(CheckpointTx) error {
		ran = true
		return nil
	})
	if err != nil {
		t.Fatalf("second checkpoint attempt: %v", err)
	}
	if acquired || ran {
		t.Fatal("expected the second concurrent checkpoint to fail to acquire and never run fn")
	}

	// A plain read from OUTSIDE the holding transaction must still show the
	// baseline — never the new, still-uncommitted seq — proving InsertHead
	// ran on the locked tx and not on a second pool connection.
	if got, ok, err := s.LastHead(ctx); err != nil {
		t.Fatalf("last head while the first checkpoint's tx is still open: %v", err)
	} else if ok != baselineOK || (ok && got.Seq != baseline.Seq) {
		t.Fatalf("observed the new uncommitted head from outside the holding transaction (got ok=%v %+v, want baseline ok=%v %+v) — "+
			"InsertHead must have run on a different connection than LastHead's read", ok, got, baselineOK, baseline)
	}

	release()
	<-doneFirst

	// Once the first checkpoint commits, the NEW head is visible via the
	// normal pool-backed path.
	got, ok, err := s.LastHead(ctx)
	if err != nil {
		t.Fatalf("last head after commit: %v", err)
	}
	if !ok || got.Seq != seq {
		t.Fatalf("expected the committed head at seq %d to be visible after commit, got ok=%v %+v", seq, ok, got)
	}
}
