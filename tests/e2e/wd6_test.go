//go:build e2e

// WD-6 — the v0.6.0 release gate (T-068, docs/releases/v0.6.0 §2): on the real stack an
// operator delegates and an AGENT autonomously drives a grant-bounded Run end-to-end.
//
// This file supplies the two legs T-065b left open; the other gate facets already live
// in the suite and stay green alongside it:
//
//	N1 out-of-grant denied            → delegated_run_test.go (TestDelegatedRunLaunch)
//	N3 keyless-surface / cannot-mint  → delegated_run_test.go (direct agent→api 401) +
//	                                    wd4_negative_test.go (SEC-21 forge-negative, carried)
//	WD-5 operator-Run regression      → run_engine_test.go (the behavior-preserving guard)
//	chain hash-intact                 → the trailing `audit-service verify` in the tester command
//
// TestWD6HappyPath = facets 1-5: grant issuance → agent run.launch of a mixed plan →
// unattended ungated step → flagged step PARKS with delegated context and a LIVE human
// approves (the park itself is the GOV-5 proof: policy-subject = the agent, so ADR-0054
// same-operator self-approve cannot resolve it under the absent principal) → continue →
// evidence packet (actor + grantId + per-step invocation ids) + delegation provenance in
// the chain.
//
// TestWD6RevokeCancelsInFlight = N2: revoking the grant mid-flight (a step parked)
// CASCADES — the Run is canceled, the parked step finalizes on release (not force-killed),
// and no later step is ever dispatched.
//
// N1's expired-grant variant stays unit-covered (TestValidatePlanAgainstGrant, T-065a);
// the live N1 is the out-of-grant resource + the revoked-grant launch refusal.
package e2e

import (
	"net/http"
	"testing"
	"time"
)

const wd6Agent = "agent:e2e" // same agent identity as the T-065b capstone; distinct grants per test

// TestWD6HappyPath — WD-6 facets 1-5 on live wires. The plan mixes the dedicated
// ungated (d3) and flagged (d4) delegated resources so its park/approve traffic cannot
// pollute the shared-resource audit polls of the other suites (the T-065b isolation
// lesson).
func TestWD6HappyPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// Facet 1 — the operator issues a grant covering BOTH delegated resources (the
	// typed, audited human-agent authorization; CSRF on the route).
	grantID := issueGrant(t, client, apiURL, session, csrf, wd6Agent,
		[]string{delegatedResourceID, delegatedFlaggedResourceID}, 50)

	// Facet 2 — the agent launches a 3-step mixed plan via run.launch (its existing
	// aud=mcp-gateway token; run.launch is control-plane, the grant is the scope).
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", delegatedResourceID)
	runID := callRunLaunchOK(t, client, mcpURL, tok, grantID, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo WD6-H1"},
		{"resourceId": delegatedFlaggedResourceID, "command": "echo WD6-H2"},
		{"resourceId": delegatedResourceID, "command": "echo WD6-H3"},
	})

	// Facet 3 ran unattended (no prompt for step 0); facet 4 — the flagged step PARKS,
	// carrying the delegated context the approving human needs (GOV-6): the agent
	// actor, the delegating principal, and the Run it belongs to, beside the verbatim
	// command (ADR-0059 posture unchanged).
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, delegatedFlaggedResourceID, 120*time.Second)
	if inv.Agent != wd6Agent {
		t.Fatalf("parked delegated step agent = %q, want %s", inv.Agent, wd6Agent)
	}
	if inv.Principal != "operator:admin" {
		t.Fatalf("parked delegated step principal = %q, want operator:admin (FE-18/GOV-6)", inv.Principal)
	}
	if inv.RunID != runID {
		t.Fatalf("parked delegated step runId = %q, want %s (FE-16 run context)", inv.RunID, runID)
	}
	if inv.Command != "echo WD6-H2" {
		t.Fatalf("parked delegated step command = %q, want the verbatim plan command", inv.Command)
	}

	// A LIVE human approves; the held step resumes and the engine CONTINUES to step 2.
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "approve")
	view := waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "completed", 180*time.Second)

	// Facet 5 — the evidence packet: the terminal Run records the delegation
	// provenance (actor = agent, the bounding grant) and every step's invocation id
	// (the read-time bridge into the hash-linked chain, EVD-3).
	if view.Actor != wd6Agent || view.GrantID != grantID {
		t.Fatalf("evidence packet provenance = {actor:%q grant:%q}, want {%s %s}", view.Actor, view.GrantID, wd6Agent, grantID)
	}
	if len(view.Steps) != 3 {
		t.Fatalf("evidence packet: %d steps, want 3", len(view.Steps))
	}
	for _, s := range view.Steps {
		if s.Status != "completed" || s.InvocationID == "" {
			t.Fatalf("step %d = %q/%q, want completed with an invocationId", s.StepIndex, s.Status, s.InvocationID)
		}
	}

	// Delegation provenance in the chain (AUD-16/17): grant.issued actor = the
	// operator on human-agent; run.launched actor = the agent; the delegated step
	// bracket edge is agent-tool (ADR-0062 §4); the approval was granted by the live
	// human, anchored to the parked invocation (dual anchor: session:<id>); the Run
	// closes with run.completed. Hash integrity over the whole chain is proven by the
	// suite's trailing `audit-service verify`.
	issued := pollTargetOutcome(t, auditURL, auditTok, "grant:"+grantID, "grant.issued", 45*time.Second)
	if issued.Actor != "operator:admin" || issued.Edge != "human-agent" {
		t.Fatalf("grant.issued = {actor:%q edge:%q}, want {operator:admin human-agent}", issued.Actor, issued.Edge)
	}
	launched := pollTargetOutcome(t, auditURL, auditTok, "run:"+runID, "run.launched", 45*time.Second)
	if launched.Actor != wd6Agent {
		t.Fatalf("run.launched actor = %q, want %s (delegated)", launched.Actor, wd6Agent)
	}
	step0 := pollEvidenceOutcomeEv(t, auditURL, auditTok, "invocation:"+view.Steps[0].InvocationID, "tool.invoked", 45*time.Second)
	if step0.Actor != wd6Agent || step0.Edge != "agent-tool" {
		t.Fatalf("delegated step tool.invoked = {actor:%q edge:%q}, want {%s agent-tool}", step0.Actor, step0.Edge, wd6Agent)
	}
	granted := pollEvidenceOutcome(t, auditURL, auditTok, "session:"+inv.ID, "approval.granted", 45*time.Second)
	if granted.Actor != "operator:admin" {
		t.Fatalf("approval.granted actor = %q, want operator:admin (a live human approved)", granted.Actor)
	}
	pollTargetOutcome(t, auditURL, auditTok, "run:"+runID, "run.completed", 45*time.Second)
	t.Logf("PASS WD-6 happy path — grant %s → agent run.launch → unattended → flagged park (agent+principal+run context) → live approve → CONTINUE → completed; provenance chained", grantID)
}

// TestWD6RevokeCancelsInFlight — WD-6 N2 on live wires: a grant with an IN-FLIGHT Run
// (its flagged step parked) is revoked; the revocation CASCADES — the Run is canceled
// and no later step ever dispatches. The parked step is then released (denied) and
// finalizes per the running-orphan discipline; the Run STAYS canceled (the
// setRunTerminal fromStatuses guard). Release comes BEFORE the audit polls so a slow
// outbox relay cannot strand the held /v1/steps call past the gateway's block-and-wait
// deadline (the TestRunEngineCancel ordering).
func TestWD6RevokeCancelsInFlight(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	grantID := issueGrant(t, client, apiURL, session, csrf, wd6Agent,
		[]string{delegatedResourceID, delegatedFlaggedResourceID}, 50)
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, "e2e", delegatedResourceID)
	runID := callRunLaunchOK(t, client, mcpURL, tok, grantID, []map[string]string{
		{"resourceId": delegatedResourceID, "command": "echo WD6-R1"},
		{"resourceId": delegatedFlaggedResourceID, "command": "echo WD6-R2"},
		{"resourceId": delegatedResourceID, "command": "echo WD6-R3"},
	})

	// Step 0 runs; step 1 (flagged) parks — the in-flight window the revoke must reach.
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, delegatedFlaggedResourceID, 120*time.Second)

	// The operator revokes the grant. The cascade (DUR-7) runs in the revoke handler:
	// the Run flips to canceled (run.canceled in-tx). Poll over HTTP — the cascade is
	// synchronous but deliberately best-effort.
	revokeGrantHTTP(t, client, apiURL, session, csrf, grantID)
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "canceled", 30*time.Second)

	// Release the parked step (deny) → the held /v1/steps returns and the step
	// finalizes; the Run STAYS canceled (FinishRun is guarded to 'running').
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")

	// The chain carries both halves of the cascade; run.canceled carries the agent as
	// actor (the delegated Run's terminal events attribute the driver).
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+grantID, "grant.revoked", 45*time.Second)
	canceled := pollTargetOutcome(t, auditURL, auditTok, "run:"+runID, "run.canceled", 45*time.Second)
	if canceled.Actor != wd6Agent {
		t.Fatalf("run.canceled actor = %q, want %s (delegated attribution)", canceled.Actor, wd6Agent)
	}

	// The released step finalizes (failed, not stranded) — poll: the sequencer writes
	// it after the held call returns.
	deadline := time.Now().Add(60 * time.Second)
	var view runStatusView
	for {
		view = getRun(t, client, apiURL, session, csrf, runID)
		if len(view.Steps) == 3 && view.Steps[1].Status == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("released step never finalized: %+v", view.Steps)
		}
		time.Sleep(2 * time.Second)
	}

	// The load-bearing N2 assertions: the Run STAYED canceled through the release
	// race, and the post-park step was NEVER dispatched — pending, with no gateway
	// invocation id, so no further egress was possible after the revoke.
	if view.Status != "canceled" {
		t.Fatalf("run status after release = %q, want canceled (the FinishRun guard)", view.Status)
	}
	if view.Steps[2].Status != "pending" || view.Steps[2].InvocationID != "" {
		t.Fatalf("post-revoke step 2 = %q/%q, want pending with NO invocation id (no dispatch after revoke)",
			view.Steps[2].Status, view.Steps[2].InvocationID)
	}
	t.Logf("PASS WD-6 N2 — revoke cascaded to the in-flight Run (grant.revoked + run.canceled); parked step finalized on release; no later step dispatched")
}
