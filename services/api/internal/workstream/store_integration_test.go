//go:build integration

// Integration tests for the chartered-Workstream store against a real Postgres
// (T-071 G1).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/workstream/...
package workstream

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/grant"
)

// Interface conformance: the real grant store satisfies GrantWriter (compile-time).
var _ GrantWriter = (*grant.Store)(nil)

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
	// The real grant store is the GrantWriter (WS-2: one issuance+revoke path, SEC-30).
	return NewStore(pool, nil, grant.NewStore(pool, "api_outbox"), "api_outbox"), pool
}

func sampleInput(principal string) Input {
	return Input{
		Principal: principal, Label: "nightly health", Objective: "keep the fleet healthy",
		IntervalSeconds: 3600,
		Steps: []StepInput{
			{ResourceID: "res-a", Command: "uptime"},
			{ResourceID: "res-b", Command: "df -h"},
		},
		Grant: grant.Input{
			Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10, BudgetWindowSeconds: 3600,
		},
	}
}

// TestWorkstreamCharterLifecycle — the T-071 G1 live-PG proof: Create charters the
// workstream + its sealed template + the auto-issued grant (grantee = workstream:<id>,
// principal bound server-side) in ONE transaction, with workstream.chartered +
// grant.issued both enqueued from it (WS-2/AUD-18); first fire = charter + interval;
// Get round-trips the template; ListForOperator is owner-scoped and template-free.
func TestWorkstreamCharterLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	before := time.Now()
	created, err := s.Create(ctx, sampleInput("operator:alice"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != StatusActive || created.GrantID == "" {
		t.Fatalf("unexpected created: %+v", created)
	}

	// First fire = charter + interval (no immediate fire, ADR-0063 §5).
	wantFire := before.Add(time.Duration(created.IntervalSeconds) * time.Second)
	if created.NextFireAt.Before(wantFire.Add(-5*time.Second)) || created.NextFireAt.After(wantFire.Add(time.Minute)) {
		t.Fatalf("next_fire_at = %v, want ≈ charter+%ds", created.NextFireAt, created.IntervalSeconds)
	}

	// The auto-issued grant: grantee = workstream:<id>, principal = the charterer,
	// window carried (WS-4).
	g, err := grant.NewStore(pool, "api_outbox").Get(ctx, created.GrantID)
	if err != nil {
		t.Fatalf("get charter grant: %v", err)
	}
	if g.Agent != "workstream:"+created.ID || g.Principal != "operator:alice" {
		t.Fatalf("grant binding wrong: agent=%q principal=%q", g.Agent, g.Principal)
	}
	if g.BudgetWindowSeconds != 3600 || g.Status != grant.StatusActive {
		t.Fatalf("grant window/status wrong: %+v", g)
	}

	// Both consent events, from the one tx (WS-2/AUD-18).
	if n := countOutcome(t, pool, "workstream:"+created.ID, "workstream.chartered"); n != 1 {
		t.Fatalf("workstream.chartered count = %d, want 1", n)
	}
	if n := countOutcome(t, pool, "grant:"+created.GrantID, "grant.issued"); n != 1 {
		t.Fatalf("grant.issued count = %d, want 1", n)
	}

	// Get round-trips the (sealed-at-rest) template in order.
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Steps) != 2 || got.Steps[0].Command != "uptime" || got.Steps[1].ResourceID != "res-b" {
		t.Fatalf("template round-trip wrong: %+v", got.Steps)
	}
	if got.Objective != "keep the fleet healthy" || got.GrantID != created.GrantID {
		t.Fatalf("get mismatch: %+v", got)
	}

	// ListForOperator: owner-scoped, template-free.
	mine, _, err := s.ListForOperator(ctx, "operator:alice", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsWorkstream(mine, created.ID) {
		t.Fatalf("created workstream %s not in alice's list", created.ID)
	}
	for _, w := range mine {
		if len(w.Steps) != 0 {
			t.Fatal("list must not carry templates")
		}
	}
	others, _, err := s.ListForOperator(ctx, "operator:bob", "", 100)
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if containsWorkstream(others, created.ID) {
		t.Fatal("alice's workstream leaked into bob's owner-scoped list")
	}

	// Unknown id → ErrNotFound.
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}
}

// TestWorkstreamCharterAtomicity — a failing grant issuance rolls the whole charter back
// (WS-2): no workstream row, no template, no events. The failure is forced through the
// SHARED validator (an expired grant TTL rejected inside CreateInTx).
func TestWorkstreamCharterAtomicity(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	in := sampleInput("operator:atomic")
	in.Grant.ExpiresAt = time.Now().Add(-time.Minute) // CreateInTx rejects → tx must roll back
	if _, err := s.Create(ctx, in); err == nil {
		t.Fatal("want charter failure on a bad grant, got nil")
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workstreams WHERE principal = 'operator:atomic'`).Scan(&n); err != nil {
		t.Fatalf("count workstreams: %v", err)
	}
	if n != 0 {
		t.Fatalf("charter row survived a failed grant issuance: %d rows", n)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'actor' = 'operator:atomic' AND payload->>'outcome' = 'workstream.chartered'`).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("workstream.chartered leaked from a rolled-back charter: %d events", n)
	}
}

// TestWorkstreamCreateValidation — Create rejects a malformed charter (no objective, no
// steps, a bad interval, an incomplete step), fail-loud, no partial row.
func TestWorkstreamCreateValidation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	bad := func(name string, mut func(*Input)) {
		in := sampleInput("operator:val")
		mut(&in)
		if _, err := s.Create(ctx, in); err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
	}
	bad("no objective", func(in *Input) { in.Objective = "" })
	bad("no steps", func(in *Input) { in.Steps = nil })
	bad("zero interval", func(in *Input) { in.IntervalSeconds = 0 })
	bad("step without command", func(in *Input) { in.Steps[0].Command = "" })
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

func containsWorkstream(items []Workstream, id string) bool {
	for _, w := range items {
		if w.ID == id {
			return true
		}
	}
	return false
}

// TestWorkstreamTickClaim — the T-073 G1 live-PG proof of the schedule mechanics (WS-3/
// DUR-8): a future charter is not due; a due one is scanned with its exact fire time;
// the CAS claim is single-winner (a second claim on the same fire time loses); the
// advance is FROM NOW (no backfill — a long-overdue charter's next fire is now+interval,
// not old+interval); paused charters are invisible to the scan.
func TestWorkstreamTickClaim(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleInput("operator:tick"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	inDue := func() (DueRef, bool) {
		due, err := s.DueWorkstreams(ctx, 1000)
		if err != nil {
			t.Fatalf("due scan: %v", err)
		}
		for _, d := range due {
			if d.ID == created.ID {
				return d, true
			}
		}
		return DueRef{}, false
	}

	// Fresh charter: first fire = charter + interval → not yet due.
	if _, due := inDue(); due {
		t.Fatal("a fresh charter must not be due before its first interval")
	}

	// Make it long-overdue (simulating downtime) → due, with the exact fire time.
	if _, err := pool.Exec(ctx, `
		UPDATE workstreams SET next_fire_at = now() - interval '2 days' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	d, due := inDue()
	if !due {
		t.Fatal("backdated charter must be due")
	}

	// CAS claim: first wins, second (same fire time) loses — single-winner.
	if ok, err := s.ClaimTick(ctx, created.ID, d.NextFireAt); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if ok, _ := s.ClaimTick(ctx, created.ID, d.NextFireAt); ok {
		t.Fatal("second claim on the same fire time must lose the CAS")
	}

	// No backfill: the advance is from NOW (≈ now+interval), not from the 2-day-old
	// fire time — missed ticks collapsed to the one claim.
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wantMin := time.Now().Add(time.Duration(created.IntervalSeconds)*time.Second - time.Minute)
	if got.NextFireAt.Before(wantMin) {
		t.Fatalf("next_fire_at = %v — backfilled? want ≈ now+%ds", got.NextFireAt, created.IntervalSeconds)
	}
	if _, due := inDue(); due {
		t.Fatal("a claimed charter must not still be due")
	}

	// A paused charter is invisible to the scan even when overdue.
	if _, err := pool.Exec(ctx, `
		UPDATE workstreams SET status = 'paused', next_fire_at = now() - interval '1 hour' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, due := inDue(); due {
		t.Fatal("a paused charter must not be due")
	}
}

// TestWorkstreamSelfRetire — F1 (WS-3): SelfRetire flips an active charter to retired
// and chains workstream.retired:grant-dead (actor = the workstream — the machine
// transition), exactly once; a second call is a no-op (already retired).
func TestWorkstreamSelfRetire(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleInput("operator:sr"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ok, err := s.SelfRetire(ctx, created.ID); err != nil || !ok {
		t.Fatalf("self-retire: ok=%v err=%v", ok, err)
	}
	got, err := s.Get(ctx, created.ID)
	if err != nil || got.Status != StatusRetired {
		t.Fatalf("status after self-retire = %q err=%v, want retired", got.Status, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = 'workstream.retired:grant-dead'
		   AND payload->>'actor' = $2`,
		"workstream:"+created.ID, "workstream:"+created.ID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 1 {
		t.Fatalf("workstream.retired:grant-dead count = %d, want 1", n)
	}
	if ok, _ := s.SelfRetire(ctx, created.ID); ok {
		t.Fatal("self-retiring an already-retired charter must be a no-op")
	}
}

// TestWorkstreamLifecycleVerbs — the T-074 G1 live-PG proof (WS-5): the guarded
// transition matrix (each verb's ok/409-no-op), the three lifecycle events chained once
// each, resume's re-arm = now + interval (never the stale pre-pause fire time), and
// retire's ONE-TX charter-flip + grant-revoke (with the grant.revoked event).
func TestWorkstreamLifecycleVerbs(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleInput("operator:verbs"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	events := func(outcome string) int {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM api_outbox
			 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
			"workstream:"+created.ID, outcome).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", outcome, err)
		}
		return n
	}

	// Resume on an active charter → no-op 409-class.
	if ok, _ := s.Resume(ctx, created.ID); ok {
		t.Fatal("resuming an active charter must be a no-op")
	}

	// Pause: active → paused + event; a second pause is a no-op.
	if ok, err := s.Pause(ctx, created.ID); err != nil || !ok {
		t.Fatalf("pause: ok=%v err=%v", ok, err)
	}
	if got, _ := s.Get(ctx, created.ID); got.Status != StatusPaused {
		t.Fatalf("status after pause = %q", got.Status)
	}
	if events("workstream.paused") != 1 {
		t.Fatal("workstream.paused not chained exactly once")
	}
	if ok, _ := s.Pause(ctx, created.ID); ok {
		t.Fatal("pausing a paused charter must be a no-op")
	}

	// Make the paused fire time stale, then resume: the re-arm must be now+interval —
	// never the stale past time (no catch-up fire).
	if _, err := pool.Exec(ctx, `
		UPDATE workstreams SET next_fire_at = now() - interval '1 day' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("stale fire time: %v", err)
	}
	if ok, err := s.Resume(ctx, created.ID); err != nil || !ok {
		t.Fatalf("resume: ok=%v err=%v", ok, err)
	}
	got, _ := s.Get(ctx, created.ID)
	if got.Status != StatusActive {
		t.Fatalf("status after resume = %q", got.Status)
	}
	wantMin := time.Now().Add(time.Duration(created.IntervalSeconds)*time.Second - time.Minute)
	if got.NextFireAt.Before(wantMin) {
		t.Fatalf("resume re-arm = %v — catch-up fire? want ≈ now+%ds", got.NextFireAt, created.IntervalSeconds)
	}
	if events("workstream.resumed") != 1 {
		t.Fatal("workstream.resumed not chained exactly once")
	}

	// Retire from PAUSED (pause is not a shield): pause again, then retire — one tx flips
	// the charter AND revokes the grant, chaining workstream.retired + grant.revoked.
	if ok, _ := s.Pause(ctx, created.ID); !ok {
		t.Fatal("re-pause failed")
	}
	if ok, err := s.Retire(ctx, created.ID); err != nil || !ok {
		t.Fatalf("retire: ok=%v err=%v", ok, err)
	}
	if got, _ := s.Get(ctx, created.ID); got.Status != StatusRetired {
		t.Fatalf("status after retire = %q", got.Status)
	}
	g, err := grant.NewStore(pool, "api_outbox").Get(ctx, created.GrantID)
	if err != nil || g.Status != grant.StatusRevoked {
		t.Fatalf("charter grant after retire: status=%q err=%v, want revoked", g.Status, err)
	}
	if events("workstream.retired") != 1 {
		t.Fatal("workstream.retired not chained exactly once")
	}
	var revoked int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = 'grant.revoked'`,
		"grant:"+created.GrantID).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("grant.revoked count = %d err=%v, want 1", revoked, err)
	}

	// Every verb on a retired charter is a no-op.
	for name, verb := range map[string]func(context.Context, string) (bool, error){
		"pause": s.Pause, "resume": s.Resume, "retire": s.Retire, "self-retire": s.SelfRetire,
	} {
		if ok, _ := verb(ctx, created.ID); ok {
			t.Fatalf("%s on a retired charter must be a no-op", name)
		}
	}
}

// TestWorkstreamRetireToleratesDeadGrant — a direct grant revoke raced ahead (the T-073
// window): retire still succeeds — the charter flips and workstream.retired chains, with
// NO duplicate grant.revoked.
func TestWorkstreamRetireToleratesDeadGrant(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleInput("operator:race"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gs := grant.NewStore(pool, "api_outbox")
	if ok, err := gs.Revoke(ctx, created.GrantID); err != nil || !ok {
		t.Fatalf("direct revoke: ok=%v err=%v", ok, err)
	}
	if ok, err := s.Retire(ctx, created.ID); err != nil || !ok {
		t.Fatalf("retire after direct revoke: ok=%v err=%v", ok, err)
	}
	if got, _ := s.Get(ctx, created.ID); got.Status != StatusRetired {
		t.Fatalf("status = %q, want retired", got.Status)
	}
	var revoked int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = 'grant.revoked'`,
		"grant:"+created.GrantID).Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("grant.revoked count = %d err=%v, want exactly 1 (no duplicate)", revoked, err)
	}
}
