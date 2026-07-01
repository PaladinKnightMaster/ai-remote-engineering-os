//go:build e2e

// TestDelegatedRunLaunch — the T-065b live capstone (ADR-0062): an agent autonomously
// drives a grant-bounded Run end-to-end on the real `task e2e` stack. It is the WD-6
// happy-path skeleton:
//
//	operator issues a delegation grant → the agent calls the run.launch MCP tool → the
//	KEYLESS gateway forwards {agent, grant, plan} to api's route-scoped internal route →
//	api validates plan-vs-grant and creates the Run (actor=agent, principal=operator) →
//	the sequencer drives the ungated step through /v1/steps → policy → connector → broker
//	→ sshd → audit, with the delegated bracket edge agent-tool and run.launched actor=agent.
//
// Plus the two live negatives: N1 an out-of-grant plan is refused before any egress; N3 a
// direct agent → api forward is rejected (the key-holding api is not agent-reachable). The
// flagged-step park→approve leg + revocation-cancel + the full WD-6 are T-068.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDelegatedRunLaunch(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	const agentSubject = "agent:e2e"
	const wantOut = "DELEGATED-OK"

	client := &http.Client{Timeout: 60 * time.Second}

	// Operator login (retry: the go-run stack may still be compiling).
	var session, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, apiURL)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			session, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}

	// The operator issues a delegation grant to the agent for a DEDICATED ungated resource +
	// verb exec (delegatedResourceID is isolated from resourceID's audit chain so the capstone
	// does not pollute the other suites' first-match audit polls).
	grantID := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 50)

	// The agent's token (audience mcp-gateway) — its subject is the grant's agent. run.launch
	// is control-plane, so the token's resource scope is irrelevant (the grant is the scope).
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", delegatedResourceID)

	// The agent launches a grant-bounded Run (one ungated step) via run.launch.
	runID := callRunLaunchOK(t, client, mcpURL, tok, grantID, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo " + wantOut},
	})

	// The sequencer drives it; poll the operator status route to completion.
	var view runStatusView
	runDeadline := time.Now().Add(120 * time.Second)
	for {
		view = getRun(t, client, apiURL, session, csrf, runID)
		if view.Status == "completed" {
			break
		}
		if view.Status == "failed" || view.Status == "canceled" {
			t.Fatalf("delegated run ended %s, want completed: %+v", view.Status, view)
		}
		if time.Now().After(runDeadline) {
			t.Fatalf("delegated run never completed (status=%s)", view.Status)
		}
		time.Sleep(2 * time.Second)
	}
	if len(view.Steps) != 1 || view.Steps[0].InvocationID == "" {
		t.Fatalf("expected 1 dispatched step with an invocation id, got %+v", view.Steps)
	}
	invID := view.Steps[0].InvocationID

	auditToken := mintAuditToken(t)

	// run.launched actor is the AGENT (not the operator) — the delegated launch.
	launched := pollTargetOutcome(t, auditURL, auditToken, "run:"+runID, "run.launched", 45*time.Second)
	if launched.Actor != agentSubject {
		t.Fatalf("run.launched actor = %q, want %q (delegated)", launched.Actor, agentSubject)
	}

	// The step's tool.invoked carries the DELEGATED bracket edge agent-tool (ADR-0062 §4),
	// with the agent as actor — the T-064 principal is on the invocation row (AUD-17).
	step := pollEvidenceOutcomeEv(t, auditURL, auditToken, "invocation:"+invID, "tool.invoked", 45*time.Second)
	if step.Actor != agentSubject || step.Edge != "agent-tool" {
		t.Fatalf("delegated step tool.invoked = {actor:%q edge:%q}, want {agent:e2e agent-tool}", step.Actor, step.Edge)
	}
	t.Logf("PASS delegated run %s completed: run.launched actor=%s, step edge=agent-tool", runID, agentSubject)

	// N1 — an out-of-grant plan (a resource NOT in the grant) is refused before any egress.
	text, isErr := callRunLaunch(t, client, mcpURL, tok, grantID, []map[string]string{
		{"resourceId": flaggedResourceID, "command": "echo NOPE"},
	})
	if !isErr || !strings.Contains(strings.ToLower(text), "refused") {
		t.Fatalf("out-of-grant launch = {isError:%v text:%q}, want a refusal", isErr, text)
	}
	t.Log("PASS N1 — out-of-grant plan refused (no Run, no egress)")

	// N3 — a DIRECT agent → api forward is rejected (the key-holding api is not agent-reachable;
	// the route-scoped verifier accepts only the gateway's v4.public token).
	body, _ := json.Marshal(map[string]any{
		"agentSubject": agentSubject, "grantId": grantID, "idempotencyKey": "n3",
		"steps": []map[string]string{{"resourceId": delegatedResourceID, "command": "echo X"}},
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/internal/delegated-runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok) // the agent's aud=mcp-gateway token
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("N3 direct call: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("direct agent→api forward = %d, want 401/403 (key-holding api not agent-reachable)", resp.StatusCode)
	}
	t.Logf("PASS N3 — direct agent→api forward rejected (%d)", resp.StatusCode)

	// PERF-7 — a plan exceeding the grant's step budget is refused (a grant with budget 1, a
	// 2-step plan; the check is before any Run is created).
	tightGrant := issueGrant(t, client, apiURL, session, csrf, agentSubject, []string{delegatedResourceID}, 1)
	bText, bErr := callRunLaunch(t, client, mcpURL, tok, tightGrant, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo A"},
		{"resourceId": delegatedResourceID, "command": "echo B"},
	})
	if !bErr || !strings.Contains(strings.ToLower(bText), "budget") {
		t.Fatalf("over-budget launch = {isError:%v text:%q}, want a budget refusal", bErr, bText)
	}
	t.Log("PASS PERF-7 — over-budget plan refused")

	// DUR-7 — revoking a grant blocks further launches under it (the cascade of in-flight Runs
	// is store-tested; the full in-flight-revoke walkthrough is T-068).
	revokeGrantHTTP(t, client, apiURL, session, csrf, grantID)
	rText, rErr := callRunLaunch(t, client, mcpURL, tok, grantID, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo X"},
	})
	if !rErr || !strings.Contains(strings.ToLower(rText), "refus") {
		t.Fatalf("launch under a revoked grant = {isError:%v text:%q}, want a refusal", rErr, rText)
	}
	t.Log("PASS DUR-7 — launch under a revoked grant refused")
}

// revokeGrantHTTP revokes a grant via the operator route (POST /v1/grants/{id}/revoke).
func revokeGrantHTTP(t *testing.T, client *http.Client, apiURL, session, csrf, id string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/grants/"+id+"/revoke", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke grant: status %d, want 200", resp.StatusCode)
	}
}

// issueGrant POSTs a delegation grant (operator, POST /v1/grants) and returns its id.
func issueGrant(t *testing.T, client *http.Client, apiURL, session, csrf, agent string, resources []string, stepBudget int) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent": agent, "label": "e2e", "resources": resources, "verbs": []string{"exec"},
		"ttlSeconds": 3600, "maxStepsPerRun": 10, "maxConcurrentRuns": 4, "stepBudget": stepBudget,
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("issue grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue grant: status %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" {
		t.Fatalf("decode grant: %v (id=%q)", err, out.ID)
	}
	return out.ID
}

// callRunLaunch invokes the run.launch MCP tool and returns the tool result text + isError.
// It retries a still-compiling stack (a not-ready downstream surfaces as a JSON-RPC error).
func callRunLaunch(t *testing.T, client *http.Client, mcpURL, tok, grantID string, steps []map[string]string) (string, bool) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"grantId": grantID, "steps": steps})
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run.launch","arguments":` + string(args) + `}}`
	deadline := time.Now().Add(180 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", strings.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpc)
			_ = resp.Body.Close()
			if rpc.Error == nil && len(rpc.Result) > 0 {
				var res struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				}
				if json.Unmarshal(rpc.Result, &res) == nil && len(res.Content) > 0 {
					return res.Content[0].Text, res.IsError
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("run.launch never returned a result (last err=%v)", err)
		}
		time.Sleep(3 * time.Second)
	}
}

// callRunLaunchOK invokes run.launch, asserts success, and extracts the new run id from the
// tool result text ("launched run <id> (<status>)").
func callRunLaunchOK(t *testing.T, client *http.Client, mcpURL, tok, grantID string, steps []map[string]string) string {
	t.Helper()
	text, isErr := callRunLaunch(t, client, mcpURL, tok, grantID, steps)
	if isErr {
		t.Fatalf("run.launch returned a tool error: %q", text)
	}
	fields := strings.Fields(text) // "launched run <id> (<status>)"
	if len(fields) < 3 || fields[0] != "launched" {
		t.Fatalf("unexpected run.launch result %q, want 'launched run <id> ...'", text)
	}
	return fields[2]
}

// pollEvidenceOutcomeEv waits for an event matching (evidencePacketId, outcome) and returns it.
func pollEvidenceOutcomeEv(t *testing.T, auditURL, token, evidence, outcome string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.EvidencePacketID == evidence && e.Outcome == outcome {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %s/%s never reached the chain within %s", evidence, outcome, within)
		}
		time.Sleep(2 * time.Second)
	}
}
