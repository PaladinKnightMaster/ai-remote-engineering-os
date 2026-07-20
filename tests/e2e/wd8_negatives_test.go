//go:build e2e

// WD-8 negatives (T-083b, docs/releases/v0.8.0 §2) — the containment legs of the
// Persistent Scoped Memory gate, all live: N1 redaction is real and idempotent, N2 the
// agent plane cannot touch memory (SEC-31), N3 owner-scope holds (the honest-404
// boundary), N4 terminal-outcome completeness (failed + canceled Runs each accrue
// exactly one pointer, proving F-a on the non-FinishRun paths).
package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestWD8RedactIdempotent — N1: a second redact of an already-redacted memory is a
// silent no-op (still 200, the view unchanged) with NO duplicate memory.redacted event
// (the flipWithEvent once-only precedent applied to memory).
func TestWD8RedactIdempotent(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	m := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "operator", ScopeID: "admin", Key: "wd8-n1-" + m8suffix(), Content: "redact me twice",
	})

	first, code1 := redactMemory(t, client, apiURL, session, csrf, m.ID)
	if code1 != http.StatusOK {
		t.Fatalf("first redact: status %d, want 200", code1)
	}
	second, code2 := redactMemory(t, client, apiURL, session, csrf, m.ID)
	if code2 != http.StatusOK {
		t.Fatalf("second redact: status %d, want 200 (idempotent, D1)", code2)
	}
	if first.RedactedAt == nil || second.RedactedAt == nil || !first.RedactedAt.Equal(*second.RedactedAt) {
		t.Fatalf("redactedAt changed across the no-op redact: first=%v second=%v", first.RedactedAt, second.RedactedAt)
	}
	if second.Content != nil {
		t.Fatalf("second redact view still carries content: %+v", second)
	}

	// The outbox relay drains asynchronously — poll for the event to land before
	// counting (the house pattern every other chain assertion in this suite uses),
	// rather than racing a single listAllEvents snapshot right after the HTTP 200s.
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.redacted", 30*time.Second)
	var count int
	for _, e := range listAllEvents(t, auditURL, auditTok) {
		if e.Target == "memory:"+m.ID && e.Outcome == "memory.redacted" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("memory.redacted count for %s = %d, want exactly 1 across two redacts", m.ID, count)
	}
	t.Logf("PASS WD-8 N1 — redact is idempotent: two calls both 200, identical tombstoned view, exactly ONE memory.redacted chained")
}

// TestWD8AgentPlaneRejected — N2 (SEC-31): a VALID agent-tool token (aud=mcp-gateway)
// against services/api's operator-plane /v1/memories* routes is rejected — the wrong
// edge/wrong plane, not a signature failure (the WD-4 forge-test precedent applied to
// the memory boundary). The gateway's tools/list exposes exactly the five control-plane
// tools (F-b, T-090 amendment): remote.exec, run.launch, workstream.poll, plan.submit,
// memory.read — and, memory.read's own governance carve-out aside, NO memory-WRITE
// tool of any kind (no author/redact/share/pin verb reaches the agent plane; the read
// plane is memory.read alone, chained per ADR-0065 §3/R4).
func TestWD8AgentPlaneRejected(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	agentTok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", resourceID)

	// GET /v1/memories with the agent token as Authorization: Bearer — no cookie, so
	// the operator capability gate never sees a session at all.
	getReq, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/memories?scopeType=operator&scopeId=admin", nil)
	getReq.Header.Set("Authorization", "Bearer "+agentTok)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("agent-token GET /v1/memories: %v", err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusUnauthorized && getResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent-token GET /v1/memories: status %d, want 401 or 403", getResp.StatusCode)
	}

	// POST /v1/memories likewise.
	postBody := strings.NewReader(`{"scopeType":"operator","scopeId":"admin","content":"should never land"}`)
	postReq, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/memories", postBody)
	postReq.Header.Set("Authorization", "Bearer "+agentTok)
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("agent-token POST /v1/memories: %v", err)
	}
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusUnauthorized && postResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent-token POST /v1/memories: status %d, want 401 or 403", postResp.StatusCode)
	}

	// The gateway's tool catalog carries no memory-WRITE tool of any kind — only the
	// two governed-step control-plane tools (remote.exec, run.launch) plus the three
	// v0.9 adaptive-workstream control-plane tools (workstream.poll, plan.submit, and
	// memory.read — the ONE lit agent-memory READ path, F-b/T-090).
	toolsBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	var tools []string
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", strings.NewReader(toolsBody))
		req.Header.Set("Authorization", "Bearer "+agentTok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result struct {
					Tools []struct {
						Name string `json:"name"`
					} `json:"tools"`
				} `json:"result"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.NewDecoder(resp.Body).Decode(&rpc) == nil {
				resp.Body.Close()
				if rpc.Error == nil {
					for _, tl := range rpc.Result.Tools {
						tools = append(tools, tl.Name)
					}
					break
				}
			} else {
				resp.Body.Close()
			}
		} else if resp != nil {
			resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway tools/list never returned a result (last err=%v)", err)
		}
		time.Sleep(2 * time.Second)
	}
	// F-b (T-090 amendment): the exact tool surface is five, not two — the v0.9
	// adaptive-workstream control-plane tools ride alongside remote.exec/run.launch.
	// The set-equality check below still enforces the N2 spirit intact: no memory tool
	// OTHER THAN memory.read reaches the agent plane — no write/author/redact/share/pin
	// verb of any kind, because any such tool would inflate len(tools) or introduce a
	// name outside `want` and fail this assertion.
	want := map[string]bool{
		"remote.exec": true, "run.launch": true,
		"workstream.poll": true, "plan.submit": true, "memory.read": true,
	}
	if len(tools) != len(want) {
		t.Fatalf("gateway tools/list = %v, want exactly %v (no memory-write tool)", tools, want)
	}
	for _, name := range tools {
		if !want[name] {
			t.Fatalf("gateway tools/list exposes %q — the agent plane must carry no memory tool other than memory.read", name)
		}
	}
	t.Logf("PASS WD-8 N2 — a valid agent-tool token is rejected on /v1/memories* (%d/%d) and the gateway catalog carries no memory-write tool (only %v)", getResp.StatusCode, postResp.StatusCode, tools)
}

// TestWD8OwnerScope404 — N3 (D2, honest scoping): redact/pin/unpin on a well-formed
// but unknown id answer 404 (the owner-scoped ownedMemory Get-then-flip guard shared by
// all three forgetting verbs). TRUE foreign-owner-404 (a different operator's row) is
// proven at the live-PG integration level (T-079/T-081 store tests) — the
// single-bootstrap-operator posture provides no second operator on this live stack to
// exercise cross-owner denial end-to-end.
func TestWD8OwnerScope404(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	const unknownID = "01900000-0000-7000-8000-0000000000ff" // well-formed, never seeded
	for _, verb := range []string{"redact", "pin", "unpin"} {
		if _, code := memoryVerb(t, client, apiURL, session, csrf, unknownID, verb); code != http.StatusNotFound {
			t.Fatalf("%s on an unknown memory id: status %d, want 404", verb, code)
		}
	}
	t.Logf("PASS WD-8 N3 — redact/pin/unpin on a well-formed unknown id all answer 404 (the owner-scoped ownedMemory guard)")
}

// TestWD8TerminalCompleteness — N4 (F-a proven live): a FAILED Run and an
// operator-CANCELED Run (cancel mid-flight, not FinishRun) each accrue EXACTLY ONE
// content-free pointer, proving the accrual hook rides every terminal outcome on the
// shared once-only flip, not just the FinishRun happy path.
func TestWD8TerminalCompleteness(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	client := &http.Client{Timeout: 30 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	t.Run("failed", func(t *testing.T) {
		// The TestWD5DeniedAbort pattern: a denied flagged FIRST step aborts the plan
		// fail-closed — the SECOND step never dispatches.
		runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
			{flaggedResourceID, "echo WD8-N4-FAIL-1"},
			{resourceID, "echo WD8-N4-FAIL-2"},
		})
		if st != http.StatusCreated {
			t.Fatalf("launch: status %d, want 201", st)
		}
		inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
		resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")
		waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "failed", 120*time.Second)

		mem := waitForMemory(t, client, apiURL, session, csrf, "run", runID, 15*time.Second, func(m memoryView) bool {
			return m.Ref == "run:"+runID
		})
		wantTrace := "run:" + runID + ":failed"
		if mem.SourceTrace != wantTrace {
			t.Fatalf("failed run accrual sourceTrace = %q, want %q", mem.SourceTrace, wantTrace)
		}
		items := listMemories(t, client, apiURL, session, csrf, "run", runID)
		var n int
		for _, m := range items {
			if m.Ref == "run:"+runID {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("failed run %s: %d accrued memory rows, want exactly 1", runID, n)
		}
		t.Logf("PASS WD-8 N4(a) — a failed Run (denied flagged step, fail-closed abort) accrued exactly one pointer, sourceTrace=%s", mem.SourceTrace)
	})

	t.Run("canceled", func(t *testing.T) {
		// The TestRunEngineCancel pattern: cancel mid-flight while a step is parked —
		// the setRunTerminal path, NOT FinishRun.
		runID, st := launchRun(t, client, apiURL, session, csrf, []stepSeed{
			{resourceID, "echo WD8-N4-CANCEL-1"},
			{flaggedResourceID, "echo WD8-N4-CANCEL-2"},
		})
		if st != http.StatusCreated {
			t.Fatalf("launch: status %d, want 201", st)
		}
		inv := pollPendingInvocation(t, client, apiURL, session, csrf, flaggedResourceID, 90*time.Second)
		if code := cancelRun(t, client, apiURL, session, csrf, runID); code != http.StatusOK {
			t.Fatalf("cancel: status %d, want 200", code)
		}
		// Release the parked step so the held call returns; the Run stays canceled.
		resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")
		waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "canceled", 30*time.Second)

		mem := waitForMemory(t, client, apiURL, session, csrf, "run", runID, 15*time.Second, func(m memoryView) bool {
			return m.Ref == "run:"+runID
		})
		wantTrace := "run:" + runID + ":canceled"
		if mem.SourceTrace != wantTrace {
			t.Fatalf("canceled run accrual sourceTrace = %q, want %q", mem.SourceTrace, wantTrace)
		}
		items := listMemories(t, client, apiURL, session, csrf, "run", runID)
		var n int
		for _, m := range items {
			if m.Ref == "run:"+runID {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("canceled run %s: %d accrued memory rows, want exactly 1", runID, n)
		}
		t.Logf("PASS WD-8 N4(b) — an operator-canceled Run (mid-flight, not FinishRun) accrued exactly one pointer, sourceTrace=%s", mem.SourceTrace)
	})
}

// m8suffix returns a short time-derived suffix so re-runs against a dirty stack never
// collide on an authored memory's key (the T-073 fresh-key lesson).
func m8suffix() string {
	return time.Now().UTC().Format("150405.000000000")
}
