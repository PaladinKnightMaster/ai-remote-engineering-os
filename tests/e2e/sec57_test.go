//go:build e2e

// TestSEC57RuleAllowCannotWidenTokenScope — SEC-57 (Commander ruling 2026-09-01,
// CEO-approved; T-155). SEC-57 reads: "a rule cannot widen authority. A rule whose
// outcome is `allow` does not grant an agent a verb its grant or token scope
// withholds — grants and rules are both floors, never ceilings for each other." It is
// `block` in criteria §5 beside SEC-55/56/58, and unlike them had zero dedicated tests
// anywhere in the repo (v0.15.0-security-review.md §f): the claim it holds rested on
// architectural reasoning — Service.Invoke runs the SEC-16 scope check
// (catalog.go:881-901) BEFORE runGovernedStep, so a rule's `allow` outcome has no code
// path back to the token's own scope — never made to fail. This test proves that, not
// just argues it.
//
// Two legs against the SAME wildcard-target `allow` rule (subject scoped to this run's
// random agent, WD-15's convention, so it cannot interfere with any other e2e test
// sharing resourceID/flaggedResourceID):
//
//   - CONTROL: a token scoped to resourceID (matches what it calls) — the wildcard
//     allow rule actually permits execution. Proves the rule is a live `allow`, not an
//     accidental no-op, so the PROOF leg's refusal below cannot be explained away as
//     "the rule secretly denied it too."
//   - PROOF (SEC-57 itself): the SAME rule, a token scoped to a DIFFERENT resource
//     (flaggedResourceID) calling resourceID. If SEC-16 is bypassable by an `allow`
//     rule, this call succeeds — that is the live authority-widening defect this test
//     exists to catch. The assertion checks the EXACT SEC-16 refusal message
//     (catalog.go:884), not merely that some error came back — a refusal for an
//     unrelated reason would prove nothing about SEC-57.
//
// ⚠ If the PROOF leg ever succeeds, or is refused with any message other than the
// named one, that is a live security defect, not a test bug: it must be reported, not
// silently patched around (T-155 brief).
package e2e

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSEC57RuleAllowCannotWidenTokenScope(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
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

	agentID := "sec57-" + hex.EncodeToString(randBytes(t, 4))

	// The wildcard-allow rule: maximally permissive for this agent's remote.exec calls
	// on ANY target — exactly the shape SEC-57 says must never be able to widen what
	// the calling token's own scope withholds.
	ruleID := createPolicyRule(t, client, apiURL, sessionToken, csrf, policyRuleBody{
		Label: "sec57-wildcard-allow-" + agentID, Subject: "agent:" + agentID,
		Edge: "agent-remote-session", Action: "remote.exec", Target: "*",
		Outcome: "allow",
	})
	t.Logf("authored rule %s: agent:%s x agent-remote-session x remote.exec x * -> allow", ruleID, agentID)
	defer deletePolicyRule(t, client, apiURL, sessionToken, csrf, ruleID)

	// --- CONTROL: token scope MATCHES the call's target. Establishes the rule is a
	// live allow (not an accidental no-op) before the PROOF leg relies on that fact. ---
	matchedTok := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, agentID, resourceID)
	const wantOut = "SEC57-CONTROL-OK"
	controlOut := make(chan mcpToolResult, 1)
	controlErr := make(chan error, 1)
	go fireHeldInvoke(mcpURL, matchedTok, resourceID, "echo "+wantOut, controlOut, controlErr)
	var control mcpToolResult
	select {
	case control = <-controlOut:
	case err := <-controlErr:
		t.Fatalf("control leg failed at transport: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("control leg (matched scope) did not return")
	}
	if control.IsError || len(control.Content) == 0 || !strings.Contains(control.Content[0].Text, wantOut) {
		t.Fatalf("control leg: wildcard allow rule did not permit a scope-matched call: %+v (rule is not live — the PROOF leg below would be meaningless)", control)
	}
	t.Log("PASS control — matched scope + wildcard allow rule: call executed (the rule is a live allow, not a no-op)")

	// --- PROOF (SEC-57): the SAME rule, token scoped to a DIFFERENT resource
	// (flaggedResourceID), calling resourceID. ---
	mismatchedTok := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, agentID, flaggedResourceID)
	proofOut := make(chan mcpToolResult, 1)
	proofErr := make(chan error, 1)
	go fireHeldInvoke(mcpURL, mismatchedTok, resourceID, "echo SEC57-SHOULD-NEVER-RUN", proofOut, proofErr)
	var proof mcpToolResult
	select {
	case proof = <-proofOut:
	case err := <-proofErr:
		t.Fatalf("proof leg failed at transport: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("proof leg (mismatched scope) did not return")
	}
	// The exact string toolError("resource is outside the agent token's scope") emits
	// at catalog.go:884 — SEC-16's resource-scope branch specifically, not the
	// operation-scope branch (:897/:900) and not a policy-evaluator deny.
	const wantMsg = "resource is outside the agent token's scope"
	if !proof.IsError {
		t.Fatalf("SEC-57 VIOLATION: a wildcard allow rule widened a token's scope — the scope-mismatched call SUCCEEDED: %+v", proof)
	}
	if len(proof.Content) == 0 || proof.Content[0].Text != wantMsg {
		t.Fatalf("SEC-57: call was refused, but NOT by the named SEC-16 gate — got %+v, want exact message %q (a different refusal proves nothing about SEC-57)", proof, wantMsg)
	}
	t.Logf("PASS SEC-57 — wildcard allow rule + scope-mismatched token: refused by the SEC-16 gate (catalog.go:884) with the exact message %q, before the policy evaluator is ever reached", wantMsg)
}
