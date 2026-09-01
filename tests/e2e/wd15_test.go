//go:build e2e

// TestWD15RuleDeniedInvocation — WD-15, the v0.15.0 "Governed Decision Policy" live
// capstone proof (T-154, VER-30). It drives the exact case the release exists to
// close (ADR-0074 §Context, criteria §1.2): "an unflagged host runs remote.exec
// ungoverned." resourceID here carries NO requires_approval label — under v0.14.0
// nothing could stop an agent invoking it. This test authors an operator rule that
// denies exactly that (subject, edge, action, target) tuple and proves BOTH halves
// VER-30 requires:
//
//   - NEGATIVE (SEC-55): no credential.leased for this invocation's session anchor,
//     no remote.exec for its invocation anchor — the broker is never reached.
//   - POSITIVE (AUD-36): the denial itself is on the chain, not inferred from an
//     absence — policy.decision:deny AND tool.result:denied both chained, with the
//     right actor/edge/target fields.
//
// The rule is scoped to a per-run random agent subject (SEC-53's operator-authored
// CRUD surface, T-151b) so it cannot interfere with any other e2e test sharing
// resourceID, and is deleted at the end (SEC-54: the decisions it already produced
// stay on the chain regardless) — which doubles as CON-1's live precondition: every
// OTHER test in this package runs against a policy_rules table this test leaves
// empty, exactly as it found it.
package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// policyRuleBody is the operator-facing create payload (services/api/internal/
// server/policy_rules.go createPolicyRuleBody) — local to this file since no other
// e2e test authors a rule yet.
type policyRuleBody struct {
	Label   string `json:"label"`
	Subject string `json:"subject"`
	Edge    string `json:"edge"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Outcome string `json:"outcome"`
}

func createPolicyRule(t *testing.T, client *http.Client, apiURL, session, csrf string, body policyRuleBody) string {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/policy-rules", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create policy rule: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create policy rule: status %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode created policy rule: %v", err)
	}
	if out.ID == "" {
		t.Fatal("create policy rule: empty id")
	}
	return out.ID
}

func deletePolicyRule(t *testing.T, client *http.Client, apiURL, session, csrf, id string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, apiURL+"/v1/policy-rules/"+id, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("delete policy rule: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete policy rule: status %d", resp.StatusCode)
	}
}

func TestWD15RuleDeniedInvocation(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 15 * time.Second}

	var sessionToken, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, apiURL)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sessionToken, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}
	auditToken := mintAuditToken(t)

	agentID := "wd15-" + hex.EncodeToString(randBytes(t, 4))
	tok := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, agentID, resourceID)

	// The rule that turns an ungoverned action governed: resourceID carries no
	// requires_approval label, so under v0.14.0 semantics this would run
	// unconditionally. Scoped to THIS run's exact agent subject so no other test
	// against the shared resourceID can ever match it.
	ruleID := createPolicyRule(t, client, apiURL, sessionToken, csrf, policyRuleBody{
		Label: "wd15-capstone-deny-" + agentID, Subject: "agent:" + agentID,
		Edge: "agent-remote-session", Action: "remote.exec", Target: "resource:" + resourceID,
		Outcome: "deny",
	})
	t.Logf("authored rule %s: agent:%s x agent-remote-session x remote.exec x resource:%s -> deny", ruleID, agentID, resourceID)
	defer deletePolicyRule(t, client, apiURL, sessionToken, csrf, ruleID)

	const wantNever = "WD15-DENY-NEVER"
	out := make(chan mcpToolResult, 1)
	errc := make(chan error, 1)
	go fireHeldInvoke(mcpURL, tok, resourceID, "echo "+wantNever, out, errc)

	var res mcpToolResult
	select {
	case res = <-out:
	case err := <-errc:
		t.Fatalf("held invoke failed at transport: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("the rule-denied held agent call did not return")
	}
	if !res.IsError {
		t.Fatalf("machine-denied invoke returned a non-error result: %+v (egress must be blocked)", res)
	}
	t.Log("agent received a typed deny error (isError:true) — SEC-58: no park, no human-approvable prompt")

	// The invocation id is minted server-side; recover it from tool.invoked, the one
	// event guaranteed to chain before any decision is made.
	var invID string
	invDeadline := time.Now().Add(30 * time.Second)
	for invID == "" {
		for _, e := range listAllEvents(t, auditURL, auditToken) {
			if e.Actor == "agent:"+agentID && e.Outcome == "tool.invoked" {
				invID = strings.TrimPrefix(e.EvidencePacketID, "invocation:")
				break
			}
		}
		if invID == "" {
			if time.Now().After(invDeadline) {
				t.Fatal("tool.invoked for the wd15 agent never reached the chain")
			}
			time.Sleep(2 * time.Second)
		}
	}
	invAnchor := "invocation:" + invID
	sessAnchor := "session:" + invID // dual anchor off the same uuid (ADR-0057/0058)

	// --- POSITIVE half (AUD-36): the denial is chained, never merely inferred ---
	denyEv := pollEvidenceOutcome(t, auditURL, auditToken, sessAnchor, "policy.decision:deny", 30*time.Second)
	if denyEv.Actor != "agent:"+agentID || denyEv.Edge != "agent-remote-session" || denyEv.Target != "resource:"+resourceID {
		t.Fatalf("policy.decision:deny fields wrong: %+v", denyEv)
	}
	resultEv := pollEvidenceOutcome(t, auditURL, auditToken, invAnchor, "tool.result:denied", 30*time.Second)
	if resultEv.Actor != "agent:"+agentID {
		t.Fatalf("tool.result:denied actor = %q, want agent:%s", resultEv.Actor, agentID)
	}
	t.Logf("PASS positive half — policy.decision:deny (actor=%s target=%s) + tool.result:denied both chained", denyEv.Actor, denyEv.Target)

	// --- NEGATIVE half (SEC-55): absence checked directly, anchored to THIS
	// invocation's own uuid so a concurrent test against the shared resourceID
	// cannot accidentally satisfy it. ---
	for _, e := range listAllEvents(t, auditURL, auditToken) {
		if e.EvidencePacketID == sessAnchor && e.Outcome == "credential.leased" {
			t.Fatalf("SEC-55: a rule-denied invocation has a credential.leased: %+v", e)
		}
		if e.Target == invAnchor && e.Outcome == "remote.exec" {
			t.Fatalf("SEC-55: a rule-denied invocation has a remote.exec (broker egress ran): %+v", e)
		}
	}
	t.Log("PASS negative half — no credential.leased, no remote.exec for this invocation")
	t.Log("PASS T-154 WD-15 — an operator-authored rule denies an otherwise-ungoverned action: no lease, no egress, and the denial is chained positively (policy.decision:deny + tool.result:denied)")
}
