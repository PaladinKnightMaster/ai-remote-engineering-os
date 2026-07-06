//go:build integration

// Integration tests for the Run store against a real Postgres.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/run/...
package run

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

func testStore(t *testing.T) *Store {
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
	return NewStore(pool, nil, "api_outbox") // nil sealer → plaintext; Create enqueues run.launched into api_outbox
}

// TestRunStoreCreateGetList — the T-054 G1 live-PG proof: a Run + its ordered steps
// persist in one transaction; Get returns the run with its steps in execution order
// (commands round-tripped through the BYTEA column, defaults set); List is run-level
// (no steps); an unknown id is ErrNotFound; deleting the run cascades its steps.
func TestRunStoreCreateGetList(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	in := Input{
		Operator: "operator:admin",
		Steps: []StepInput{
			{ResourceID: "res-a", Command: "uname -a"},
			{ResourceID: "res-b", Command: "df -h"},
			{ResourceID: "res-c", Command: "echo RUN-OK"},
		},
	}
	created, err := s.Create(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != StatusPending || len(created.Steps) != 3 {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Operator != "operator:admin" || got.Status != StatusPending {
		t.Fatalf("get run mismatch: %+v", got)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("want 3 steps, got %d", len(got.Steps))
	}
	wantCmd := []string{"uname -a", "df -h", "echo RUN-OK"}
	for i, st := range got.Steps {
		if st.Index != i {
			t.Fatalf("step out of order at %d: index=%d", i, st.Index)
		}
		if st.Command != wantCmd[i] {
			t.Fatalf("step %d command: got %q want %q", i, st.Command, wantCmd[i])
		}
		if st.Status != StepPending || st.InvocationID != "" || st.Outcome != "" {
			t.Fatalf("step %d defaults wrong: %+v", i, st)
		}
	}

	// List is run-level (no steps populated) and includes the created run.
	runs, _, err := s.ListForOperator(ctx, "operator:admin", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, r := range runs {
		if r.ID == created.ID {
			found = true
			if len(r.Steps) != 0 {
				t.Fatalf("list should not populate steps, got %d", len(r.Steps))
			}
		}
	}
	if !found {
		t.Fatalf("created run %s not in list", created.ID)
	}

	// Unknown id → ErrNotFound.
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}

	// FK cascade: deleting the run removes its steps.
	var stepCount int
	if _, err := s.pool.Exec(ctx, `DELETE FROM runs WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("delete run: %v", err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM run_steps WHERE run_id = $1`, created.ID).Scan(&stepCount); err != nil {
		t.Fatalf("count after delete: %v", err)
	}
	if stepCount != 0 {
		t.Fatalf("cascade failed: %d steps remain", stepCount)
	}
}

// TestRunStoreCreateValidation — Create rejects an empty plan and a step missing a
// resource or command (fail-loud, no partial run written).
func TestRunStoreCreateValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, Input{Operator: "operator:admin"}); err == nil {
		t.Fatal("empty plan: want error, got nil")
	}
	if _, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "res-a"}}}); err == nil {
		t.Fatal("step missing command: want error, got nil")
	}
	if _, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{Command: "echo hi"}}}); err == nil {
		t.Fatal("step missing resource: want error, got nil")
	}
}

// TestRunClaimLease — T-056 DUR-2: ClaimRun is reclaimable. It takes a pending Run or
// a 'running' Run whose lease has expired, but NOT one holding a live lease;
// NextClaimable mirrors that, and RenewClaim pushes the lease forward.
func TestRunClaimLease(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.ID

	// Fresh pending → claimable, and the claim wins.
	if !inClaimable(t, s, id) {
		t.Fatal("a pending run must be claimable")
	}
	if ok, err := s.ClaimRun(ctx, id, 1200); err != nil || !ok {
		t.Fatalf("claim pending: ok=%v err=%v", ok, err)
	}

	// Now running with a live lease → not claimable, and a second claim loses.
	if inClaimable(t, s, id) {
		t.Fatal("a live-leased run must not be claimable")
	}
	if ok, _ := s.ClaimRun(ctx, id, 1200); ok {
		t.Fatal("claimed a live-leased run (double-drive)")
	}

	// Expire the lease (a crashed driver) → reclaimable, and the reclaim wins.
	if _, err := s.pool.Exec(ctx, `UPDATE runs SET claimed_until = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	if !inClaimable(t, s, id) {
		t.Fatal("a stale-leased run must be reclaimable")
	}
	if ok, _ := s.ClaimRun(ctx, id, 1200); !ok {
		t.Fatal("failed to reclaim a stale-leased run")
	}

	// RenewClaim pushes the lease into the future → not claimable again.
	if err := s.RenewClaim(ctx, id, 1200); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if inClaimable(t, s, id) {
		t.Fatal("a renewed run must not be claimable")
	}
}

// TestFinishRunLifecycleAndStepCorrelation — T-058a: FinishRun emits the terminal
// run.* event in-tx (AUD-14), and SetStepResult records the gateway invocation id (the
// run→invocation correlation, AUD-15). With run.launched (from Create) the lifecycle is
// fully audited: launched → completed.
func TestFinishRunLifecycleAndStepCorrelation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// The pre-dispatch 'running' set carries no id ("" → NULL via NULLIF); the terminal
	// set records the gateway invocation id.
	const invID = "01900000-0000-7000-8000-0000000000ff"
	if err := s.SetStepResult(ctx, created.ID, 0, StepRunning, "", ""); err != nil {
		t.Fatalf("running set: %v", err)
	}
	if err := s.SetStepResult(ctx, created.ID, 0, StepCompleted, "", invID); err != nil {
		t.Fatalf("terminal set: %v", err)
	}
	got, err := s.Get(ctx, created.ID)
	if err != nil || len(got.Steps) != 1 || got.Steps[0].InvocationID != invID {
		t.Fatalf("step invocation_id = %q, want %q (err=%v)", got.Steps[0].InvocationID, invID, err)
	}

	// The sequencer claims (pending→running) before finishing; FinishRun guards on
	// status='running' (T-059a), so claim first to mirror the real flow.
	if ok, err := s.ClaimRun(ctx, created.ID, 1200); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := s.FinishRun(ctx, created.ID, StatusCompleted); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if n := countRunOutcome(t, s, "run:"+created.ID, "run.completed"); n != 1 {
		t.Fatalf("run.completed count = %d, want 1", n)
	}
	if n := countRunOutcome(t, s, "run:"+created.ID, "run.launched"); n != 1 {
		t.Fatalf("run.launched count = %d, want 1 (lifecycle: launched→completed)", n)
	}
}

// TestCancelRun — T-059a (DUR-5): CancelRun cancels a pending|running Run → canceled +
// run.canceled in-tx; a second cancel (already terminal) returns ok=false; and FinishRun
// no-ops over a canceled Run (the race fix — no spurious run.completed).
func TestCancelRun(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	created, err := s.Create(ctx, Input{Operator: "operator:admin", Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ok, err := s.CancelRun(ctx, created.ID)
	if err != nil || !ok {
		t.Fatalf("cancel: ok=%v err=%v", ok, err)
	}
	if st, _ := s.RunStatus(ctx, created.ID); st != StatusCanceled {
		t.Fatalf("status after cancel = %q, want canceled", st)
	}
	if n := countRunOutcome(t, s, "run:"+created.ID, "run.canceled"); n != 1 {
		t.Fatalf("run.canceled count = %d, want 1", n)
	}
	if ok, _ := s.CancelRun(ctx, created.ID); ok {
		t.Fatal("canceling an already-canceled run must return ok=false")
	}
	// FinishRun must no-op over a canceled run (status='running' guard) — the race fix.
	if err := s.FinishRun(ctx, created.ID, StatusCompleted); err != nil {
		t.Fatalf("finish over canceled: %v", err)
	}
	if n := countRunOutcome(t, s, "run:"+created.ID, "run.completed"); n != 0 {
		t.Fatalf("run.completed count = %d, want 0 (FinishRun must no-op over canceled)", n)
	}
}

// TestRunActorGrantRoundTrip — T-064 (ADR-0062, the actor/principal split): a delegated
// Run records operator=principal + actor=agent + grant_id; Get reads them back; the
// run.launched actor = the agent. An operator-direct Run keeps actor/grant empty and
// run.launched actor = the operator (behavior-preserving).
func TestRunActorGrantRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const grantID = "01900000-0000-7000-8000-0000000000aa"

	del, err := s.Create(ctx, Input{
		Operator: "operator:grantor", Actor: "agent:bot-1", GrantID: grantID,
		Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}},
	})
	if err != nil {
		t.Fatalf("create delegated: %v", err)
	}
	got, err := s.Get(ctx, del.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Operator != "operator:grantor" || got.Actor != "agent:bot-1" || got.GrantID != grantID {
		t.Fatalf("delegated round-trip wrong: %+v", got)
	}
	if n := countRunActor(t, s, "run:"+del.ID, "run.launched", "agent:bot-1"); n != 1 {
		t.Fatalf("delegated run.launched actor != agent (count=%d)", n)
	}

	dir, err := s.Create(ctx, Input{Operator: "operator:alice", Steps: []StepInput{{ResourceID: "r-b", Command: "echo hi"}}})
	if err != nil {
		t.Fatalf("create direct: %v", err)
	}
	g2, _ := s.Get(ctx, dir.ID)
	if g2.Actor != "" || g2.GrantID != "" {
		t.Fatalf("operator-direct must have empty actor/grant: %+v", g2)
	}
	if n := countRunActor(t, s, "run:"+dir.ID, "run.launched", "operator:alice"); n != 1 {
		t.Fatalf("direct run.launched actor != operator (count=%d)", n)
	}
}

// TestRunLaunchIdempotency — T-065a: a delegated launch dedups on (grant_id, launch_key) —
// a retry returns ErrDuplicateLaunch and GetByLaunchKey re-attaches to the original Run (no
// double-launch). An operator-direct Run (empty launch_key) is never deduped.
func TestRunLaunchIdempotency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const grantID = "01900000-0000-7000-8000-0000000000bb"

	in := Input{
		Operator: "operator:grantor", Actor: "agent:bot-1", GrantID: grantID, LaunchKey: "idem-x",
		Steps: []StepInput{{ResourceID: "r-a", Command: "echo hi"}},
	}
	first, err := s.Create(ctx, in)
	if err != nil {
		t.Fatalf("first launch: %v", err)
	}
	if _, err := s.Create(ctx, in); !errors.Is(err, ErrDuplicateLaunch) {
		t.Fatalf("retry: want ErrDuplicateLaunch, got %v", err)
	}
	got, err := s.GetByLaunchKey(ctx, grantID, "idem-x")
	if err != nil || got.ID != first.ID {
		t.Fatalf("re-attach: got %q (err %v), want %q", got.ID, err, first.ID)
	}

	// Operator-direct (empty launch_key) is never deduped: two distinct Runs.
	op := Input{Operator: "operator:alice", Steps: []StepInput{{ResourceID: "r-b", Command: "echo hi"}}}
	a, err := s.Create(ctx, op)
	if err != nil {
		t.Fatalf("op run 1: %v", err)
	}
	b, err := s.Create(ctx, op)
	if err != nil {
		t.Fatalf("op run 2: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("operator-direct Runs must not dedup")
	}
}

// TestGrantBoundsAndCascade — T-066a: the per-grant counts (CountActiveForGrant,
// CountStepsForGrant) drive PERF-7, and the revocation cascade (CancelRunsForGrant) cancels
// the grant's active Runs + emits run.canceled; a canceled Run still consumed its budget.
func TestGrantBoundsAndCascade(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const grantID = "01900000-0000-7000-8000-0000000000cc"

	mk := func(key string, steps int) Run {
		ss := make([]StepInput, steps)
		for i := range ss {
			ss[i] = StepInput{ResourceID: "r-a", Command: "echo hi"}
		}
		r, err := s.Create(ctx, Input{Operator: "operator:grantor", Actor: "agent:bot", GrantID: grantID, LaunchKey: key, Steps: ss})
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		return r
	}
	r1, r2 := mk("k1", 2), mk("k2", 3)

	if n, _ := s.CountActiveForGrant(ctx, grantID); n != 2 {
		t.Fatalf("CountActiveForGrant = %d, want 2", n)
	}
	if n, _ := s.CountStepsForGrant(ctx, grantID, 0); n != 5 {
		t.Fatalf("CountStepsForGrant = %d, want 5", n)
	}

	n, err := s.CancelRunsForGrant(ctx, grantID)
	if err != nil || n != 2 {
		t.Fatalf("CancelRunsForGrant = %d (err %v), want 2", n, err)
	}
	for _, r := range []Run{r1, r2} {
		if st, _ := s.RunStatus(ctx, r.ID); st != StatusCanceled {
			t.Fatalf("run %s status = %q, want canceled", r.ID, st)
		}
		if c := countRunOutcome(t, s, "run:"+r.ID, "run.canceled"); c != 1 {
			t.Fatalf("run.canceled count for %s = %d, want 1", r.ID, c)
		}
	}
	if n, _ := s.CancelRunsForGrant(ctx, grantID); n != 0 {
		t.Fatalf("second cascade canceled %d, want 0 (all terminal)", n)
	}
	if n, _ := s.CountActiveForGrant(ctx, grantID); n != 0 {
		t.Fatalf("CountActiveForGrant after cascade = %d, want 0", n)
	}
	if n, _ := s.CountStepsForGrant(ctx, grantID, 0); n != 5 {
		t.Fatalf("CountStepsForGrant after cancel = %d, want 5 (canceled Runs still consumed budget)", n)
	}
}

func countRunActor(t *testing.T, s *Store, evidence, outcome, actor string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2 AND payload->>'actor' = $3`,
		evidence, outcome, actor).Scan(&n); err != nil {
		t.Fatalf("count run actor: %v", err)
	}
	return n
}

func countRunOutcome(t *testing.T, s *Store, evidence, outcome string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
		evidence, outcome).Scan(&n); err != nil {
		t.Fatalf("count run outcome: %v", err)
	}
	return n
}

func inClaimable(t *testing.T, s *Store, id string) bool {
	t.Helper()
	ids, err := s.NextClaimable(context.Background(), 1000)
	if err != nil {
		t.Fatalf("next claimable: %v", err)
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// TestLaunchBounded — the T-072 G1 live-PG proof of the SHARED bounded-launch core
// (SEC-30a): happy launch binds {operator=principal, actor, grant}; a same-key retry
// re-attaches — INCLUDING after the budget is exhausted (the ratified early-re-attach
// delta); the typed refusals carry the right kind (OutOfGrant / Bounds / GrantDead);
// every refusal is deny-audited (run.launch.denied).
func TestLaunchBounded(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	dsn := os.Getenv("API_TEST_DATABASE_URL")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	grants := grant.NewStore(pool, "api_outbox")

	g, err := grants.Create(ctx, grant.Input{
		Principal: "operator:lb", Agent: "agent:lb", Label: "t072",
		Resources: []string{"res-a"}, Verbs: []string{"exec"},
		ExpiresAt: time.Now().Add(time.Hour), MaxStepsPerRun: 2, MaxConcurrentRuns: 4, StepBudget: 3,
	})
	if err != nil {
		t.Fatalf("issue grant: %v", err)
	}
	oneStep := []StepInput{{ResourceID: "res-a", Command: "uptime"}}

	deniedCount := func() int {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM api_outbox
			 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = 'run.launch.denied'`,
			"grant:"+g.ID).Scan(&n); err != nil {
			t.Fatalf("count denials: %v", err)
		}
		return n
	}

	// Happy: created with the delegated identity bound from the grant.
	first, reattached, refusal, err := s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-1", oneStep)
	if err != nil || refusal != nil || reattached {
		t.Fatalf("happy launch: run=%+v reattached=%v refusal=%+v err=%v", first, reattached, refusal, err)
	}
	if first.Operator != "operator:lb" || first.Actor != "agent:lb" || first.GrantID != g.ID {
		t.Fatalf("identity binding wrong: %+v", first)
	}

	// Same-key retry → re-attach, same Run, no refusal.
	again, reattached, refusal, err := s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-1", oneStep)
	if err != nil || refusal != nil || !reattached || again.ID != first.ID {
		t.Fatalf("re-attach: run=%+v reattached=%v refusal=%+v err=%v", again, reattached, refusal, err)
	}

	// Exhaust the budget (1 consumed + 2 = 3 = budget).
	twoSteps := []StepInput{{ResourceID: "res-a", Command: "a"}, {ResourceID: "res-a", Command: "b"}}
	if _, _, refusal, err = s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-2", twoSteps); err != nil || refusal != nil {
		t.Fatalf("second launch: refusal=%+v err=%v", refusal, err)
	}

	// Budget exhausted → a NEW key refuses with Bounds + is audited.
	before := deniedCount()
	_, _, refusal, err = s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-3", oneStep)
	if err != nil || refusal == nil || refusal.Kind != RefusalBounds {
		t.Fatalf("budget refusal: refusal=%+v err=%v, want Bounds", refusal, err)
	}
	if deniedCount() != before+1 {
		t.Fatal("budget refusal was not deny-audited")
	}

	// The early-re-attach delta, proven live: the ORIGINAL key still re-attaches even
	// though the grant's budget is now exhausted (pre-T-072 this answered a bounds 429).
	again, reattached, refusal, err = s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-1", oneStep)
	if err != nil || refusal != nil || !reattached || again.ID != first.ID {
		t.Fatalf("post-exhaustion re-attach: run=%+v reattached=%v refusal=%+v err=%v", again, reattached, refusal, err)
	}

	// Foreign agent → OutOfGrant (validation precedes re-attach: even with a known key).
	_, _, refusal, err = s.LaunchBounded(ctx, grants, "agent:other", g.ID, "key-1", oneStep)
	if err != nil || refusal == nil || refusal.Kind != RefusalOutOfGrant {
		t.Fatalf("foreign agent: refusal=%+v err=%v, want OutOfGrant", refusal, err)
	}

	// Unknown grant → OutOfGrant.
	_, _, refusal, err = s.LaunchBounded(ctx, grants, "agent:lb", "00000000-0000-0000-0000-000000000000", "key-9", oneStep)
	if err != nil || refusal == nil || refusal.Kind != RefusalOutOfGrant {
		t.Fatalf("unknown grant: refusal=%+v err=%v, want OutOfGrant", refusal, err)
	}

	// Revoked grant → GrantDead (the T-073 self-retire trigger) — and validation-first
	// means even the original launch key refuses rather than re-attaching.
	if ok, err := grants.Revoke(ctx, g.ID); err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	_, _, refusal, err = s.LaunchBounded(ctx, grants, "agent:lb", g.ID, "key-1", oneStep)
	if err != nil || refusal == nil || refusal.Kind != RefusalGrantDead {
		t.Fatalf("revoked grant: refusal=%+v err=%v, want GrantDead", refusal, err)
	}
}

// TestCountStepsForGrantWindowed — WS-4 (ADR-0063 §3): a windowed count includes only
// Runs created within the rolling window (STRICT inequality), while windowSeconds=0
// keeps the v0.6 cumulative-forever semantics — behavior-preserving for NULL-window
// grants.
func TestCountStepsForGrantWindowed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	dsn := os.Getenv("API_TEST_DATABASE_URL")
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	gid, err := uuid.NewV7() // fresh per run (no FK on runs.grant_id) — keeps the fixed launch keys + counts re-runnable
	if err != nil {
		t.Fatalf("mint grant id: %v", err)
	}
	grantID := gid.String()
	mk := func(key string, steps int) Run {
		in := Input{Operator: "operator:win", Actor: "agent:win", GrantID: grantID, LaunchKey: key}
		for i := 0; i < steps; i++ {
			in.Steps = append(in.Steps, StepInput{ResourceID: "res-w", Command: "c"})
		}
		r, err := s.Create(ctx, in)
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		return r
	}
	old := mk("win-old", 2)
	_ = mk("win-new", 3)

	// Backdate the first Run beyond a 1h window.
	if _, err := pool.Exec(ctx, `UPDATE runs SET created_at = now() - interval '2 hours' WHERE id = $1`, old.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	if n, _ := s.CountStepsForGrant(ctx, grantID, 0); n != 5 {
		t.Fatalf("cumulative count = %d, want 5 (v0.6 semantics)", n)
	}
	if n, _ := s.CountStepsForGrant(ctx, grantID, 3600); n != 3 {
		t.Fatalf("windowed count = %d, want 3 (the backdated Run left the window)", n)
	}
	if n, _ := s.CountStepsForGrant(ctx, grantID, 3*3600); n != 5 {
		t.Fatalf("wide-window count = %d, want 5", n)
	}
}
