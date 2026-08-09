//go:build e2e

// WD-11 — the v0.12.0 pairing walkthrough (T-121, docs/releases/v0.12.0 §2 GATE-1;
// ADR-0068 §D9): request -> redeem -> approve from the paired session -> revoke from
// the desk, over real HTTP against the compose stack, with the audit chain verifying
// intact end to end. Follows wd10_test.go's shape. The mandatory negative leg
// (SEC-34/SEC-35) lives in wd11_negatives_test.go (T-121b), per ADR-0068 §D9's "a
// capability claim is proven by what it denies" (the SEC-21 precedent).
//
// The QR itself is NOT this test's job — T-115a's smoke spec covers rendering. Here
// the equivalent is constructing and posting the <id>.<secret> the fragment would
// carry (pairing_driver.go's createPairingRequest/redeemPairingRequest).
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWD11PairingWalkthrough(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	deskSession, deskCSRF, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("desk login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	operator := "operator:" + operatorUser

	// --- Leg 1: the desk requests a pairing (GOV-12: 32 bytes of entropy, hashed at
	// rest, the plaintext returned exactly once) -------------------------------------
	label := "wd11-phone-" + w9suffix()
	reqID, secret := createPairingRequest(t, client, apiURL, deskSession, deskCSRF, label)
	reqEv := pollTargetOutcome(t, auditURL, auditTok, "pairing:"+reqID, "pairing.requested", 30*time.Second)
	if reqEv.Actor != operator || reqEv.Edge != "human-agent" {
		t.Fatalf("pairing.requested = {actor:%q edge:%q}, want {%s human-agent}", reqEv.Actor, reqEv.Edge, operator)
	}
	t.Logf("PASS WD-11 leg 1 — desk requested pairing %s; pairing.requested chained (AUD-32)", reqID)

	// --- Leg 2: the device redeems the fragment-carried secret (GOV-13: never a query
	// param) for a durable, revocable, scope-narrowed session (ADR-0068 §D4/§D5) ------
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, apiURL, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" || pairedCSRF == "" {
		t.Fatalf("redeem: status=%d session-empty=%v csrf-empty=%v", redeemStatus, pairedSession == "", pairedCSRF == "")
	}
	sessions := listPairedSessionsHTTP(t, client, apiURL, deskSession, deskCSRF)
	var pairedID string
	for _, s := range sessions {
		if s.Label == label && s.RevokedAt == nil {
			pairedID = s.ID
		}
	}
	if pairedID == "" {
		t.Fatalf("no live paired session labeled %q in the desk's list: %+v", label, sessions)
	}
	redeemEv := pollTargetOutcome(t, auditURL, auditTok, "paired-session:"+pairedID, "pairing.redeemed", 30*time.Second)
	if redeemEv.Actor != operator || redeemEv.Edge != "human-agent" {
		t.Fatalf("pairing.redeemed = {actor:%q edge:%q}, want {%s human-agent}", redeemEv.Actor, redeemEv.Edge, operator)
	}
	t.Logf("PASS WD-11 leg 2 — the device redeemed the secret for paired session %s (scope approvals@1); pairing.redeemed chained (AUD-32)", pairedID)

	// --- Leg 3: a held invocation parks, and the PAIRED session — not the desk —
	// approves it (AUD-34: the approval must be attributable as paired-origin) -------
	tok := mintAgentTokenViaAPI(t, client, apiURL, deskSession, deskCSRF, "wd11-agent-"+w9suffix(), flaggedResourceID)
	const wantOut = "WD11-PAIRED-APPROVE-OK"
	out := make(chan mcpToolResult, 1)
	errc := make(chan error, 1)
	go fireHeldInvoke(mcpURL, tok, flaggedResourceID, "echo "+wantOut, out, errc)
	inv := pollPendingInvocation(t, client, apiURL, deskSession, deskCSRF, flaggedResourceID, 150*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, pairedSession, pairedCSRF, "approve")
	t.Logf("approved invocation %s FROM THE PAIRED SESSION (not the desk)", inv.ID)

	var res mcpToolResult
	select {
	case res = <-out:
	case err := <-errc:
		t.Fatalf("held invoke failed: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the held agent call did not return after the paired approval")
	}
	if res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, wantOut) {
		t.Fatalf("approved tool result = %+v, want stdout containing %q (isError false)", res, wantOut)
	}

	grantEv := pollEvidenceOutcome(t, auditURL, auditTok, "session:"+inv.ID, "approval.granted", 45*time.Second)
	wantActor := operator + "#paired"
	if grantEv.Actor != wantActor {
		t.Fatalf("AUD-34: approval.granted actor = %q, want %q (the paired-origin marker; an exact-match filter for %q will NOT see this event by design)", grantEv.Actor, wantActor, operator)
	}
	t.Logf("PASS WD-11 leg 3 — the held invocation was approved FROM THE PAIRED SESSION; approval.granted actor=%q proves AUD-34's actor fork live", grantEv.Actor)

	// --- Leg 4: the desk revokes the paired session — the product's first revocable
	// operator session (ADR-0068 §D4) -------------------------------------------------
	revokePairedSessionHTTP(t, client, apiURL, deskSession, deskCSRF, pairedID)
	revokeEv := pollTargetOutcome(t, auditURL, auditTok, "paired-session:"+pairedID, "pairing.revoked", 30*time.Second)
	if revokeEv.Actor != operator || revokeEv.Edge != "human-agent" {
		t.Fatalf("pairing.revoked = {actor:%q edge:%q}, want {%s human-agent}", revokeEv.Actor, revokeEv.Edge, operator)
	}
	t.Logf("PASS WD-11 leg 4 — the desk revoked paired session %s from the desk; pairing.revoked chained (AUD-32)", pairedID)
	t.Log("PASS WD-11 — request -> redeem -> approve from the paired session -> revoke from the desk; pairing.requested/redeemed/revoked all chained; AUD-34's fork proven live (audit-service verify runs chain-intact at the suite's end)")
}
