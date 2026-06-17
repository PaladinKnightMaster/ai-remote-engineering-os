//go:build wd1

// TestInstalledInvocationApprovalWD3 — the WD-3 wedge on the INSTALLED stack through
// Caddy HTTPS (T-043a): the operator provisions an agent-tool token, the agent calls
// remote.exec on the requires_approval-flagged resource via Caddy /mcp → the gateway
// parks + blocks → the operator approves via api → the broker execs on the real sshd
// → the held call returns the stdout. This proves the gateway installs + boots as a
// systemd unit and that the api→gateway proxy, the agent /mcp ingress, GATEWAY_URL
// wiring, and the whole chain work on the production wiring (env files, real
// binaries, Caddy) — the failure modes the compose-e2e cannot catch ([[t033]]). The
// audit chain (tool.invoked / approval.granted / remote.exec / tool.result) is
// asserted host-internally by run-wd1.sh; here we assert the agent-visible result.
// Shared helpers live in harness_test.go (build tag e2e || wd1).
package e2e

import (
	"crypto/tls"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestInstalledInvocationApprovalWD3(t *testing.T) {
	// Trust the self-signed local CA (Caddy `tls internal`) on the shared default
	// transport so fireHeldInvoke's client trusts it too. Idempotent with the other
	// wd1 test, so it is safe regardless of which runs first.
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := &http.Client{Timeout: 20 * time.Second}

	// 1. operator login over Caddy HTTPS (retry while the stack settles).
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

	// 2. the operator provisions a scoped agent-tool token (POST /v1/agent-tokens).
	tok := mintAgentTokenViaAPI(t, client, base, sess, csrf, "wd3", resourceID)

	// 3. the agent calls remote.exec via Caddy /mcp → the gateway parks + blocks.
	const wantOut = "WD3-INSTALLED-OK"
	out := make(chan mcpToolResult, 1)
	errc := make(chan error, 1)
	go fireHeldInvoke(base, tok, resourceID, "echo "+wantOut, out, errc)

	// 4. the operator sees the parked invocation (with its command) and approves it.
	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 150*time.Second)
	if inv.Command != "echo "+wantOut {
		t.Fatalf("parked invocation command = %q, want echo %s", inv.Command, wantOut)
	}
	resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")

	// 5. the held agent call unblocks with the executed stdout (the chain itself is
	//    asserted host-internally by run-wd1.sh).
	select {
	case res := <-out:
		if res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, wantOut) {
			t.Fatalf("approved tool result = %+v, want stdout containing %q", res, wantOut)
		}
	case err := <-errc:
		t.Fatalf("held invoke failed: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the held agent call did not return after approval")
	}

	t.Log("PASS WD-3 installed — agent (Caddy /mcp) → gateway block-and-wait → operator approve (api) → broker exec, on the installed stack")
}
