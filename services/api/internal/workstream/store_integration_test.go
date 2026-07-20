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

	"github.com/google/uuid"
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

// sampleAdaptiveInput mirrors sampleInput for an adaptive charter (T-087, WS-5): zero
// template steps, a named composer, otherwise the same scope/bounds shape.
func sampleAdaptiveInput(principal, composer string) Input {
	return Input{
		Principal: principal, Label: "adaptive ops", Objective: "adapt to whatever the memory teaches",
		IntervalSeconds: 3600, Kind: KindAdaptive, Composer: composer,
		Grant: grant.Input{
			Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10, BudgetWindowSeconds: 3600,
		},
	}
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

// countTargetOutcome counts events by (target, outcome) — the plan-request tests use
// this instead of countOutcome (evidencePacketId-scoped, which is the CHARTER and so
// accumulates across every tick a charter ever fires) when they need an exactly-once
// assertion scoped to ONE plan-request row (Target = "plan-request:<id>", unique per row).
func countTargetOutcome(t *testing.T, pool *pgxpool.Pool, target, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'target' = $1 AND payload->>'outcome' = $2`,
		target, outcome).Scan(&n); err != nil {
		t.Fatalf("count target outcome: %v", err)
	}
	return n
}

// countTargetOutcomeActor narrows countTargetOutcome to one actor (the memory
// countActorForOutcome precedent) — asserts the event's actor without a separate query.
func countTargetOutcomeActor(t *testing.T, pool *pgxpool.Pool, target, outcome, actor string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'target' = $1 AND payload->>'outcome' = $2 AND payload->>'actor' = $3`,
		target, outcome, actor).Scan(&n); err != nil {
		t.Fatalf("count target outcome actor: %v", err)
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

// TestWorkstreamKindMatrix — T-087 (WS-5, ADR-0065 §4): Create validates the kind
// matrix — adaptive requires a composer and zero template steps; template requires no
// composer and at least one step; any other combination is rejected. An adaptive
// charter's auto-issued grant names the COMPOSER as grantee (R2), not workstream:<id> —
// the one behavior change to charter creation; a template charter's grantee is
// unchanged (byte-behavior-preserved). The DB CHECK is the structural backstop, not
// just store discipline.
func TestWorkstreamKindMatrix(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	// Adaptive, well-formed: composer named, no template steps, grantee = composer.
	adaptive, err := s.Create(ctx, sampleAdaptiveInput("operator:adapt", "agent:composer-1"))
	if err != nil {
		t.Fatalf("create adaptive: %v", err)
	}
	if adaptive.Kind != KindAdaptive || adaptive.Composer != "agent:composer-1" || len(adaptive.Steps) != 0 {
		t.Fatalf("unexpected adaptive charter: %+v", adaptive)
	}
	g, err := grant.NewStore(pool, "api_outbox").Get(ctx, adaptive.GrantID)
	if err != nil {
		t.Fatalf("get adaptive grant: %v", err)
	}
	if g.Agent != "agent:composer-1" {
		t.Fatalf("adaptive grantee = %q, want the named composer (R2)", g.Agent)
	}

	// Template, well-formed (the default/existing shape — behavior-preserving): no
	// composer, >=1 step, grantee stays workstream:<id>.
	tmpl, err := s.Create(ctx, sampleInput("operator:tmpl"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if tmpl.Kind != KindTemplate || tmpl.Composer != "" {
		t.Fatalf("unexpected template charter: %+v", tmpl)
	}
	g2, err := grant.NewStore(pool, "api_outbox").Get(ctx, tmpl.GrantID)
	if err != nil {
		t.Fatalf("get template grant: %v", err)
	}
	if g2.Agent != "workstream:"+tmpl.ID {
		t.Fatalf("template grantee changed: %q, want workstream:%s (byte-behavior-preserved)", g2.Agent, tmpl.ID)
	}

	// An explicit Kind: KindTemplate with no composer/>=1 step behaves exactly like the
	// "" default (both routes through the same validation).
	explicit, err := s.Create(ctx, Input{
		Principal: "operator:explicit-tmpl", Label: "x", Objective: "x", IntervalSeconds: 3600,
		Kind:  KindTemplate,
		Steps: []StepInput{{ResourceID: "res-a", Command: "uptime"}},
		Grant: sampleInput("operator:explicit-tmpl").Grant,
	})
	if err != nil || explicit.Kind != KindTemplate {
		t.Fatalf("explicit KindTemplate: %+v err=%v", explicit, err)
	}

	// Rejected combinations.
	bad := func(name string, mut func(*Input)) {
		in := sampleAdaptiveInput("operator:badkind-"+name, "agent:bad")
		mut(&in)
		if _, err := s.Create(ctx, in); err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
	}
	bad("adaptive-with-steps", func(in *Input) {
		in.Steps = []StepInput{{ResourceID: "res-a", Command: "uptime"}}
	})
	bad("adaptive-without-composer", func(in *Input) { in.Composer = "" })
	bad("template-with-composer", func(in *Input) {
		in.Kind = KindTemplate
		in.Steps = []StepInput{{ResourceID: "res-a", Command: "uptime"}}
		// in.Composer stays set from sampleAdaptiveInput — a template must reject it.
	})
	bad("template-without-steps", func(in *Input) {
		in.Kind = KindTemplate
		in.Composer = ""
		in.Steps = nil
	})
	bad("unknown-kind", func(in *Input) { in.Kind = "bogus" })

	// The DB CHECK rejects a bad row even bypassing the store entirely.
	if _, err := pool.Exec(ctx, `
		INSERT INTO workstreams (id, principal, label, objective, grant_id, interval_seconds, next_fire_at, kind, composer)
		VALUES (gen_random_uuid(), 'operator:raw', '', 'x', $1, 60, now(), 'adaptive', '')`, tmpl.GrantID); err == nil {
		t.Fatal("DB CHECK must reject an adaptive row with an empty composer")
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workstreams (id, principal, label, objective, grant_id, interval_seconds, next_fire_at, kind, composer)
		VALUES (gen_random_uuid(), 'operator:raw2', '', 'x', $1, 60, now(), 'bogus', '')`, tmpl.GrantID); err == nil {
		t.Fatal("DB CHECK must reject an unknown kind")
	}
}

// TestPlanRequestLifecycle — T-087/T-089 (PLAN-1, D2): OpenPlanRequest is idempotent —
// a retried launch_key, or a second open attempt while the charter already has one
// open, both return the SAME row rather than minting a second one (launch_key UNIQUE
// backstopped by the structural plan_requests_one_open_idx, C2) AND chain NO duplicate
// plan.requested (reattached=true on both no-op paths); a genuine open chains EXACTLY
// ONE plan.requested, actor = the charter itself; MarkPlanRequestSubmitted flips
// open→submitted exactly once (a second call is ok=false); GetPlanRequest round-trips;
// and a fresh open (after the prior request left 'open') is a genuine new row + a
// second plan.requested.
func TestPlanRequestLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	ws, err := s.Create(ctx, sampleAdaptiveInput("operator:plan", "agent:composer-plan"))
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour)
	wsActor := "workstream:" + ws.ID

	first, reattached, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:1", expiresAt)
	if err != nil || reattached {
		t.Fatalf("open: reattached=%v err=%v", reattached, err)
	}
	if first.Status != PlanRequestOpen || first.WorkstreamID != ws.ID || first.RunID != "" {
		t.Fatalf("unexpected opened request: %+v", first)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+first.ID, "plan.requested"); n != 1 {
		t.Fatalf("plan.requested count = %d, want 1", n)
	}
	if n := countTargetOutcomeActor(t, pool, "plan-request:"+first.ID, "plan.requested", wsActor); n != 1 {
		t.Fatalf("plan.requested actor should be the charter itself (count=%d)", n)
	}

	// A retry of the SAME launch_key is a no-op re-attach (launch_key UNIQUE) — emits
	// NOTHING (no duplicate plan.requested).
	retry, reattached, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:1", expiresAt)
	if err != nil || !reattached {
		t.Fatalf("retry open: reattached=%v err=%v, want reattached=true", reattached, err)
	}
	if retry.ID != first.ID {
		t.Fatalf("retry minted a second row: %q != %q", retry.ID, first.ID)
	}

	// A DIFFERENT launch_key while one is still open hits the structural
	// one-open-per-charter index — also a no-op re-attach to the SAME open row, also
	// event-silent.
	again, reattached, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:2", expiresAt)
	if err != nil || !reattached {
		t.Fatalf("second-key open: reattached=%v err=%v, want reattached=true", reattached, err)
	}
	if again.ID != first.ID {
		t.Fatalf("a second open-tick minted a new row while one was open: %q != %q", again.ID, first.ID)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+first.ID, "plan.requested"); n != 1 {
		t.Fatalf("plan.requested count after two duplicate opens = %d, want still 1 (no dup events)", n)
	}

	got, err := s.GetPlanRequest(ctx, first.ID)
	if err != nil || got.Status != PlanRequestOpen {
		t.Fatalf("get: %+v err=%v", got, err)
	}
	if _, err := s.GetPlanRequest(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrPlanRequestNotFound) {
		t.Fatalf("get unknown: want ErrPlanRequestNotFound, got %v", err)
	}

	// MarkPlanRequestSubmitted flips exactly once and chains ONE plan.submitted, actor
	// = the composing agent (R2) — not the charter.
	runID := uuid.Must(uuid.NewV7()).String()
	composer := "agent:composer-plan"
	if ok, err := s.MarkPlanRequestSubmitted(ctx, first.ID, runID, composer); err != nil || !ok {
		t.Fatalf("mark submitted: ok=%v err=%v", ok, err)
	}
	got, err = s.GetPlanRequest(ctx, first.ID)
	if err != nil || got.Status != PlanRequestSubmitted || got.RunID != runID {
		t.Fatalf("after submit: %+v err=%v", got, err)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+first.ID, "plan.submitted"); n != 1 {
		t.Fatalf("plan.submitted count = %d, want 1", n)
	}
	if n := countTargetOutcomeActor(t, pool, "plan-request:"+first.ID, "plan.submitted", composer); n != 1 {
		t.Fatalf("plan.submitted actor should be the composing agent (count=%d)", n)
	}

	// A double-submit is a no-op AND emits nothing further (no duplicate plan.submitted).
	if ok, err := s.MarkPlanRequestSubmitted(ctx, first.ID, runID, composer); err != nil || ok {
		t.Fatalf("double-submit must be a no-op: ok=%v err=%v", ok, err)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+first.ID, "plan.submitted"); n != 1 {
		t.Fatalf("plan.submitted count after double-submit = %d, want still 1 (no dup event)", n)
	}

	// Now that the prior request is terminal, a fresh open is a genuine new row (the
	// one-open invariant only blocks a SECOND open one, not a new one after the first
	// left 'open') — and chains its OWN plan.requested.
	fresh, reattached, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:3", expiresAt)
	if err != nil || reattached {
		t.Fatalf("open after submit: reattached=%v err=%v", reattached, err)
	}
	if fresh.ID == first.ID {
		t.Fatal("expected a fresh row once the prior request left open")
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+fresh.ID, "plan.requested"); n != 1 {
		t.Fatalf("fresh request's plan.requested count = %d, want 1", n)
	}
	// The charter-scoped total is now 2 (first's genuine open + fresh's) — the
	// duplicate-open retries above contributed nothing.
	if n := countOutcome(t, pool, wsActor, "plan.requested"); n != 2 {
		t.Fatalf("charter-scoped plan.requested total = %d, want 2 (2 genuine opens, 0 from duplicates)", n)
	}
}

// TestPollOpenPlanRequests — D1/SEC-32 (ADR-0065 §5, T-090): the composer's poll sees
// exactly its OWN charter's open request, with the composition context (objective +
// the bounding grant's resource/verb scope + step bound + TTL); a SECOND composer with
// its own charter+grant sees NOTHING of the first's (cross-grant containment, the N1
// live negative); an unknown/foreign workstreamId filter answers empty, not an error
// (no oracle); after the charter's grant is revoked, poll answers empty too (N3).
func TestPollOpenPlanRequests(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	gs := grant.NewStore(pool, "api_outbox")

	// Unique-suffix composer subjects (the T-073 dirty-DB lesson): a fixed literal
	// subject would accumulate rows across re-runs against an already-migrated,
	// never-truncated DB.
	suffix := uuid.Must(uuid.NewV7()).String()
	composerA, composerB := "agent:poll-composer-a-"+suffix, "agent:poll-composer-b-"+suffix

	wsA, err := s.Create(ctx, sampleAdaptiveInput("operator:poll-a-"+suffix, composerA))
	if err != nil {
		t.Fatalf("create charter A: %v", err)
	}
	wsB, err := s.Create(ctx, sampleAdaptiveInput("operator:poll-b-"+suffix, composerB))
	if err != nil {
		t.Fatalf("create charter B: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour)
	reqA, _, err := s.OpenPlanRequest(ctx, wsA.ID, "workstream:"+wsA.ID+":tick:1", expiresAt)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	if _, _, err := s.OpenPlanRequest(ctx, wsB.ID, "workstream:"+wsB.ID+":tick:1", expiresAt); err != nil {
		t.Fatalf("open B: %v", err)
	}

	// Composer A sees exactly its own request, with composition context.
	gotA, err := s.PollOpenPlanRequests(ctx, composerA, "")
	if err != nil {
		t.Fatalf("poll A: %v", err)
	}
	if len(gotA) != 1 || gotA[0].RequestID != reqA.ID {
		t.Fatalf("poll A = %+v, want exactly [reqA]", gotA)
	}
	if gotA[0].WorkstreamID != wsA.ID || gotA[0].Objective != "adapt to whatever the memory teaches" {
		t.Fatalf("poll A composition context wrong: %+v", gotA[0])
	}
	if len(gotA[0].Resources) != 2 || len(gotA[0].Verbs) != 1 || gotA[0].MaxStepsPerRun != 5 {
		t.Fatalf("poll A grant-scope context wrong: %+v", gotA[0])
	}

	// Cross-grant containment (N1, SEC-32): composer A never sees B's open request, and
	// vice versa.
	for _, r := range gotA {
		if r.WorkstreamID == wsB.ID {
			t.Fatal("composer A's poll leaked charter B's request")
		}
	}
	gotB, err := s.PollOpenPlanRequests(ctx, composerB, "")
	if err != nil {
		t.Fatalf("poll B: %v", err)
	}
	if len(gotB) != 1 || gotB[0].WorkstreamID != wsB.ID {
		t.Fatalf("poll B = %+v, want exactly charter B's request", gotB)
	}

	// A foreign workstreamId filter (A polling scoped to B's charter) answers empty —
	// no oracle, never an error.
	filtered, err := s.PollOpenPlanRequests(ctx, composerA, wsB.ID)
	if err != nil || len(filtered) != 0 {
		t.Fatalf("poll A filtered to B's charter: rows=%d err=%v, want 0 rows no error", len(filtered), err)
	}
	// An unknown workstreamId likewise answers empty.
	unknown, err := s.PollOpenPlanRequests(ctx, composerA, "00000000-0000-0000-0000-000000000000")
	if err != nil || len(unknown) != 0 {
		t.Fatalf("poll A filtered to an unknown id: rows=%d err=%v, want 0 rows no error", len(unknown), err)
	}
	// The matching filter still returns the row.
	same, err := s.PollOpenPlanRequests(ctx, composerA, wsA.ID)
	if err != nil || len(same) != 1 {
		t.Fatalf("poll A filtered to its own charter: rows=%d err=%v, want 1", len(same), err)
	}

	// N3: once the charter's grant is revoked, poll answers empty (the composer's
	// standing authority is gone — DUR-10 §7 cascade).
	if _, err := gs.Revoke(ctx, wsA.GrantID); err != nil {
		t.Fatalf("revoke grant A: %v", err)
	}
	afterRevoke, err := s.PollOpenPlanRequests(ctx, composerA, "")
	if err != nil || len(afterRevoke) != 0 {
		t.Fatalf("poll A after grant revoke: rows=%d err=%v, want 0", len(afterRevoke), err)
	}
}

// TestExpirePlanRequestsDue — T-087/T-089 (DUR-10/PLAN-1, D2): a past-due OPEN request
// expires exactly once (bounded batch, FOR UPDATE SKIP LOCKED), chaining ONE
// plan.expired (actor = the row's own charter) in the SAME transaction as the flip; a
// second sweep touches nothing already expired (chains no second event, N4); a request
// within its TTL is untouched — the charter never wedges.
func TestExpirePlanRequestsDue(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	ws, err := s.Create(ctx, sampleAdaptiveInput("operator:expire", "agent:composer-expire"))
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}
	pr, _, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:expire", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Backdate the TTL to simulate an unanswered request past its deadline.
	if _, err := pool.Exec(ctx, `UPDATE plan_requests SET expires_at = now() - interval '1 minute' WHERE id = $1`, pr.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := s.ExpirePlanRequestsDue(ctx, 100)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired count = %d, want 1", n)
	}
	got, err := s.GetPlanRequest(ctx, pr.ID)
	if err != nil || got.Status != PlanRequestExpired {
		t.Fatalf("after expire: %+v err=%v", got, err)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+pr.ID, "plan.expired"); n != 1 {
		t.Fatalf("plan.expired count = %d, want 1", n)
	}
	if n := countTargetOutcomeActor(t, pool, "plan-request:"+pr.ID, "plan.expired", "workstream:"+ws.ID); n != 1 {
		t.Fatalf("plan.expired actor should be the row's own charter (count=%d)", n)
	}

	// A second sweep expires nothing more (exactly-once, by the flip) — and chains no
	// second plan.expired.
	if n, err := s.ExpirePlanRequestsDue(ctx, 100); err != nil || n != 0 {
		t.Fatalf("second sweep: n=%d err=%v, want 0", n, err)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+pr.ID, "plan.expired"); n != 1 {
		t.Fatalf("plan.expired count after second sweep = %d, want still 1", n)
	}

	// A fresh request opens fine now that the charter's slot is free again — and a
	// not-yet-due request is untouched by a sweep.
	fresh, _, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:notdue", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	if n, err := s.ExpirePlanRequestsDue(ctx, 100); err != nil || n != 0 {
		t.Fatalf("sweep must not touch a not-yet-due request: n=%d err=%v", n, err)
	}
	if got, err := s.GetPlanRequest(ctx, fresh.ID); err != nil || got.Status != PlanRequestOpen {
		t.Fatalf("not-yet-due request status = %+v err=%v, want open", got, err)
	}
}

// TestClosePlanRequests — T-087/T-089 (DUR-10 §7, R3): ClosePlanRequestsForWorkstream
// and ClosePlanRequestsForGrant each close a charter's open request (open→closed), are
// no-ops when nothing is open, ClosePlanRequestsForGrant reaches its charter via the
// grant_id join (a plan_requests row carries no grant_id of its own), and BOTH are
// event-silent (D2: no plan.closed — the pinned AUD-20 vocabulary has none; only the
// row's status changes, no new api_outbox row appears for its target).
func TestClosePlanRequests(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	ws, err := s.Create(ctx, sampleAdaptiveInput("operator:close", "agent:composer-close"))
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}
	pr, _, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:close", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	beforeClose := countOutcome(t, pool, "workstream:"+ws.ID, "plan.requested") // baseline: 1

	if n, err := s.ClosePlanRequestsForWorkstream(ctx, ws.ID); err != nil || n != 1 {
		t.Fatalf("close for workstream: n=%d err=%v, want 1", n, err)
	}
	got, err := s.GetPlanRequest(ctx, pr.ID)
	if err != nil || got.Status != PlanRequestClosed {
		t.Fatalf("after close: %+v err=%v", got, err)
	}
	// Event-silent: closing minted no new api_outbox row for this charter (the
	// plan.requested baseline count is unchanged — no plan.closed, no anything else).
	if n := countOutcome(t, pool, "workstream:"+ws.ID, "plan.requested"); n != beforeClose {
		t.Fatalf("close must not chain any event: plan.requested count changed %d -> %d", beforeClose, n)
	}
	// No-op when nothing is open.
	if n, err := s.ClosePlanRequestsForWorkstream(ctx, ws.ID); err != nil || n != 0 {
		t.Fatalf("re-close: n=%d err=%v, want 0", n, err)
	}

	// ClosePlanRequestsForGrant closes via the workstream's grant_id join — also
	// event-silent.
	ws2, err := s.Create(ctx, sampleAdaptiveInput("operator:closegrant", "agent:composer-closegrant"))
	if err != nil {
		t.Fatalf("create adaptive charter 2: %v", err)
	}
	pr2, _, err := s.OpenPlanRequest(ctx, ws2.ID, "workstream:"+ws2.ID+":tick:closegrant", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	if n, err := s.ClosePlanRequestsForGrant(ctx, ws2.GrantID); err != nil || n != 1 {
		t.Fatalf("close for grant: n=%d err=%v, want 1", n, err)
	}
	got2, err := s.GetPlanRequest(ctx, pr2.ID)
	if err != nil || got2.Status != PlanRequestClosed {
		t.Fatalf("after close-for-grant: %+v err=%v", got2, err)
	}
	if n := countOutcome(t, pool, "workstream:"+ws2.ID, "plan.requested"); n != 1 {
		t.Fatalf("close-for-grant must not chain any event: plan.requested count = %d, want still 1", n)
	}
}

// TestRetireClosesOpenPlanRequestAtomically — DUR-10 §7/R3 point 2 (T-089): Retire
// folds the plan-request close into its OWN transaction alongside the charter flip and
// the grant revoke — the same-tx proof is that all THREE post-conditions (charter
// retired, grant revoked, request closed) are visible together once Retire returns
// ok=true, with NO plan.closed event (event-silent by design).
func TestRetireClosesOpenPlanRequestAtomically(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	ws, err := s.Create(ctx, sampleAdaptiveInput("operator:retire-plan", "agent:composer-retire-plan"))
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}
	pr, _, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:retire", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if ok, err := s.Retire(ctx, ws.ID); err != nil || !ok {
		t.Fatalf("retire: ok=%v err=%v", ok, err)
	}

	wsGot, err := s.Get(ctx, ws.ID)
	if err != nil || wsGot.Status != StatusRetired {
		t.Fatalf("charter status after retire: %+v err=%v, want retired", wsGot, err)
	}
	g, err := grant.NewStore(pool, "api_outbox").Get(ctx, ws.GrantID)
	if err != nil || g.Status != grant.StatusRevoked {
		t.Fatalf("charter grant after retire: status=%q err=%v, want revoked", g.Status, err)
	}
	prGot, err := s.GetPlanRequest(ctx, pr.ID)
	if err != nil || prGot.Status != PlanRequestClosed {
		t.Fatalf("plan request after retire: %+v err=%v, want closed", prGot, err)
	}
	if n := countTargetOutcome(t, pool, "plan-request:"+pr.ID, "plan.closed"); n != 0 {
		t.Fatalf("plan.closed must never be chained (event-silent), got %d", n)
	}
}

// TestSelfRetireClosesOpenPlanRequestAtomically — DUR-10 §7/R3 point 3 (T-089): the
// adaptive ticker's dead-grant path reaches SelfRetire, which now folds the
// plan-request close into its OWN tx too — proven directly at the store level (no
// separate close-wiring exists in the ticker; SelfRetire covers it).
func TestSelfRetireClosesOpenPlanRequestAtomically(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	ws, err := s.Create(ctx, sampleAdaptiveInput("operator:selfretire-plan", "agent:composer-selfretire-plan"))
	if err != nil {
		t.Fatalf("create adaptive charter: %v", err)
	}
	pr, _, err := s.OpenPlanRequest(ctx, ws.ID, "workstream:"+ws.ID+":tick:selfretire", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if ok, err := s.SelfRetire(ctx, ws.ID); err != nil || !ok {
		t.Fatalf("self-retire: ok=%v err=%v", ok, err)
	}

	wsGot, err := s.Get(ctx, ws.ID)
	if err != nil || wsGot.Status != StatusRetired {
		t.Fatalf("charter status after self-retire: %+v err=%v, want retired", wsGot, err)
	}
	prGot, err := s.GetPlanRequest(ctx, pr.ID)
	if err != nil || prGot.Status != PlanRequestClosed {
		t.Fatalf("plan request after self-retire: %+v err=%v, want closed", prGot, err)
	}
}
