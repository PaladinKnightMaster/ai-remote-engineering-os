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
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
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
// Actor cardinality is deliberately high (250 distinct values over 2000 rows,
// ~8 rows/actor) and the outcome filter targets a deliberately rare value —
// low cardinality *per value* is exactly the PERF-7 case a composite index
// earns its keep on (a highly selective equality + ORDER BY seq LIMIT n
// cannot be satisfied cheaply by walking the PK backward with a filter, the
// way a common value can). "agent-repo" is never seeded at all, mirroring
// PERF-7's own zero-match rationale.
func TestExplainUsesIndexScan(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	const seedCount = 2000
	const actorCount = 250
	actors := make([]string, actorCount)
	for i := range actors {
		actors[i] = fmt.Sprintf("perf-actor-%d-%s", i, uuid.Must(uuid.NewV7()).String())
	}
	// agent-repo intentionally excluded from seeding — the PERF-7 zero-match case.
	edges := []string{
		"human-agent", "agent-agent", "agent-tool", "agent-workspace",
		"agent-memory", "agent-remote-session", "service-service",
	}
	regularOutcomes := []string{"run.completed", "run.failed", "plan.requested", "plan.expired", ""}
	rareOutcome := "perf-rare-outcome-" + uuid.Must(uuid.NewV7()).String()

	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for i := 0; i < seedCount; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			outcome := regularOutcomes[i%len(regularOutcomes)]
			if i%400 == 0 { // ~5 rows out of 2000 get the rare outcome
				outcome = rareOutcome
			}
			in := ingestWith(actors[i%len(actors)], edges[i%len(edges)], outcome)
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
	outcomeFilter := rareOutcome
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
			rows, err := s.pool.Query(ctx, "EXPLAIN "+query, args...)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			var plan strings.Builder
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					rows.Close()
					t.Fatalf("scan explain line: %v", err)
				}
				plan.WriteString(line)
				plan.WriteString("\n")
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				t.Fatalf("explain rows: %v", err)
			}
			p := plan.String()
			t.Logf("plan for %s:\n%s", tc.name, p)
			if strings.Contains(p, "Seq Scan") {
				t.Fatalf("%s: expected no Seq Scan, got plan:\n%s", tc.name, p)
			}
			if !strings.Contains(p, "Index Scan") {
				t.Fatalf("%s: expected an Index Scan, got plan:\n%s", tc.name, p)
			}
		})
	}
}
