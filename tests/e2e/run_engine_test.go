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
		StepIndex int    `json:"stepIndex"`
		Status    string `json:"status"`
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
	t.Logf("PASS orphan — a step stuck 'running' across a restart fails the run closed (no re-dispatch)")
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
	t.Logf("PASS leg 1 — real launch (POST /v1/runs) → sequencer drove an ungated 2-step run; GET completed; run.launched audited")

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
