//go:build wd1

// TestWedgeDemoInstalledStack — the WD-2 wedge demo against the INSTALLED stack
// through Caddy HTTPS (T-021d-b walkthrough, extended to the governed-egress
// wedge by T-033b). The seeded resource is requires_approval-flagged, so the
// walkthrough proves the approval primitive on the production wire: login →
// resource picker → create (parked pending_approval) → operator APPROVE via
// POST /v1/sessions/{id}/resolve → broker WS over wss → run a command + read
// its output → end → DENY a second session (blocked + chained) → logout.
//
// The audit chain (integrity + the expected events, incl. policy.decision /
// approval.* / credential.leased) is asserted host-internally by run-wd1.sh,
// because audit-service binds to localhost in the install and is not
// Caddy-routed. The operator audit-timeline VIEW is asserted here (T-024).
//
// Shared harness helpers live in harness_test.go (build tag e2e || wd1).
package e2e

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWedgeDemoInstalledStack(t *testing.T) {
	// Trust the self-signed local CA (Caddy `tls internal`) for the whole driver.
	// One test per binary (tag wd1), so mutating the default transport is safe.
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	wsBase := strings.Replace(base, "https://", "wss://", 1)
	client := &http.Client{Timeout: 20 * time.Second}

	// 1. login over Caddy HTTPS (retry while the stack settles). Secure cookies
	//    work normally here (real HTTPS) — no plaintext cookie workaround.
	var sess, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, base)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sess, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded over Caddy HTTPS (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("login ok over Caddy HTTPS")

	// 2. resource picker: GET /v1/resources shows the seeded ssh resource (WD-1 step 3).
	assertResourceVisible(t, client, base, sess, csrf)

	// 3. create against the flagged resource → parked pending_approval (the
	//    ADR-0054 gate on the production wire) → operator approves → 'pending',
	//    then open the broker WS over wss through Caddy.
	sessionID := createSessionFor(t, client, base, sess, csrf, resourceID, "pending_approval")
	t.Log("session parked pending_approval (requires_approval resource)")
	if got := resolveSession(t, client, base, sessionID, sess, csrf, "approve"); got != "pending" {
		t.Fatalf("approve: session status = %q, want pending", got)
	}
	t.Log("approved — session released to the broker")
	wsURL := wsBase + "/v1/sessions/" + sessionID + "/channel"
	ws := dialWS(t, wsURL, http.Header{"Cookie": []string{sessionCookieName + "=" + sess}})
	defer ws.CloseNow()
	assertWSRejectsWithoutAuth(t, wsURL) // ADR-0050 §2 guard, now over wss

	// 4. run a command + read its output back over the live PTY (SSH round-trip).
	marker := "WD1OK-" + hex.EncodeToString(randBytes(t, 6))
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte("echo "+marker+"\r")); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if !readUntilMarker(ws, marker, 20*time.Second) {
		t.Fatal("command output never returned over the PTY — the SSH round-trip failed")
	}
	t.Log("command executed; marker observed in PTY output")

	// 5. end the session + close the WS.
	endSession(t, client, base, sessionID, sess, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "wd1 done")

	// 5b. deny leg (WD-2.3's other half): a second parked session is DENIED —
	//     api answers closed (approval_denied); approval.denied:operator is
	//     asserted host-internally by run-wd1.sh. The blocked-attach runtime
	//     proof ran live in the compose e2e (T-033a).
	deniedID := createSessionFor(t, client, base, sess, csrf, resourceID, "pending_approval")
	if got := resolveSession(t, client, base, deniedID, sess, csrf, "deny"); got != "closed" {
		t.Fatalf("deny: session status = %q, want closed", got)
	}
	t.Logf("denied session %s closed (approval_denied)", deniedID)

	// 6. operator visits the audit timeline through Caddy -> api read-proxy ->
	//    audit-service (T-024). Proves the in-cockpit read path on real wires:
	//    the operator reads their own chained events. Closes the "operator
	//    visits the audit timeline" half of WD-1 step 8 (chain *integrity* is
	//    asserted host-internally by run-wd1.sh).
	assertAuditTimelineReadable(t, client, base, sess, csrf)

	// 7. logout (emits auth.logout).
	logout(t, client, base, sess, csrf)

	t.Log("PASS — WD-2 operator walkthrough complete through Caddy HTTPS (login → resource → parked create → approve → session → command → end → deny leg → audit timeline → logout). Chain integrity is asserted by run-wd1.sh.")
}

// TestWD5InstalledStack — OI-12: WD-5 on the INSTALLED stack through Caddy HTTPS. It
// proves the Run plane works on the real install (migrations applied, GATEWAY_URL wired,
// the in-process sequencer driving the installed gateway): an operator launches a
// multi-step governed Run, each step PARKS for approval and is approved on /invocations,
// the sequencer CONTINUES to the next step, and the Run completes. The seeded resource is
// requires_approval-flagged (run-wd1.sh), so a 2-step plan on it parks twice → the
// multi-step governed drive end-to-end. run.launched / run.completed landing in the chain
// (via api's in-process outbox relay) are asserted host-internally by run-wd1.sh. The full
// negative matrix (denied-abort/cancel/crash-resume) is compose-proven by T-060; HTTP-only
// here (the external driver has no DB, only Caddy HTTPS).
func TestWD5InstalledStack(t *testing.T) {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := &http.Client{Timeout: 30 * time.Second}

	// login over Caddy HTTPS (retry while the stack settles).
	var sess, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, base)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sess, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded over Caddy HTTPS (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}

	// Launch a 2-step governed Run on the flagged resource (the only installed resource).
	runID, st := launchRun(t, client, base, sess, csrf, []stepSeed{
		{resourceID, "echo WD5-INSTALLED-1"},
		{resourceID, "echo WD5-INSTALLED-2"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch run: status %d, want 201", st)
	}
	t.Logf("launched 2-step run %s on the installed stack", runID)

	// Each step parks for approval; approve in turn — the sequencer continues past each.
	for i := 0; i < 2; i++ {
		inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 90*time.Second)
		resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
		t.Logf("approved parked step %d (invocation %s)", i, inv.ID)
	}

	// Poll the operator GET route until the Run completes (HTTP-only — no DB here).
	runDeadline := time.Now().Add(180 * time.Second)
	for {
		got := getRun(t, client, base, sess, csrf, runID)
		if got.Status == "completed" {
			if len(got.Steps) != 2 {
				t.Fatalf("completed run has %d steps, want 2", len(got.Steps))
			}
			break
		}
		if got.Status == "failed" || got.Status == "aborted" || got.Status == "canceled" {
			t.Fatalf("run %s ended %q, want completed", runID, got.Status)
		}
		if time.Now().After(runDeadline) {
			t.Fatalf("run %s never completed (last status %q)", runID, got.Status)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("PASS — WD-5 on the installed stack: a 2-step governed Run drove through the INSTALLED gateway; each step parked→approved→continued→completed. run.launched/run.completed in the chain are asserted by run-wd1.sh.")
}

func assertResourceVisible(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/resources", nil)
	req.Header.Set("Cookie", cookieHeader(sess, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list resources: status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), resourceID) {
		t.Fatalf("resource picker missing the seeded resource %s: %s", resourceID, string(b))
	}
}

// assertAuditTimelineReadable drives the operator audit-timeline read through
// Caddy -> services/api read-proxy -> audit-service (T-024-a). It polls because
// api delivers auth.* events to the chain asynchronously through its outbox
// relay (T-022); the operator's own auth.login.success must appear in the page
// read back through the proxy.
func assertAuditTimelineReadable(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/audit/events?limit=100", nil)
		req.Header.Set("Cookie", cookieHeader(sess, csrf))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("read audit timeline: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("audit timeline (Caddy->api->audit): status %d body=%s", resp.StatusCode, string(body))
		}
		s := string(body)
		// the operator's own login event, chained + read back through the proxy.
		if strings.Contains(s, "auth.login.success") && strings.Contains(s, "operator:admin") {
			t.Log("operator read the audit timeline through Caddy -> api proxy -> audit-service; own auth.login.success present")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit timeline never showed the operator's auth.login.success (relay lag?): %s", s)
		}
		time.Sleep(2 * time.Second)
	}
}

func logout(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/auth/logout", nil)
	req.Header.Set("Cookie", cookieHeader(sess, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: status %d", resp.StatusCode)
	}
}
