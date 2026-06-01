//go:build wd1

// TestWedgeDemoInstalledStack — the WD-1 wedge demo against the INSTALLED stack
// through Caddy HTTPS (T-021d-b). Proves the operator walkthrough on the real
// production wire: login → resource picker (GET /v1/resources) → create session →
// broker WS over wss → run a command + read its output → end → logout.
//
// The audit chain (integrity + the expected events) is asserted host-internally
// by run-wd1.sh, because audit-service binds to localhost in the install and is
// not Caddy-routed. The operator audit-timeline VIEW is a separate gap (T-024).
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

	// 3. create session + open the broker WS over wss through Caddy.
	sessionID := createSession(t, client, base, sess, csrf)
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

	// 5. end the session, close the WS, then logout (WD-1 step 8; emits auth.logout).
	endSession(t, client, base, sessionID, sess, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "wd1 done")
	logout(t, client, base, sess, csrf)

	t.Log("PASS — WD-1 operator walkthrough complete through Caddy HTTPS (login → resource → session → command → end → logout). Chain integrity is asserted by run-wd1.sh.")
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
