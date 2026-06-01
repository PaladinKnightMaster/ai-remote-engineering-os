//go:build e2e

// Package e2e is the live SSH cockpit end-to-end proof (T-020b) — the G1
// verification for the SSH pillar and the closure of ADR-0045 §Verification
// item 3. It drives the real wire path with NO mock or fake at any boundary:
//
//	login (cookie, services/api) → create session (services/api) →
//	open WebSocket (cookie auth, services/session-broker) → type a command →
//	read the command output back over the live PTY → end the session →
//	assert the LIVE audit chain (audit-service GET /v1/events, delivered through
//	the REAL outbox relay) contains session.open + terminal.input.line +
//	session.close, with the cast SHA anchored in the close event, and the
//	on-disk asciinema cast's SHA256 equals that anchor.
//
// Run via `task e2e` (infra/docker-compose/docker-compose.e2e.yml). Tagged
// `e2e` so it never runs in the unit/integration suite (`task test`).
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"aireos.local/packages/go/auth"
)

// Must match tests/e2e/cmd/seed/main.go (resourceID) and docker-compose.e2e.yml
// (operator credentials). sessionCookieName mirrors the cross-service contract
// in packages/go/middleware.SessionCookieName (ADR-0047).
const (
	resourceID        = "01900000-0000-7000-8000-0000000000e2"
	operatorUser      = "admin"
	operatorPass      = "e2e-operator-pw"
	sessionCookieName = "aireos_session"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func TestSSHCockpitEndToEnd(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	brokerWS := env("E2E_BROKER_WS", "ws://broker:8082")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	castDir := env("AIREOS_CAST_DIR", "/var/lib/aireos/sessions")

	// No cookiejar: the api sets Secure cookies (the HTTPS same-origin
	// production posture), and Go's cookiejar drops Secure cookies received over
	// plain http://. The harness runs cleartext on the compose network, so we
	// capture the Set-Cookie values from the login response and replay them
	// manually on subsequent api calls + the WS upgrade.
	client := &http.Client{Timeout: 15 * time.Second}

	// --- 1. login (returns the aireos_session + aireos_csrf cookie values) -----
	// Retry: the api container compiles via `go run` and may not be listening yet.
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

	// --- 2. create session (operator-scoped; cookie + double-submit CSRF) ------
	sessionID := createSession(t, client, apiURL, sessionToken, csrf)
	t.Logf("created session %s (pending)", sessionID)

	// --- 3. open the broker WebSocket (cookie auth, ADR-0048 §4) ---------------
	// The browser cannot set Authorization on a WS upgrade, so the broker
	// translates the aireos_session cookie server-side (CookieToAuthHeader).
	wsURL := brokerWS + "/v1/sessions/" + sessionID + "/channel"
	wsHeader := http.Header{"Cookie": []string{sessionCookieName + "=" + sessionToken}}
	ws := dialWS(t, wsURL, wsHeader)
	defer ws.CloseNow()

	// Regression guard for ADR-0050 §2: the broker must still REJECT an
	// unauthenticated upgrade. The audience fix relaxed which tokens are
	// accepted; this proves it did not loosen the gate to accept none — i.e.
	// auth is still enforced, not accidentally disabled. (The broker is up: the
	// authenticated dial above succeeded.)
	assertWSRejectsWithoutAuth(t, wsURL)

	// --- 4/5. type a command and read its output back over the live PTY --------
	// NB: coder/websocket Read/Write CLOSE the connection if their context is
	// canceled — so we must use one overall-deadline context for the read phase,
	// never short per-read timeouts (which would kill the live socket).
	marker := "E2EOK-" + hex.EncodeToString(randBytes(t, 6))
	cmdLine := "echo " + marker
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte(cmdLine+"\r")); err != nil {
		t.Fatalf("ws write command: %v", err)
	}
	if !readUntilMarker(ws, marker, 20*time.Second) {
		t.Fatal("command output (the echoed marker) never came back over the PTY — the SSH round-trip did not complete")
	}
	t.Logf("command %q executed; marker observed in PTY output", cmdLine)

	// --- 6. end the session (primary UI/REST path) then close the WS -----------
	// api.end flips status='closed' audit-silently; the broker is the
	// authoritative finalizer (finalize-once on cast_sha256 IS NULL, T-020a) and
	// emits session.close + the cast SHA when the WS drops.
	endSession(t, client, apiURL, sessionID, sessionToken, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "e2e done")

	// --- 7. assert the LIVE audit chain via audit-service GET /v1/events -------
	auditToken := mintAuditToken(t)
	expectedLineSHA := sha256Hex([]byte(cmdLine))
	target := "session:" + sessionID

	events := pollChain(t, auditURL, auditToken, target, 45*time.Second)

	assertOutcome(t, events, "opened", "")
	assertOutcome(t, events, "input.line", "sha256:"+expectedLineSHA)
	closeEv := assertOutcome(t, events, "closed:operator", "")

	castSHA := strings.TrimPrefix(closeEv.EvidencePacketID, "sha256:")
	if castSHA == "" || castSHA == closeEv.EvidencePacketID {
		t.Fatalf("session.close evidencePacketId not a sha256 anchor: %q", closeEv.EvidencePacketID)
	}

	// --- 8. the on-disk cast file's SHA256 matches the chain anchor ------------
	castPath := filepath.Join(castDir, sessionID+".cast")
	castBytes, err := os.ReadFile(castPath)
	if err != nil {
		t.Fatalf("read cast file %s: %v", castPath, err)
	}
	if got := sha256Hex(castBytes); got != castSHA {
		t.Fatalf("cast file SHA mismatch: on-disk %s != chain-anchored %s", got, castSHA)
	}
	t.Logf("PASS — live chain has session.open + terminal.input.line + session.close; cast %s SHA matches the anchor", castPath)
}

// --- helpers ---------------------------------------------------------------

type auditEvent struct {
	Actor            string `json:"actor"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Outcome          string `json:"outcome"`
	EvidencePacketID string `json:"evidencePacketId"`
}

type eventPage struct {
	Items []auditEvent `json:"items"`
	Page  struct {
		NextCursor string `json:"nextCursor"`
	} `json:"page"`
}

func doLogin(client *http.Client, apiURL string) (session, csrf string, status int, err error) {
	body, _ := json.Marshal(map[string]string{"username": operatorUser, "password": operatorPass})
	resp, err := client.Post(apiURL+"/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	// resp.Cookies() parses Set-Cookie regardless of the Secure attribute.
	for _, ck := range resp.Cookies() {
		switch ck.Name {
		case sessionCookieName:
			session = ck.Value
		case "aireos_csrf":
			csrf = ck.Value
		}
	}
	return session, csrf, resp.StatusCode, nil
}

// cookieHeader replays both auth cookies on an api request the same way a
// browser would (CookieToAuthHeader uses the session cookie; the CSRF
// double-submit check compares the aireos_csrf cookie to the X-CSRF-Token header).
func cookieHeader(session, csrf string) string {
	return sessionCookieName + "=" + session + "; aireos_csrf=" + csrf
}

func createSession(t *testing.T, client *http.Client, apiURL, session, csrf string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"resourceId": resourceID})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create session: status %d", resp.StatusCode)
	}
	var s struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if s.ID == "" || s.Status != "pending" {
		t.Fatalf("unexpected session: %+v", s)
	}
	return s.ID
}

func endSession(t *testing.T, client *http.Client, apiURL, id, session, csrf string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/sessions/"+id+"/end", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("end session: status %d", resp.StatusCode)
	}
}

func dialWS(t *testing.T, wsURL string, header http.Header) *websocket.Conn {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: header})
		cancel()
		if err == nil {
			return c
		}
		status, body := 0, ""
		if resp != nil {
			status = resp.StatusCode
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			body = strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("ws dial %s failed (last http status=%d body=%q): %v", wsURL, status, body, err)
		}
		// A non-connection error (e.g. an HTTP 4xx) is a real rejection, not a
		// not-yet-listening retry — surface it immediately with the status + body.
		if status != 0 {
			t.Fatalf("ws upgrade rejected with HTTP %d (body=%q): %v", status, body, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// assertWSRejectsWithoutAuth proves the broker capability gate still rejects an
// upgrade carrying no session cookie (HTTP 401) — the negative half of the
// ADR-0050 §2 auth contract, so a future change cannot silently disable WS auth.
func assertWSRejectsWithoutAuth(t *testing.T, wsURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, wsURL, nil) // no Cookie header
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("unauthenticated WS upgrade succeeded — broker capability gate is NOT enforcing")
	}
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WS upgrade: want HTTP 401, got status=%d err=%v", status, err)
	}
}

// readUntilMarker waits for the marker to appear at least twice in PTY output:
// once as the shell's echo of the typed line, once as the command's stdout —
// the second occurrence proves the command actually executed, not just echoed.
// One overall-deadline context governs the whole phase: coder/websocket closes
// the connection on context cancellation, so a per-read timeout would kill the
// live socket. On success we return before the deadline with the conn intact.
func readUntilMarker(ws *websocket.Conn, marker string, within time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	defer cancel()
	var acc bytes.Buffer
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return false
		}
		acc.Write(data)
		if bytes.Count(acc.Bytes(), []byte(marker)) >= 2 {
			return true
		}
	}
}

func pollChain(t *testing.T, auditURL, token, target string, within time.Duration) []auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		all := listAllEvents(t, auditURL, token)
		var match []auditEvent
		for _, e := range all {
			if e.Target == target {
				match = append(match, e)
			}
		}
		if hasOutcome(match, "opened") && hasOutcome(match, "input.line") && hasOutcome(match, "closed:operator") {
			return match
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit chain incomplete for %s after %s: got %d events %+v", target, within, len(match), match)
		}
		time.Sleep(2 * time.Second)
	}
}

func listAllEvents(t *testing.T, auditURL, token string) []auditEvent {
	t.Helper()
	var out []auditEvent
	cursor := ""
	client := &http.Client{Timeout: 10 * time.Second}
	for {
		u := auditURL + "/v1/events?limit=200"
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		var page eventPage
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list events: status %d", resp.StatusCode)
		}
		if err != nil {
			t.Fatalf("decode events: %v", err)
		}
		out = append(out, page.Items...)
		if page.Page.NextCursor == "" {
			return out
		}
		cursor = page.Page.NextCursor
	}
}

func hasOutcome(events []auditEvent, outcome string) bool {
	for _, e := range events {
		if e.Outcome == outcome {
			return true
		}
	}
	return false
}

// assertOutcome requires exactly one event with the given outcome and (when
// wantEvidence != "") that its evidencePacketId matches. Returns that event.
func assertOutcome(t *testing.T, events []auditEvent, outcome, wantEvidence string) auditEvent {
	t.Helper()
	var found []auditEvent
	for _, e := range events {
		if e.Outcome == outcome {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly 1 %q event, got %d: %+v", outcome, len(found), events)
	}
	if wantEvidence != "" && found[0].EvidencePacketID != wantEvidence {
		t.Fatalf("%q evidencePacketId = %q, want %q", outcome, found[0].EvidencePacketID, wantEvidence)
	}
	return found[0]
}

func mintAuditToken(t *testing.T) string {
	t.Helper()
	keyHex := os.Getenv("AIREOS_PASETO_KEY")
	if keyHex == "" {
		t.Fatal("AIREOS_PASETO_KEY must be set to mint the audit-read token")
	}
	key, err := auth.NewKeyFromHex(keyHex)
	if err != nil {
		t.Fatalf("parse paseto key: %v", err)
	}
	tok, err := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID")).IssueService("e2e-test", "audit-service")
	if err != nil {
		t.Fatalf("mint audit token: %v", err)
	}
	return tok
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return b
}
