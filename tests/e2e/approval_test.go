//go:build e2e

// TestGovernedEgressApprovalEndToEnd — the WD-2 live approval proof (T-033a), the
// G1 capstone of v0.2.0 "Governed Egress Foundation". It drives the full governed
// wedge on real wires with NO mock at any boundary against the go-run compose
// stack (infra/docker-compose/docker-compose.e2e.yml, `task e2e`):
//
//	login → create a session against a requires_approval-flagged resource (api
//	parks it pending_approval + consults policy-service: ADR-0054) → operator
//	approve via POST /v1/sessions/{id}/resolve → broker WS → type a command →
//	read the output back over the live PTY → end → assert the LIVE chain carries
//	policy.decision:requires_approval + approval.granted + credential.leased +
//	session.open + terminal.input.line + session.close (cast SHA) — every
//	boundary mocked during T-031 (api↔policy decide/resolve, the policy outbox→
//	audit relay) proven live, on top of the T-030b connector lease.
//
// The DENY leg proves WD-2.3's other half: a second parked session is denied →
// api answers closed (approval_denied), approval.denied:operator chains, and the
// broker refuses the WS attach (the action is BLOCKED, not just relabeled). No
// session.open/close is expected for a denied session: it never opens, and
// api's deny close is status-only (the audit record is policy's).
//
// Shared harness helpers live in harness_test.go (build tag e2e || wd1).
package e2e

import (
	"context"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestGovernedEgressApprovalEndToEnd(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	brokerWS := env("E2E_BROKER_WS", "ws://broker:8082")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	castDir := env("AIREOS_CAST_DIR", "/var/lib/aireos/sessions")
	operator := "operator:" + operatorUser

	client := &http.Client{Timeout: 15 * time.Second}

	// --- 1. login (retry: the go-run stack may still be compiling) --------------
	var sessionToken, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		session, c, status, err := doLogin(client, apiURL)
		if err == nil && status == http.StatusOK && session != "" && c != "" {
			sessionToken, csrf = session, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("login ok — captured session + csrf cookies")

	// --- 2. APPROVE leg: create against the flagged resource → parked -----------
	sessionID := createSessionFor(t, client, apiURL, sessionToken, csrf, flaggedResourceID, "pending_approval")
	t.Logf("created session %s (pending_approval — parked, ADR-0054)", sessionID)

	// --- 3. operator approves; api flips the parked session to 'pending' --------
	if got := resolveSession(t, client, apiURL, sessionID, sessionToken, csrf, "approve"); got != "pending" {
		t.Fatalf("approve: session status = %q, want pending", got)
	}
	t.Log("approved — session released to the broker (pending)")

	// --- 4. broker WS + live PTY round-trip (identical to the ungated path) -----
	wsURL := brokerWS + "/v1/sessions/" + sessionID + "/channel"
	wsHeader := http.Header{"Cookie": []string{sessionCookieName + "=" + sessionToken}}
	ws := dialWS(t, wsURL, wsHeader)
	defer ws.CloseNow()

	marker := "WD2OK-" + hex.EncodeToString(randBytes(t, 6))
	cmdLine := "echo " + marker
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte(cmdLine+"\r")); err != nil {
		t.Fatalf("ws write command: %v", err)
	}
	if !readUntilMarker(ws, marker, 20*time.Second) {
		t.Fatal("command output never returned over the PTY — the approved session's SSH round-trip failed")
	}
	t.Logf("command %q executed over the approved session", cmdLine)

	// --- 5. end the session, then assert the FULL governed chain ----------------
	endSession(t, client, apiURL, sessionID, sessionToken, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "wd2 approve leg done")

	auditToken := mintAuditToken(t)
	evidence := "session:" + sessionID

	// 5a. policy decision + approval, anchored to this session (ADR-0054 §3).
	decEv := pollEvidenceOutcome(t, auditURL, auditToken, evidence, "policy.decision:requires_approval", 45*time.Second)
	if decEv.Actor != operator || decEv.Edge != "agent-remote-session" || decEv.Target != "resource:"+flaggedResourceID {
		t.Fatalf("policy.decision event fields wrong: %+v", decEv)
	}
	grantEv := pollEvidenceOutcome(t, auditURL, auditToken, evidence, "approval.granted", 30*time.Second)
	if grantEv.Actor != operator {
		t.Fatalf("approval.granted actor = %q, want %s (same-operator self-approve, ADR-0054)", grantEv.Actor, operator)
	}

	// 5b. the governed egress: the credential was leased from connector for the
	// FLAGGED resource, anchored to this session (T-030b path, now behind approval).
	leasedEv := pollLeased(t, auditURL, auditToken, "resource:"+flaggedResourceID, 30*time.Second)
	if leasedEv.Actor != operator || leasedEv.EvidencePacketID != evidence {
		t.Fatalf("credential.leased fields wrong: %+v", leasedEv)
	}

	// 5c. session lifecycle + cast anchor (the v0.1.0 wedge, unchanged by the gate).
	events := pollChain(t, auditURL, auditToken, evidence, 45*time.Second)
	assertOutcome(t, events, "opened", "")
	assertOutcome(t, events, "input.line", "sha256:"+sha256Hex([]byte(cmdLine)))
	closeEv := assertOutcome(t, events, "closed:operator", "")
	castSHA := strings.TrimPrefix(closeEv.EvidencePacketID, "sha256:")
	if castSHA == "" || castSHA == closeEv.EvidencePacketID {
		t.Fatalf("session.close evidencePacketId not a sha256 anchor: %q", closeEv.EvidencePacketID)
	}
	castBytes, err := os.ReadFile(filepath.Join(castDir, sessionID+".cast"))
	if err != nil {
		t.Fatalf("read cast file: %v", err)
	}
	if got := sha256Hex(castBytes); got != castSHA {
		t.Fatalf("cast file SHA mismatch: on-disk %s != chain-anchored %s", got, castSHA)
	}
	t.Log("PASS approve leg — live chain: policy.decision:requires_approval + approval.granted + credential.leased + session.open + terminal.input.line + session.close (cast SHA verified)")

	// --- 6. DENY leg: parked → denied → blocked + chained ------------------------
	deniedID := createSessionFor(t, client, apiURL, sessionToken, csrf, flaggedResourceID, "pending_approval")
	if got := resolveSession(t, client, apiURL, deniedID, sessionToken, csrf, "deny"); got != "closed" {
		t.Fatalf("deny: session status = %q, want closed", got)
	}
	deniedEvidence := "session:" + deniedID
	pollEvidenceOutcome(t, auditURL, auditToken, deniedEvidence, "policy.decision:requires_approval", 30*time.Second)
	denyEv := pollEvidenceOutcome(t, auditURL, auditToken, deniedEvidence, "approval.denied:operator", 30*time.Second)
	if denyEv.Actor != operator {
		t.Fatalf("approval.denied:operator actor = %q, want %s", denyEv.Actor, operator)
	}

	// The denied action is BLOCKED: the broker must refuse the WS attach (its
	// status guard only admits 'pending'), and no session.open may ever chain.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, _, err := websocket.Dial(ctx, brokerWS+"/v1/sessions/"+deniedID+"/channel",
		&websocket.DialOptions{HTTPHeader: wsHeader}); err == nil {
		_ = c.CloseNow()
		t.Fatal("denied session accepted a WS attach — the approval gate is NOT enforcing")
	}
	for _, e := range listAllEvents(t, auditURL, auditToken) {
		if e.Target == deniedEvidence && e.Outcome == "opened" {
			t.Fatalf("denied session has a session.open in the chain: %+v", e)
		}
	}
	t.Log("PASS deny leg — approval.denied:operator chained; WS attach refused; no session.open")
}
