//go:build e2e

// TestSSHCockpitEndToEnd — the live SSH cockpit end-to-end proof (T-020b), the
// G1 verification for the SSH pillar and the closure of ADR-0045 §Verification
// item 3. It drives the real wire path with NO mock at any boundary against the
// go-run compose stack (infra/docker-compose/docker-compose.e2e.yml, `task e2e`):
//
//	login (cookie) → create session → broker WebSocket (cookie auth) → type a
//	command → read the output back over the live PTY → end → assert the LIVE
//	audit chain (session.open + terminal.input.line + session.close w/ cast SHA)
//	delivered through the real outbox relay; the on-disk cast SHA matches; and
//	api's own relay delivered auth.login.success (T-022 / SEC-9).
//
// Shared harness helpers live in harness_test.go (build tag `e2e || wd1`).
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

func TestSSHCockpitEndToEnd(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	brokerWS := env("E2E_BROKER_WS", "ws://broker:8082")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	castDir := env("AIREOS_CAST_DIR", "/var/lib/aireos/sessions")

	// No cookiejar: api sets Secure cookies (HTTPS production posture) and Go's
	// cookiejar drops Secure cookies over plain http://. This harness is cleartext
	// on the compose network, so we capture + replay the Set-Cookie values.
	client := &http.Client{Timeout: 15 * time.Second}

	// --- 1. login (retry: api compiles via `go run` and may not be listening) ---
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

	// --- 2. create session ------------------------------------------------------
	sessionID := createSession(t, client, apiURL, sessionToken, csrf)
	t.Logf("created session %s (pending)", sessionID)

	// --- 3. open the broker WebSocket (cookie auth, ADR-0048 §4 / ADR-0050) ------
	wsURL := brokerWS + "/v1/sessions/" + sessionID + "/channel"
	wsHeader := http.Header{"Cookie": []string{sessionCookieName + "=" + sessionToken}}
	ws := dialWS(t, wsURL, wsHeader)
	defer ws.CloseNow()
	assertWSRejectsWithoutAuth(t, wsURL) // ADR-0050 §2 regression guard

	// --- 4/5. type a command, read its output back over the live PTY ------------
	marker := "E2EOK-" + hex.EncodeToString(randBytes(t, 6))
	cmdLine := "echo " + marker
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte(cmdLine+"\r")); err != nil {
		t.Fatalf("ws write command: %v", err)
	}
	if !readUntilMarker(ws, marker, 20*time.Second) {
		t.Fatal("command output (the echoed marker) never came back over the PTY — the SSH round-trip did not complete")
	}
	t.Logf("command %q executed; marker observed in PTY output", cmdLine)

	// --- 6. end the session (primary UI/REST path) then close the WS ------------
	endSession(t, client, apiURL, sessionID, sessionToken, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "e2e done")

	// --- 7. assert the LIVE audit chain via audit-service GET /v1/events ---------
	auditToken := mintAuditToken(t)
	expectedLineSHA := sha256Hex([]byte(cmdLine))
	events := pollChain(t, auditURL, auditToken, "session:"+sessionID, 45*time.Second)

	assertOutcome(t, events, "opened", "")
	assertOutcome(t, events, "input.line", "sha256:"+expectedLineSHA)
	closeEv := assertOutcome(t, events, "closed:operator", "")
	castSHA := strings.TrimPrefix(closeEv.EvidencePacketID, "sha256:")
	if castSHA == "" || castSHA == closeEv.EvidencePacketID {
		t.Fatalf("session.close evidencePacketId not a sha256 anchor: %q", closeEv.EvidencePacketID)
	}

	// --- 8. the on-disk cast file's SHA256 matches the chain anchor --------------
	castPath := filepath.Join(castDir, sessionID+".cast")
	castBytes, err := os.ReadFile(castPath)
	if err != nil {
		t.Fatalf("read cast file %s: %v", castPath, err)
	}
	if got := sha256Hex(castBytes); got != castSHA {
		t.Fatalf("cast file SHA mismatch: on-disk %s != chain-anchored %s", got, castSHA)
	}
	t.Logf("PASS — live chain has session.open + terminal.input.line + session.close; cast SHA matches")

	// --- 9. api-side auth audit (T-022 / SEC-9) ---------------------------------
	authEv := pollAuthEvent(t, auditURL, auditToken, "auth.login.success", "operator:"+operatorUser, 30*time.Second)
	t.Logf("PASS — api auth event in live chain: %q by %q (edge %q)", authEv.Outcome, authEv.Actor, authEv.Edge)
}
