//go:build e2e

// TestGatewayGovernedExecApprovalPath — the WD-3 live capstone (T-041), the v0.3.0
// "Typed MCP Gateway" G1. It drives the FULL invocation-approval wedge on real wires
// with no mocked boundary against the go-run compose stack (docker-compose.e2e.yml,
// `task e2e`):
//
//	operator login → operator PROVISIONS a scoped agent-tool token (POST
//	/v1/agent-tokens) → the agent calls remote.exec on a requires_approval-flagged
//	resource → the gateway opens an invocation, policy decides requires_approval, and
//	the gateway PARKS + BLOCKS the agent's call (block-and-wait) → the operator sees
//	the parked invocation WITH its command (GET /v1/invocations) and APPROVES it (api
//	→ gateway proxied) → the gateway claims it, the broker leases + runs the command
//	on the real sshd, and the held agent call returns the stdout → the chain is
//	asserted: tool.invoked → policy.decision:requires_approval → approval.granted →
//	credential.leased → remote.exec (stdout-SHA == the returned stdout) → tool.result.
//
// Anchors span TWO namespaces by design (opaque-key reuse, no policy/connector
// change, ADR-0057/0058): the gateway + broker anchor invocation:<uuid>; policy +
// connector anchor session:<uuid>. Both carry the SAME uuid (taken from the operator
// list). The DENY leg proves the block: a second parked call is denied → the agent
// gets a typed tool-error, NO remote.exec runs, approval.denied:operator chains.
//
// Whole-chain hash integrity (AUD-11) is verified by `audit-service verify` appended
// to the tester command (docker-compose.e2e.yml).
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// mcpToolResult + fireHeldInvoke moved to harness_test.go (build tag e2e || wd1) so
// the installed wd1 WD-3 driver (T-043a) reuses them.

func TestGatewayGovernedExecApprovalPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	operator := "operator:" + operatorUser

	client := &http.Client{Timeout: 15 * time.Second}

	// --- 1. operator login (retry: the go-run stack may still be compiling) ------
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
	t.Log("login ok")
	auditToken := mintAuditToken(t)

	// --- 2. APPROVE leg: operator provisions an agent token, the agent parks ------
	tok := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, "e2e", flaggedResourceID)
	const wantOut = "WD3-APPROVE-OK"
	out := make(chan mcpToolResult, 1)
	errc := make(chan error, 1)
	go fireHeldInvoke(mcpURL, tok, flaggedResourceID, "echo "+wantOut, out, errc)

	// 3. operator discovers the parked invocation (with its command) and approves it.
	inv := pollPendingInvocation(t, client, apiURL, sessionToken, csrf, flaggedResourceID, 150*time.Second)
	if inv.Agent != "agent:e2e" || inv.Command != "echo "+wantOut {
		t.Fatalf("parked invocation fields wrong: %+v", inv)
	}
	resolveInvocation(t, client, apiURL, inv.ID, sessionToken, csrf, "approve")
	t.Logf("approved invocation %s (command %q)", inv.ID, inv.Command)

	// 4. the held agent call unblocks with the executed stdout.
	var res mcpToolResult
	select {
	case res = <-out:
	case err := <-errc:
		t.Fatalf("held invoke failed: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the held agent call did not return after approval")
	}
	if res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, wantOut) {
		t.Fatalf("approved tool result = %+v, want stdout containing %q (isError false)", res, wantOut)
	}
	stdout := res.Content[0].Text

	// 5. assert the governed chain. Dual anchors off the SAME uuid: the gateway +
	// broker anchor invocation:<uuid>; policy + connector anchor session:<uuid>.
	invAnchor := "invocation:" + inv.ID
	sessAnchor := "session:" + inv.ID

	invokedEv := pollEvidenceOutcome(t, auditURL, auditToken, invAnchor, "tool.invoked", 60*time.Second)
	if invokedEv.Actor != "agent:e2e" || invokedEv.Edge != "agent-tool" {
		t.Fatalf("tool.invoked fields wrong: %+v", invokedEv)
	}
	decEv := pollEvidenceOutcome(t, auditURL, auditToken, sessAnchor, "policy.decision:requires_approval", 45*time.Second)
	if decEv.Actor != "agent:e2e" || decEv.Edge != "agent-remote-session" || decEv.Target != "resource:"+flaggedResourceID {
		t.Fatalf("policy.decision fields wrong: %+v", decEv)
	}
	grantEv := pollEvidenceOutcome(t, auditURL, auditToken, sessAnchor, "approval.granted", 45*time.Second)
	if grantEv.Actor != operator {
		t.Fatalf("approval.granted actor = %q, want %s", grantEv.Actor, operator)
	}
	pollLeased(t, auditURL, auditToken, "resource:"+flaggedResourceID, 45*time.Second)
	execEv := pollExecEvent(t, auditURL, auditToken, invAnchor, 45*time.Second)
	wantSHA := sha256.Sum256([]byte(stdout))
	if strings.TrimPrefix(execEv.EvidencePacketID, "sha256:") != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("remote.exec sha %q != sha256(returned stdout)", execEv.EvidencePacketID)
	}
	pollEvidenceOutcome(t, auditURL, auditToken, invAnchor, "tool.result", 45*time.Second)
	t.Log("PASS approve leg — tool.invoked → policy.decision:requires_approval → approval.granted → credential.leased → remote.exec(stdout-SHA) → tool.result")

	// --- 6. DENY leg: a parked call is denied → blocked, typed error, no exec -----
	tok2 := mintAgentTokenViaAPI(t, client, apiURL, sessionToken, csrf, "e2e-deny", flaggedResourceID)
	const denyOut = "WD3-DENY-NEVER"
	out2 := make(chan mcpToolResult, 1)
	errc2 := make(chan error, 1)
	go fireHeldInvoke(mcpURL, tok2, flaggedResourceID, "echo "+denyOut, out2, errc2)

	invD := pollPendingInvocation(t, client, apiURL, sessionToken, csrf, flaggedResourceID, 150*time.Second)
	resolveInvocation(t, client, apiURL, invD.ID, sessionToken, csrf, "deny")
	t.Logf("denied invocation %s", invD.ID)

	select {
	case r := <-out2:
		if !r.IsError {
			t.Fatalf("denied invoke returned a non-error result: %+v (egress must be blocked)", r)
		}
	case err := <-errc2:
		t.Fatalf("denied held invoke errored at transport: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the denied held agent call did not return")
	}

	denyEv := pollEvidenceOutcome(t, auditURL, auditToken, "session:"+invD.ID, "approval.denied:operator", 45*time.Second)
	if denyEv.Actor != operator {
		t.Fatalf("approval.denied:operator actor = %q, want %s", denyEv.Actor, operator)
	}
	// The denied invocation must have NO remote.exec — the egress was blocked.
	for _, e := range listAllEvents(t, auditURL, auditToken) {
		if e.Target == "invocation:"+invD.ID && e.Outcome == "remote.exec" {
			t.Fatalf("denied invocation has a remote.exec — egress was NOT blocked: %+v", e)
		}
	}
	t.Log("PASS deny leg — approval.denied:operator chained; no remote.exec; the agent got a typed deny error")
	t.Log("PASS T-041 — WD-3 live capstone: agent surface → block-and-wait approval → governed exec, full chain on real wires")
}
