//go:build e2e || wd1

// Package e2e — shared live-harness helpers for the cockpit end-to-end tests.
// Compiled under either build tag so both drivers reuse one set of helpers:
//
//	tag e2e  → e2e_test.go  TestSSHCockpitEndToEnd        (T-020b: go-run stack, plain http)
//	tag wd1  → wd1_test.go  TestWedgeDemoInstalledStack   (T-021d-b: installed stack via Caddy HTTPS)
//
// The wd1 driver sets http.DefaultTransport's TLSClientConfig to trust the
// self-signed local CA, so these default-client helpers work over https unchanged.
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
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"aireos.local/packages/go/auth"
)

// Must match tests/e2e/cmd/seed/main.go (resourceID) and the harness env
// (operator credentials). sessionCookieName mirrors packages/go/middleware.SessionCookieName.
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

// cookieHeader replays both auth cookies on an api request the same way a browser
// would (CookieToAuthHeader uses the session cookie; the CSRF double-submit check
// compares the aireos_csrf cookie to the X-CSRF-Token header).
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
// once as the shell's echo of the typed line, once as the command's stdout — the
// second occurrence proves the command actually executed, not just echoed. One
// overall-deadline context governs the phase: coder/websocket closes the conn on
// context cancellation, so a per-read timeout would kill the live socket.
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
		var match []auditEvent
		for _, e := range listAllEvents(t, auditURL, token) {
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

func pollAuthEvent(t *testing.T, auditURL, token, outcome, actor string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Outcome == outcome && e.Actor == actor {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("auth event %q by %q never reached the chain within %s", outcome, actor, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// pollLeased waits for connector-service's credential.leased egress event for the
// given resource target (T-030b) — the live proof that the broker obtained the SSH
// key from connector (not from a local decrypt) and that the egress was audited.
func pollLeased(t *testing.T, auditURL, token, resourceTarget string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Target == resourceTarget && e.Outcome == "credential.leased" {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("credential.leased for %s never reached the chain within %s", resourceTarget, within)
		}
		time.Sleep(2 * time.Second)
	}
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
