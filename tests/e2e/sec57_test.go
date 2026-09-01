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
//   - CONTROL: a token scoped to flaggedResourceID, calling flaggedResourceID (matches).
//     ⚠ Commander review (2026-09-01) of the first cut of this test found the control
//     ran against the UNFLAGGED resourceID: under CON-4's fallback, a matched-scope call
//     against an unflagged resource executes with NO rule at all (v0.14.0 behaviour),
//     so that control passed identically whether or not the rule ever fired — it could
//     not distinguish "the rule fired and was correctly overridden" from "the rule never
//     fired." Fixed by moving the control to flaggedResourceID (requires_approval=true):
//     decideHandler (services/policy-service/internal/server/server.go) only reaches the
//     resource's own requires_approval label when the evaluator returns ok=false (CON-4);
//     a MATCHED rule's outcome — allow here — is used unconditionally, so only an ACTIVE
//     allow decision can make a flagged resource execute instead of park. The
//     discriminating assertion is the audit-chain policy.decision:allow event itself
//     (the Commander-approved alternative instrument, the same shape WD-15 already uses
//     for its positive half) — not merely that the call returned fast, which is a timing
//     signal that could be misread. A behavioral corroboration (isError:false, expected
//     stdout) is kept alongside it, but is not the proof.
//   - PROOF (SEC-57 itself, unchanged by the review): the SAME rule, a token scoped to a
//     DIFFERENT resource (flaggedResourceID) calling resourceID. If SEC-16 is bypassable
//     by an `allow` rule, this call succeeds — that is the live authority-widening defect
//     this test exists to catch. The assertion checks the EXACT SEC-16 refusal message
//     (catalog.go:884), not merely that some error came back — a refusal for an unrelated
//     reason would prove nothing about SEC-57.
//
// ⚠ If the PROOF leg ever succeeds, or is refused with any message other than the named
// one, that is a live security defect, not a test bug: it must be reported, not silently
// patched around (T-155 brief). Equally, if the CONTROL leg's policy.decision:allow never
// appears, that means the rule did not fire — the PROOF leg's refusal would then be
// unexplained by SEC-57 at all, and must be reported rather than accepted.
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

	agentID := "sec57-" + hex.EncodeToString(randBytes(t, 4))

	// The wildcard-allow rule: maximally permissive for this agent's remote.exec calls
	// on ANY target — exactly the shape SEC-57 says must never be able to widen what
	// the calling token's own scope withholds. Target: "*" already matches a flagged
	// resource, so the rule needs no change for the corrected control below.
	ruleID := createPolicyRule(t, client, apiURL, sessionToken, csrf, policyRuleBody{
		Label: "sec57-wildcard-allow-" + agentID, Subject: "agent:" + agentID,
		Edge: "agent-remote-session", Action: "remote.exec", Target: "*",
		Outcome: "allow",
	})
	t.Logf("authored rule %s: agent:%s x agent-remote-session x remote.exec x * -> allow", ruleID, agentID)
	defer deletePolicyRule(t, client, apiURL, sessionToken, csrf, ruleID)

	// --- CONTROL: token scope MATCHES the call's target, AND the call targets the
	// requires_approval-FLAGGED resource (flaggedResourceID), not the ungated
	// resourceID — only a flagged resource can distinguish "an active allow decision
	// fired" from "no rule matched, fall back to today's default." ---
	controlTok := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, agentID, flaggedResourceID)
	const wantOut = "SEC57-CONTROL-OK"
	controlOut := make(chan mcpToolResult, 1)
	controlErr := make(chan error, 1)
	go fireHeldInvoke(mcpURL, controlTok, flaggedResourceID, "echo "+wantOut, controlOut, controlErr)

	// Recover the control invocation's id from tool.invoked (chained before any policy
	// decision, catalog.go's runGovernedStep: Create() precedes Decide()). Filtered by
	// Target=resource:flaggedResourceID so it cannot collide with the PROOF leg's
	// invocation below, run under the same agent subject moments later against a
	// different resource.
	var controlInvID string
	invDeadline := time.Now().Add(30 * time.Second)
	for controlInvID == "" {
		for _, e := range listAllEvents(t, auditURL, auditToken) {
			if e.Actor == "agent:"+agentID && e.Outcome == "tool.invoked" && e.Target == "resource:"+flaggedResourceID {
				controlInvID = strings.TrimPrefix(e.EvidencePacketID, "invocation:")
				break
			}
		}
		if controlInvID == "" {
			if time.Now().After(invDeadline) {
				t.Fatal("tool.invoked for the control leg (flagged resource) never reached the chain")
			}
			time.Sleep(2 * time.Second)
		}
	}

	// THE discriminating assertion: an ACTIVE policy.decision:allow was recorded for
	// this invocation. If the rule had never matched (wrong subject, evaluator not
	// consulted, etc.), decideHandler's CON-4 fallback would instead chain
	// policy.decision:requires_approval and park the call — this poll would time out
	// and fail loudly, exactly what the pre-fix control could never do.
	allowEv := pollEvidenceOutcome(t, auditURL, auditToken, "session:"+controlInvID, "policy.decision:allow", 30*time.Second)
	if allowEv.Actor != "agent:"+agentID || allowEv.Edge != "agent-remote-session" || allowEv.Target != "resource:"+flaggedResourceID {
		t.Fatalf("control leg: policy.decision:allow fields wrong: %+v", allowEv)
	}
	t.Logf("PASS control — an ACTIVE policy.decision:allow was recorded for the flagged-resource call (actor=%s target=%s): the wildcard rule fired and bypassed the approval park, not a no-rule fallback", allowEv.Actor, allowEv.Target)

	// Behavioral corroboration (not the proof): the call also actually executed rather
	// than parking — consistent with the recorded allow decision.
	var control mcpToolResult
	select {
	case control = <-controlOut:
	case err := <-controlErr:
		t.Fatalf("control leg failed at transport: %v", err)
	case <-time.After(60 * time.Second):
		t.Fatal("control leg (matched scope, flagged resource) did not return")
	}
	if control.IsError || len(control.Content) == 0 || !strings.Contains(control.Content[0].Text, wantOut) {
		t.Fatalf("control leg: call did not execute despite a recorded allow decision: %+v", control)
	}
	t.Log("PASS control corroboration — the call executed (isError:false, expected stdout), not a park")

	// --- PROOF (SEC-57): the SAME rule, token scoped to a DIFFERENT resource
	// (flaggedResourceID), calling resourceID. Unchanged by the control fix. ---
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
