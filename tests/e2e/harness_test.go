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
	"fmt"
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

// Must match tests/e2e/cmd/seed/main.go (resourceID / flaggedResourceID) and the
// harness env (operator credentials). sessionCookieName mirrors
// packages/go/middleware.SessionCookieName.
const (
	resourceID          = "01900000-0000-7000-8000-0000000000e2"
	flaggedResourceID   = "01900000-0000-7000-8000-0000000000a2" // requires_approval=true (T-033a / WD-2)
	delegatedResourceID = "01900000-0000-7000-8000-0000000000d3" // ungated; the T-065b agent-initiated-Run capstone
	operatorUser        = "admin"
	operatorPass        = "e2e-operator-pw"
	sessionCookieName   = "aireos_session"
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
	return createSessionFor(t, client, apiURL, session, csrf, resourceID, "pending")
}

// createSessionFor creates a session against an arbitrary resource and asserts
// the create-time status: "pending" for a normal resource, "pending_approval"
// for a requires_approval-flagged one (ADR-0054 — api parks the session and
// consults policy before any broker attach is possible).
func createSessionFor(t *testing.T, client *http.Client, apiURL, session, csrf, resID, wantStatus string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"resourceId": resID})
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
	if s.ID == "" || s.Status != wantStatus {
		t.Fatalf("unexpected session (want status %q): %+v", wantStatus, s)
	}
	return s.ID
}

// resolveSession drives the operator approve/deny on a parked session
// (POST /v1/sessions/{id}/resolve, T-031b-ii — human-agent + CSRF gated) and
// returns the session status api answered with: approve → "pending" (the broker
// may now attach), deny → "closed" (reason approval_denied, status-only — the
// audit event comes from policy-service's outbox, not api).
func resolveSession(t *testing.T, client *http.Client, apiURL, id, session, csrf, decision string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/sessions/"+id+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("resolve session (%s): %v", decision, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve session (%s): status %d", decision, resp.StatusCode)
	}
	var s struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("decode resolved session: %v", err)
	}
	return s.Status
}

// pollEvidenceOutcome waits for an event anchored to the given evidencePacketId
// with the given outcome — how the policy decision + approval events are found
// (they target resource:<id> but anchor evidence to session:<id>, ADR-0054 §3).
func pollEvidenceOutcome(t *testing.T, auditURL, token, evidence, outcome string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.EvidencePacketID == evidence && e.Outcome == outcome {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %q with evidence %q never reached the chain within %s", outcome, evidence, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// mintAgentTokenViaAPI provisions a scoped agent-tool token through the operator's
// session (POST /v1/agent-tokens — human-agent + CSRF, ADR-0056/0057; T-036/AG-3),
// the real operator-provisions-agent path used by the WD-3 capstone (not the
// cluster-key shortcut). The token subject is agent:<agentID>, scoped to resID.
func mintAgentTokenViaAPI(t *testing.T, client *http.Client, apiURL, session, csrf, agentID, resID string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"agentId": agentID, "resourceId": resID, "operations": []string{"exec"}})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/agent-tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("mint agent token via api: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mint agent token via api: status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode agent token: %v", err)
	}
	if out.Token == "" {
		t.Fatal("api returned an empty agent token")
	}
	return out.Token
}

// pendingInvocation mirrors the operator pending-list row (services/api proxies the
// gateway's internal list; ADR-0057 §4 / ADR-0059 — it carries the command so the
// operator approves the exact payload). id is the bare invocation UUID.
type pendingInvocation struct {
	ID         string `json:"id"`
	Agent      string `json:"agent"`
	ResourceID string `json:"resourceId"`
	Tool       string `json:"tool"`
	Command    string `json:"command"`
	Status     string `json:"status"`
}

// pollPendingInvocation waits for a parked invocation against resID to surface in the
// operator list (GET /v1/invocations) — how the operator discovers what to approve.
// Exactly one is pending per resource per leg: cold-compile retries finalize to
// 'failed' (not 'pending'), so they never collide here.
func pollPendingInvocation(t *testing.T, client *http.Client, apiURL, session, csrf, resID string, within time.Duration) pendingInvocation {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/invocations", nil)
		req.Header.Set("Cookie", cookieHeader(session, csrf))
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var body struct {
				Invocations []pendingInvocation `json:"invocations"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			for _, inv := range body.Invocations {
				if inv.ResourceID == resID && inv.Status == "pending_approval" {
					return inv
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("no parked invocation for %s appeared within %s", resID, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// resolveInvocation drives the operator approve/deny on a parked invocation
// (POST /v1/invocations/{id}/resolve — human-agent + CSRF, proxied to the gateway
// which owns the reconcile; ADR-0057 §4). Asserts api answered 200.
func resolveInvocation(t *testing.T, client *http.Client, apiURL, id, session, csrf, decision string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"decision": decision})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/invocations/"+id+"/resolve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("resolve invocation (%s): %v", decision, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve invocation (%s): status %d", decision, resp.StatusCode)
	}
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

// mcpToolResult is the agent-facing tools/call result (MCP content blocks + isError).
type mcpToolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// fireHeldInvoke runs an agent's tools/call in the background against mcpURL/mcp. A
// parked invocation BLOCKS the call (block-and-wait) until the operator resolves it;
// a fail-closed JSON-RPC error means a downstream service is still cold-compiling, so
// it retries. It delivers the first real tool result (success OR a tool-error such as
// a deny) to out. No t.Fatalf here — it runs off the test goroutine. Shared by the
// compose-e2e (T-041) and the installed wd1 WD-3 driver (T-043a); the client uses the
// default transport, so the wd1 driver's CA-trust mutation applies to it too.
func fireHeldInvoke(mcpURL, tok, resID, command string, out chan<- mcpToolResult, errc chan<- error) {
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.exec",` +
		`"arguments":{"resourceId":"` + resID + `","command":"` + command + `"}}}`
	client := &http.Client{Timeout: 150 * time.Second} // > the gateway block-and-wait deadline (125s)
	deadline := time.Now().Add(240 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", strings.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpc)
			_ = resp.Body.Close()
			if rpc.Error == nil && len(rpc.Result) > 0 {
				var r mcpToolResult
				if err := json.Unmarshal(rpc.Result, &r); err == nil {
					out <- r
					return
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			errc <- fmt.Errorf("held tools/call never returned a tool result (last transport err=%v)", err)
			return
		}
		time.Sleep(3 * time.Second)
	}
}
