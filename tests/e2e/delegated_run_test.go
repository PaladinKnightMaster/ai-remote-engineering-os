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

// The delegated-launch HTTP helpers (issueGrant / revokeGrantHTTP / callRunLaunch /
// callRunLaunchOK) moved to delegated_driver.go at T-068 (tag e2e || wd1) so the
// installed-stack WD-6 driver can reuse them (the T-061 run_driver.go pattern).

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
