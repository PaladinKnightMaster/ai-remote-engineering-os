//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func runEngineDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type stepSeed struct {
	resourceID string
	command    string
}

func waitRunStatus(t *testing.T, pool *pgxpool.Pool, runID, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM runs WHERE id = $1`, runID).Scan(&status); err != nil {
			t.Fatalf("read run status: %v", err)
		}
		if status == want {
			return
		}
		if status == "failed" && want != "failed" {
			t.Fatalf("run %s failed (wanted %s)", runID, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s status = %q, want %q within %s", runID, status, want, within)
		}
		time.Sleep(2 * time.Second)
	}
}

func hasEventTarget(events []auditEvent, outcome, target string) bool {
	for _, e := range events {
		if e.Outcome == outcome && e.Target == target {
			return true
		}
	}
	return false
}

// launchRun POSTs an operator-authored plan to the real launch route (POST /v1/runs,
// T-057). Returns the new run id (or "" on a non-201) + the HTTP status, so callers
// can assert both the happy path (201) and a bounds rejection (422).
func launchRun(t *testing.T, client *http.Client, apiURL, session, csrf string, steps []stepSeed) (string, int) {
	t.Helper()
	type wireStep struct {
		ResourceID string `json:"resourceId"`
		Command    string `json:"command"`
	}
	ws := make([]wireStep, len(steps))
	for i, s := range steps {
		ws[i] = wireStep{ResourceID: s.resourceID, Command: s.command}
	}
	body, _ := json.Marshal(map[string]any{"steps": ws})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("launch run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", resp.StatusCode
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return out.ID, resp.StatusCode
}

type runStatusView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Steps  []struct {
		StepIndex    int    `json:"stepIndex"`
		Status       string `json:"status"`
		InvocationID string `json:"invocationId"`
	} `json:"steps"`
}

// getRun reads a Run's status via the operator route (GET /v1/runs/{id}, T-057).
func getRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) runStatusView {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/runs/"+id, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get run: status %d", resp.StatusCode)
	}
	var v runStatusView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return v
}

// cancelRun cancels a Run via the operator route (POST /v1/runs/{id}/cancel, T-059a)
// and returns the HTTP status.
func cancelRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs/"+id+"/cancel", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// TestRunEngineCancel — T-059a (DUR-5): the operator cancels a Run whose current step is
// parked. Cancel stops future steps (run.canceled); the parked step is then denied via
// /invocations to release the held call, and the Run STAYS canceled (FinishRun no-ops
// over the canceled status — the race fix).
func TestRunEngineCancel(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://localhost:8080")
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	pool := runEngineDB(t)
	auditTok := mintAuditToken(t)
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
		{resourceID, "echo CANCEL-A"},
		{flaggedResourceID, "echo CANCEL-B"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch: status %d, want 201", st)
	}
	// step 0 runs; step 1 (flagged) parks — the cancel window.
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)

	if code := cancelRun(t, client, apiURL, session, csrf, runID); code != http.StatusOK {
		t.Fatalf("cancel: status %d, want 200", code)
	}
	if got := getRun(t, client, apiURL, session, csrf, runID); got.Status != "canceled" {
		t.Fatalf("after cancel: status %q, want canceled", got.Status)
	}
	// Release the parked step (deny) → the held /v1/steps returns; the run STAYS canceled.
	// (Done before the audit poll so a slow relay can't strand the parked invocation.)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")
	// run.canceled is drained to the chain by the async outbox relay — poll for it.
	pollEvidenceOutcome(t, auditURL, auditTok, "run:"+runID, "run.canceled", 30*time.Second)
	waitRunStatus(t, pool, runID, "canceled", 30*time.Second)
	t.Logf("PASS T-059a — operator cancel stops future steps (run.canceled) + survives the parked step's deny (FinishRun no-op race)")
}

type seededStep struct {
	resourceID string
	command    string
	status     string
}

// seedRunWithState inserts a Run in an arbitrary (post-crash) state — a given run
// status + claimed_until + per-step statuses — so a crashed mid-Run can be reproduced
// without docker-killing the api (T-056). gen_random_uuid() supplies the id.
func seedRunWithState(t *testing.T, pool *pgxpool.Pool, operator, runStatus string, claimedUntil time.Time, steps []seededStep) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO runs (id, operator, status, claimed_until) VALUES (gen_random_uuid(), $1, $2, $3) RETURNING id`,
		operator, runStatus, claimedUntil).Scan(&id); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	for i, st := range steps {
		if _, err := pool.Exec(ctx,
			`INSERT INTO run_steps (run_id, step_index, resource_id, command, status) VALUES ($1, $2, $3, $4, $5)`,
			id, i, st.resourceID, []byte(st.command), st.status); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}
	return id
}

func assertStepStatus(t *testing.T, pool *pgxpool.Pool, runID string, index int, want string) {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM run_steps WHERE run_id = $1 AND step_index = $2`, runID, index).Scan(&status); err != nil {
		t.Fatalf("read step %d: %v", index, err)
	}
	if status != want {
		t.Fatalf("step %d status = %q, want %q", index, status, want)
	}
}

// TestRunEngineResumeAndOrphan — T-056 G1: crash-safety, with the crash simulated via
// seeded post-crash DB state (a stale lease + mixed step statuses).
//   - Resume: a stale-leased 'running' Run with step 0 'completed', step 1 'pending' is
//     reclaimed, step 0 skipped (NOT re-executed), step 1 driven live → completed.
//   - Orphan: a stale-leased 'running' Run with a step stuck 'running' is reclaimed →
//     fail-closed (no re-dispatch) → failed.
func TestRunEngineResumeAndOrphan(t *testing.T) {
	pool := runEngineDB(t)
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	auditTok := mintAuditToken(t)
	stale := time.Now().Add(-time.Minute)

	resumeID := seedRunWithState(t, pool, "operator:admin", "running", stale, []seededStep{
		{resourceID, "echo DONE-0", "completed"},
		{resourceID, "echo RESUME-1", "pending"},
	})
	waitRunStatus(t, pool, resumeID, "completed", 120*time.Second)
	assertStepStatus(t, pool, resumeID, 0, "completed") // untouched — proves no re-exec of the done step
	assertStepStatus(t, pool, resumeID, 1, "completed") // driven on resume
	t.Logf("PASS resume — reclaimed a stale-leased run, skipped the completed step, drove the pending one")

	orphanID := seedRunWithState(t, pool, "operator:admin", "running", stale, []seededStep{
		{resourceID, "echo ORPHAN", "running"},
	})
	waitRunStatus(t, pool, orphanID, "failed", 120*time.Second)
	if !hasEventTarget(listAllEvents(t, auditURL, auditTok), "run.aborted", "run:"+orphanID) {
		t.Fatal("run.aborted not audited for the failed orphan run (AUD-14)")
	}
	t.Logf("PASS orphan — a step stuck 'running' across a restart fails the run closed (no re-dispatch); run.aborted audited")
	t.Logf("PASS T-056 — reclaimable claim + crash-resume + running-orphan fail-closed on real wires")
}

func hasOperatorToolInvoked(events []auditEvent, actor, target string) bool {
	for _, e := range events {
		if e.Outcome == "tool.invoked" && e.Actor == actor && e.Edge == "human-agent" && e.Target == target {
			return true
		}
	}
	return false
}

// TestRunEngineEndToEnd — T-057 G1: the operator launches Runs via the REAL route
// (POST /v1/runs — informed, bounded, audited) and the wired sequencer drives them
// through the gateway's governed /v1/steps → policy → connector → broker → sshd →
// audit, with the OPERATOR as actor on the human-agent bracket edge (RUN-5). Bounds:
// an oversized plan is rejected 422. Leg 1: an ungated multi-step Run completes (GET
// shows it; run.launched is audited). Leg 2: a flagged step parks, the operator
// approves via /v1/invocations, the held step resumes, the Run completes.
func TestRunEngineEndToEnd(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://localhost:8080")
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	pool := runEngineDB(t)
	auditTok := mintAuditToken(t)
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	// Bounds (PERF-6): an oversized plan (> 50 steps) is rejected 422 before any Run
	// is created.
	tooMany := make([]stepSeed, 51)
	for i := range tooMany {
		tooMany[i] = stepSeed{resourceID, "echo x"}
	}
	if _, st := launchRun(t, client, apiURL, session, csrf, tooMany); st != http.StatusUnprocessableEntity {
		t.Fatalf("oversized plan: status %d, want 422", st)
	}

	// --- Leg 1: ungated multi-step run via the REAL launch route ---------------
	runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
		{resourceID, "echo RUN-A1"},
		{resourceID, "echo RUN-A2"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch: status %d, want 201", st)
	}
	waitRunStatus(t, pool, runID, "completed", 120*time.Second)

	// GET route (owner-scoped): the run shows completed + both steps completed.
	got := getRun(t, client, apiURL, session, csrf, runID)
	if got.Status != "completed" || len(got.Steps) != 2 {
		t.Fatalf("GET run: %+v, want completed with 2 steps", got)
	}
	// EVD-1: the GET surfaces each step's invocation id — the read-time evidence-packet
	// anchor into the hash-linked chain.
	for _, st := range got.Steps {
		if st.InvocationID == "" {
			t.Fatalf("GET run step %d missing invocationId (evidence-packet bridge)", st.StepIndex)
		}
	}

	events := listAllEvents(t, auditURL, auditTok)
	if !hasOperatorToolInvoked(events, "operator:admin", "resource:"+resourceID) {
		t.Fatal("no operator tool.invoked (human-agent) for the run's resource — RUN-5 not recorded")
	}
	if !hasOutcome(events, "remote.exec") {
		t.Fatal("no remote.exec egress — the sequencer did not drive a real exec")
	}
	if !hasEventTarget(events, "run.launched", "run:"+runID) {
		t.Fatal("run.launched not audited for the launched run (GOV-1)")
	}
	if !hasEventTarget(events, "run.completed", "run:"+runID) {
		t.Fatal("run.completed not audited (AUD-14 terminal lifecycle event)")
	}
	// AUD-15: each step records its gateway invocation id (the run→chain bridge).
	var invCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM run_steps WHERE run_id = $1 AND invocation_id IS NOT NULL`, runID).Scan(&invCount); err != nil {
		t.Fatalf("count step invocation_ids: %v", err)
	}
	if invCount != 2 {
		t.Fatalf("recorded step invocation_ids = %d, want 2 (AUD-15 correlation)", invCount)
	}
	t.Logf("PASS leg 1 — real launch (POST /v1/runs) → sequencer drove an ungated 2-step run; GET completed; run.launched+run.completed audited; step invocation ids recorded")

	// --- Leg 2: a flagged step parks → operator approves → run completes -------
	runID2, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{{flaggedResourceID, "echo RUN-B1"}})
	if st != http.StatusCreated {
		t.Fatalf("launch flagged: status %d, want 201", st)
	}
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "approve")
	waitRunStatus(t, pool, runID2, "completed", 120*time.Second)
	t.Logf("PASS leg 2 — flagged step parked, operator approved, held step resumed, run completed")
	t.Logf("PASS T-057 — operator launches Runs via POST /v1/runs (informed, bounded, audited); sequencer drives them on real wires")
}

// stepInvocationID reads a step's recorded gateway invocation id (NULL → ""), the
// run→chain bridge used to assert dispatch (or its absence).
func stepInvocationID(t *testing.T, pool *pgxpool.Pool, runID string, index int) string {
	t.Helper()
	var inv *string
	if err := pool.QueryRow(context.Background(),
		`SELECT invocation_id::text FROM run_steps WHERE run_id = $1 AND step_index = $2`, runID, index).Scan(&inv); err != nil {
		t.Fatalf("read step %d invocation_id: %v", index, err)
	}
	if inv == nil {
		return ""
	}
	return *inv
}

// TestWD5HappyPath — WD-5 facets 1-3, 7 (the capstone happy path). On the live stack an
// operator launches ONE ordered >=3-step plan that MIXES ungated and requires_approval
// resources. Step 0 (ungated) runs; step 1 (flagged) PARKS and is approved on the
// /invocations console; the engine then CONTINUES to step 2 (ungated) — the mid-plan
// park->approve->continue that neither EndToEnd leg proves on its own. The terminal Run
// is the evidence packet: GET surfaces each step's invocation id, and the chain carries
// run.launched -> per-step egress -> run.completed, every run.* anchored run:<id>. Chain
// hash-integrity is asserted by the suite's trailing `audit-service verify` (facet 7).
func TestWD5HappyPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://localhost:8080")
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	pool := runEngineDB(t)
	auditTok := mintAuditToken(t)
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	// Compose + informed launch (facets 1-2): a 3-step plan mixing ungated + flagged.
	runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
		{resourceID, "echo WD5-H1"},
		{flaggedResourceID, "echo WD5-H2"},
		{resourceID, "echo WD5-H3"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch: status %d, want 201", st)
	}

	// Facet 3: the flagged step 1 parks and is approved; the held step resumes.
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "approve")

	// The engine CONTINUES past the approved step to drive step 2 → completed.
	waitRunStatus(t, pool, runID, "completed", 180*time.Second)

	// Facet 7 (evidence packet): GET surfaces completed steps each with an invocation id.
	got := getRun(t, client, apiURL, session, csrf, runID)
	if got.Status != "completed" || len(got.Steps) != 3 {
		t.Fatalf("GET run: %+v, want completed with 3 steps", got)
	}
	for _, s := range got.Steps {
		if s.Status != "completed" || s.InvocationID == "" {
			t.Fatalf("step %d = %q/%q, want completed with an invocationId (evidence-packet bridge)", s.StepIndex, s.Status, s.InvocationID)
		}
	}

	events := listAllEvents(t, auditURL, auditTok)
	if !hasEventTarget(events, "run.launched", "run:"+runID) {
		t.Fatal("run.launched not audited (GOV-1, facet 1)")
	}
	if !hasEventTarget(events, "run.completed", "run:"+runID) {
		t.Fatal("run.completed not audited (AUD-14, facet 7)")
	}
	if !hasOperatorToolInvoked(events, "operator:admin", "resource:"+resourceID) {
		t.Fatal("no operator tool.invoked (human-agent) — RUN-5 bracket not recorded (facet 2)")
	}
	if !hasOutcome(events, "remote.exec") {
		t.Fatal("no remote.exec egress — the sequencer did not drive a real exec (facet 2)")
	}
	t.Logf("PASS WD-5 happy path — mixed 3-step run: ungated → flagged park→approve → CONTINUE → ungated → completed; evidence packet (3 step invocation ids) + run.launched/run.completed audited")
}

// TestWD5DeniedAbort — WD-5 facet 4 (fail-closed abort), the explicitly missing leg.
// An operator launches a 2-step Run whose FIRST step is flagged; the operator DENIES it.
// The Run is marked failed (run.aborted), and — the load-bearing assertion — the SECOND
// step is NEVER dispatched: it stays pending with no gateway invocation id, so no further
// egress is possible. (Commands are content-free in the chain by design, so the absence
// of dispatch is proven by the NULL invocation id, not by a command-scoped audit search.)
func TestWD5DeniedAbort(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://localhost:8080")
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	pool := runEngineDB(t)
	auditTok := mintAuditToken(t)
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
		{flaggedResourceID, "echo WD5-D1"},
		{resourceID, "echo WD5-D2"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch: status %d, want 201", st)
	}

	// Step 0 (flagged) parks; the operator DENIES it → the engine stops fail-closed.
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")

	waitRunStatus(t, pool, runID, "failed", 120*time.Second)
	pollEvidenceOutcome(t, auditURL, auditTok, "run:"+runID, "run.aborted", 30*time.Second)

	// The denied step is failed; the subsequent step was NEVER dispatched — no further
	// egress. (The denied step does record an invocation id, sequencer.go; the NEXT one
	// must not, because the loop returned before reaching it.)
	assertStepStatus(t, pool, runID, 0, "failed")
	assertStepStatus(t, pool, runID, 1, "pending")
	if id := stepInvocationID(t, pool, runID, 1); id != "" {
		t.Fatalf("undispatched step 1 has invocation_id %q — the engine dispatched past a denied step (NOT fail-closed)", id)
	}
	if got := getRun(t, client, apiURL, session, csrf, runID); got.Status != "failed" {
		t.Fatalf("GET run after deny: status %q, want failed", got.Status)
	}
	t.Logf("PASS WD-5 denied-abort — a denied flagged step aborts the run fail-closed (run.aborted); the subsequent step is never dispatched (no invocation, no egress)")
}
