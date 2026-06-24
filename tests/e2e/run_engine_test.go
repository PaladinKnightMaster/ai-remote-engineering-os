//go:build e2e

package e2e

import (
	"context"
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

// seedRun inserts a launched Run + its ordered steps directly — the operator launch
// route is T-057, so here the test plays the operator launching a plan. The api-side
// command is plaintext at rest (nil sealer, T-054), so it is inserted as raw bytes.
// gen_random_uuid() (PG16 core) supplies the id, so the e2e module needs no uuid dep.
func seedRun(t *testing.T, pool *pgxpool.Pool, operator string, steps []stepSeed) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO runs (id, operator) VALUES (gen_random_uuid(), $1) RETURNING id`, operator).Scan(&id); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	for i, st := range steps {
		if _, err := pool.Exec(ctx,
			`INSERT INTO run_steps (run_id, step_index, resource_id, command) VALUES ($1, $2, $3, $4)`,
			id, i, st.resourceID, []byte(st.command)); err != nil {
			t.Fatalf("insert step %d: %v", i, err)
		}
	}
	return id
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

func assertAllSteps(t *testing.T, pool *pgxpool.Pool, runID, want string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT status FROM run_steps WHERE run_id = $1 ORDER BY step_index`, runID)
	if err != nil {
		t.Fatalf("read steps: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatalf("scan step: %v", err)
		}
		if status != want {
			t.Fatalf("step %d status = %q, want %q", n, status, want)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no steps found for run")
	}
}

func hasOperatorToolInvoked(events []auditEvent, actor, target string) bool {
	for _, e := range events {
		if e.Outcome == "tool.invoked" && e.Actor == actor && e.Edge == "human-agent" && e.Target == target {
			return true
		}
	}
	return false
}

// TestRunEngineEndToEnd — T-055c G1: the wired api Run sequencer drives a launched Run
// through the gateway's governed /v1/steps (subject-pinned to api) → policy → connector
// → broker → sshd → audit, with the OPERATOR as actor on the human-agent bracket edge
// (RUN-5). Leg 1: an ungated multi-step Run completes. Leg 2: a flagged step parks, the
// operator approves via /v1/invocations, the held step resumes, the Run completes.
func TestRunEngineEndToEnd(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://localhost:8080")
	auditURL := env("E2E_AUDIT_URL", "http://localhost:8081")
	pool := runEngineDB(t)
	auditTok := mintAuditToken(t)

	// --- Leg 1: ungated multi-step run drives to completion --------------------
	runID := seedRun(t, pool, "operator:admin", []stepSeed{
		{resourceID, "echo RUN-A1"},
		{resourceID, "echo RUN-A2"},
	})
	waitRunStatus(t, pool, runID, "completed", 120*time.Second)
	assertAllSteps(t, pool, runID, "completed")

	events := listAllEvents(t, auditURL, auditTok)
	if !hasOperatorToolInvoked(events, "operator:admin", "resource:"+resourceID) {
		t.Fatal("no operator tool.invoked (human-agent) for the run's resource — RUN-5 not recorded")
	}
	if !hasOutcome(events, "remote.exec") {
		t.Fatal("no remote.exec egress — the sequencer did not drive a real exec")
	}
	t.Logf("PASS leg 1 — sequencer drove an ungated 2-step run to completion (operator/human-agent, real exec)")

	// --- Leg 2: a flagged step parks → operator approves → run completes -------
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	runID2 := seedRun(t, pool, "operator:admin", []stepSeed{{flaggedResourceID, "echo RUN-B1"}})
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "approve")
	waitRunStatus(t, pool, runID2, "completed", 120*time.Second)
	t.Logf("PASS leg 2 — flagged step parked, operator approved, held step resumed, run completed")
	t.Logf("PASS T-055c — the api Run sequencer drives launched Runs through the governed gateway plane on real wires")
}
