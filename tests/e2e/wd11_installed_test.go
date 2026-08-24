//go:build wd1

// WD-11 — the v0.12.0 pairing walkthrough (T-122a, docs/releases/v0.12.0 §2 GATE-1)
// on the INSTALLED stack through Caddy HTTPS: request -> redeem -> approve a held
// invocation FROM the paired session -> revoke from the desk. Mirrors wd11_test.go
// (tag e2e) but HTTP-only throughout — the wd1 tester container, unlike compose
// e2e's tester, has no network path to the installed Postgres (it runs inside the
// systemd host container, not published on the harness network; see run-wd1.sh),
// and audit-service is not Caddy-routed either (it binds localhost only — see
// wd1_test.go's header). So the pairing.requested/redeemed/revoked + the paired
// approval.granted#paired outcome are asserted host-internally by run-wd1.sh's
// WANTS list, exactly like every other WD-N event already proven on this stack.
//
// Pairing ships OFF by default (SEC-41). run-wd1.sh re-runs install.sh with
// AIREOS_PAIRING_ENABLED=1 BEFORE this test runs — the real operator enable path
// (env > persisted > "0", OI-23 idempotence preserved) — and restarts
// aireos-api.service so the new env takes effect (a running process only reads
// AIREOS_PAIRING_ENABLED at boot, services/api/main.go). run-wd1.sh also asserts
// the OFF state 404s beforehand, proving the off-by-default posture on THIS stack
// rather than assuming compose's proof carries over.
//
// TestWD11RevokedSessionRefusedInstalled and TestWD11OutOfScopeDeniedInstalled are
// both HTTP-only and driven here, mirroring wd11_negatives_test.go's compose legs.
// TestWD11ExpiredSessionRefusedInstalled documents why the EXPIRED sub-case of
// SEC-34 is NOT driven on this stack (see its body) rather than silently omitting
// it.
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// wd11InstalledLogin logs in over Caddy HTTPS, retrying while the stack settles —
// the same shape wd1_test.go / installed_invocation_test.go already use.
func wd11InstalledLogin(t *testing.T, client *http.Client, base string) (sess, csrf string) {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, base)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			return s, c
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded over Caddy HTTPS (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func TestWD11PairingInstalled(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 20*time.Second)
	sess, csrf := wd11InstalledLogin(t, client, base)

	// --- Leg 1: the desk requests a pairing (GOV-12). ---------------------------
	label := "wd11-installed-phone"
	reqID, secret := createPairingRequest(t, client, base, sess, csrf, label)
	t.Logf("PASS leg 1 — desk requested pairing %s (pairing.requested chained host-internally, asserted by run-wd1.sh)", reqID)

	// --- Leg 2: the device redeems the fragment-carried secret. -----------------
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, base, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" || pairedCSRF == "" {
		t.Fatalf("redeem: status=%d session-empty=%v csrf-empty=%v", redeemStatus, pairedSession == "", pairedCSRF == "")
	}
	sessions := listPairedSessionsHTTP(t, client, base, sess, csrf)
	var pairedID string
	for _, s := range sessions {
		if s.Label == label && s.RevokedAt == nil {
			pairedID = s.ID
		}
	}
	if pairedID == "" {
		t.Fatalf("no live paired session labeled %q in the desk's list: %+v", label, sessions)
	}
	t.Logf("PASS leg 2 — device redeemed the secret for paired session %s (pairing.redeemed chained host-internally)", pairedID)

	// --- Leg 3: a held invocation parks, and the PAIRED session approves it -----
	// (AUD-34: the approval must be attributable as paired-origin; asserted
	// host-internally as actor=operator:admin#paired by run-wd1.sh).
	tok := mintAgentTokenViaAPI(t, client, base, sess, csrf, "wd11-installed-agent", resourceID)
	const wantOut = "WD11-INSTALLED-PAIRED-APPROVE-OK"
	out := make(chan mcpToolResult, 1)
	errc := make(chan error, 1)
	go fireHeldInvoke(base, tok, resourceID, "echo "+wantOut, out, errc)
	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 150*time.Second)
	resolveInvocation(t, client, base, inv.ID, pairedSession, pairedCSRF, "approve")
	t.Logf("approved invocation %s FROM THE PAIRED SESSION (not the desk)", inv.ID)

	select {
	case res := <-out:
		if res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, wantOut) {
			t.Fatalf("approved tool result = %+v, want stdout containing %q (isError false)", res, wantOut)
		}
	case err := <-errc:
		t.Fatalf("held invoke failed: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the held agent call did not return after the paired approval")
	}
	t.Log("PASS leg 3 — held invocation approved FROM THE PAIRED SESSION")

	// --- Leg 4: the desk revokes the paired session — the first revocable ------
	// operator session (ADR-0068 §D4) — then reuse proves SEC-34 live.
	revokePairedSessionHTTP(t, client, base, sess, csrf, pairedID)
	if got := meStatus(t, client, base, pairedSession, pairedCSRF); got != http.StatusUnauthorized {
		t.Fatalf("SEC-34: a revoked paired session's own cookie = %d, want 401 (post-revoke reuse)", got)
	}
	t.Log("PASS WD-11 installed — request -> redeem -> approve from the paired session -> revoke from the desk, over Caddy HTTPS; pairing.requested/redeemed/revoked + approval.granted#paired asserted host-internally by run-wd1.sh")
}

// TestWD11RevokedSessionRefusedInstalled — SEC-34 (revoked), HTTP-only, mirroring
// wd11_negatives_test.go's TestWD11RevokedSessionRefused on compose.
func TestWD11RevokedSessionRefusedInstalled(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 20*time.Second)
	sess, csrf := wd11InstalledLogin(t, client, base)

	label := "wd11-installed-revoked"
	reqID, secret := createPairingRequest(t, client, base, sess, csrf, label)
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, base, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" {
		t.Fatalf("redeem: status=%d session-empty=%v", redeemStatus, pairedSession == "")
	}
	if got := meStatus(t, client, base, pairedSession, pairedCSRF); got != http.StatusOK {
		t.Fatalf("me before revoke: status %d, want 200", got)
	}

	sessions := listPairedSessionsHTTP(t, client, base, sess, csrf)
	var pairedID string
	for _, s := range sessions {
		if s.Label == label && s.RevokedAt == nil {
			pairedID = s.ID
		}
	}
	if pairedID == "" {
		t.Fatalf("no live paired session labeled %q: %+v", label, sessions)
	}

	revokePairedSessionHTTP(t, client, base, sess, csrf, pairedID)
	if got := meStatus(t, client, base, pairedSession, pairedCSRF); got != http.StatusUnauthorized {
		t.Fatalf("SEC-34: a revoked paired session's own (real, redeemed) cookie = %d, want 401", got)
	}
	t.Log("PASS WD-11 installed SEC-34 (revoked) — a paired session revoked through the real desk route is refused live (401) on reuse")
}

// TestWD11OutOfScopeDeniedInstalled — SEC-35, HTTP-only, mirroring
// wd11_negatives_test.go's TestWD11OutOfScopeDenied on compose.
func TestWD11OutOfScopeDeniedInstalled(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 20*time.Second)
	sess, csrf := wd11InstalledLogin(t, client, base)

	label := "wd11-installed-scope"
	reqID, secret := createPairingRequest(t, client, base, sess, csrf, label)
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, base, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" {
		t.Fatalf("redeem: status=%d session-empty=%v", redeemStatus, pairedSession == "")
	}

	cases := []struct {
		name, method, path, body string
	}{
		{"launch a Run", http.MethodPost, "/v1/runs", `{}`},
		{"issue a grant", http.MethodPost, "/v1/grants", `{}`},
		{"revoke a grant", http.MethodPost, "/v1/grants/wd11-nonexistent/revoke", ""},
		{"register a context source", http.MethodPost, "/v1/context-sources", `{}`},
		{"remove a context source", http.MethodDelete, "/v1/context-sources/wd11-nonexistent", ""},
		{"mint an agent token", http.MethodPost, "/v1/agent-tokens", `{}`},
		{"open a terminal session", http.MethodPost, "/v1/sessions", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pairedRouteStatus(t, client, base, pairedSession, pairedCSRF, tc.method, tc.path, tc.body)
			if got != http.StatusForbidden {
				t.Fatalf("SEC-35: %s %s from a live paired session = %d, want 403 (refused)", tc.method, tc.path, got)
			}
			t.Logf("PASS SEC-35 — %s %s refused live (403)", tc.method, tc.path)
		})
	}

	if got := meStatus(t, client, base, pairedSession, pairedCSRF); got != http.StatusOK {
		t.Fatalf("positive control: /v1/auth/me from the same live paired session = %d, want 200", got)
	}
	t.Log("PASS WD-11 installed SEC-35 — a live paired session is refused every out-of-scope action; its own in-scope surface still works")
}

// TestWD11ExpiredSessionRefusedInstalled documents the one WD-11 negative leg this
// stack CANNOT drive HTTP-only, per the T-122a brief's instruction not to silently
// drop a leg that can't be driven: compose's TestWD11ExpiredSessionRefused
// (wd11_negatives_test.go) proves SEC-34's expired sub-case by writing
// expires_at directly into paired_sessions (T-056's seeded-stale-state
// precedent) — the compose tester sits on the same Docker network as Postgres.
// The wd1 tester does not: on this stack, Postgres runs INSIDE the systemd host
// container as a systemd-managed service with no port published to the harness
// network (see run-wd1.sh), so there is no DATABASE_URL this test could even
// reach. The other route — waiting out PairedTTL — is a hardcoded 8h constant
// (services/api/internal/pairing/store.go) with no env override (unlike, e.g.,
// AIREOS_MEMORY_SWEEP_SECONDS elsewhere in this codebase), so no HTTP-only
// mechanism exists to force a paired session expired within a test's lifetime
// either. Closing this for real on the installed stack would need one of:
//   - a test-only env-overridable PairedTTL (mirroring the memory-sweep precedent), or
//   - a narrow debug/admin force-expire route,
//
// both of which are product-code changes and out of T-122a's ratified scope.
// SEC-34's REVOKED sub-case IS driven on this stack (TestWD11RevokedSessionRefusedInstalled)
// and the EXPIRED sub-case IS driven on compose (wd11_negatives_test.go, tag e2e) —
// so SEC-34 as a criterion is proven live; only this one stack/sub-case combination
// is not.
func TestWD11ExpiredSessionRefusedInstalled(t *testing.T) {
	t.Skip("SEC-34 expired-session leg not driven on the installed stack: PairedTTL is a hardcoded 8h constant with no env override, and the wd1 tester has no network path to the installed Postgres to seed expires_at into the past (see this test's doc comment for what closing this properly would need). The revoked-session leg (TestWD11RevokedSessionRefusedInstalled) and compose's TestWD11ExpiredSessionRefused (tag e2e) both prove SEC-34 live.")
}
