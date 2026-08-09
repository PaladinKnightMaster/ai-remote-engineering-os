//go:build e2e

// WD-11 negatives (T-121, docs/releases/v0.12.0 §2 GATE-1 + §4; ADR-0068 §D9) — the
// mandatory negative leg SEC-21 established at WD-4: "a capability claim is proven by
// what it denies."
//
//   - TestWD11RevokedSessionRefused (SEC-34) — a paired session revoked through the
//     real desk route is refused live on reuse.
//   - TestWD11ExpiredSessionRefused (SEC-34) — a paired session is redeemed for real,
//     then seeded stale directly in Postgres (T-056's seeded-stale-state precedent,
//     the same move the run-claim lease used) rather than waiting out PairedTTL (8h);
//     the REAL redeemed cookie is reused, exercising the genuine liveness check.
//   - TestWD11OutOfScopeDenied (SEC-35) — a live paired session is refused every
//     named out-of-scope action: launching a Run, issuing or revoking a grant,
//     registering or removing a context source, minting an agent token, and opening
//     a terminal session. TestPairedSessionDeniesNamedActions
//     (services/api/internal/server/pairing_test.go) already proves this at the
//     handler level; this drives the SAME denials live, over real HTTP, against a
//     genuinely redeemed cookie.
package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestWD11RevokedSessionRefused(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	client := &http.Client{Timeout: 60 * time.Second}
	deskSession, deskCSRF, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("desk login: status=%d err=%v", status, err)
	}

	label := "wd11-revoked-" + w9suffix()
	reqID, secret := createPairingRequest(t, client, apiURL, deskSession, deskCSRF, label)
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, apiURL, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" {
		t.Fatalf("redeem: status=%d session-empty=%v", redeemStatus, pairedSession == "")
	}

	// Sanity: the freshly redeemed session works before revoke.
	if got := meStatus(t, client, apiURL, pairedSession, pairedCSRF); got != http.StatusOK {
		t.Fatalf("me before revoke: status %d, want 200", got)
	}

	sessions := listPairedSessionsHTTP(t, client, apiURL, deskSession, deskCSRF)
	var pairedID string
	for _, s := range sessions {
		if s.Label == label && s.RevokedAt == nil {
			pairedID = s.ID
		}
	}
	if pairedID == "" {
		t.Fatalf("no live paired session labeled %q: %+v", label, sessions)
	}

	// Revoke through the REAL desk route (GOV-14/ADR-0068 §D4), then reuse the
	// paired credentials — the genuine revocation cascade, not a store-level stub.
	revokePairedSessionHTTP(t, client, apiURL, deskSession, deskCSRF, pairedID)

	if got := meStatus(t, client, apiURL, pairedSession, pairedCSRF); got != http.StatusUnauthorized {
		t.Fatalf("SEC-34: a revoked paired session's own (real, redeemed) cookie = %d, want 401", got)
	}
	t.Log("PASS WD-11 SEC-34 (revoked) — a paired session revoked through the real desk route is refused live (401) on reuse")
}

func TestWD11ExpiredSessionRefused(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	client := &http.Client{Timeout: 60 * time.Second}
	deskSession, deskCSRF, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("desk login: status=%d err=%v", status, err)
	}

	label := "wd11-expired-" + w9suffix()
	reqID, secret := createPairingRequest(t, client, apiURL, deskSession, deskCSRF, label)
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, apiURL, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" {
		t.Fatalf("redeem: status=%d session-empty=%v", redeemStatus, pairedSession == "")
	}

	sessions := listPairedSessionsHTTP(t, client, apiURL, deskSession, deskCSRF)
	var pairedID string
	for _, s := range sessions {
		if s.Label == label && s.RevokedAt == nil {
			pairedID = s.ID
		}
	}
	if pairedID == "" {
		t.Fatalf("no live paired session labeled %q: %+v", label, sessions)
	}

	// T-056's seeded-stale-state precedent: reach into Postgres directly and push
	// expires_at into the past, then reuse the REAL redeemed cookie — this exercises
	// the genuine per-request liveness check rather than waiting out PairedTTL (8h)
	// or forging a synthetic token.
	pool := runEngineDB(t)
	if _, err := pool.Exec(context.Background(),
		`UPDATE paired_sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, pairedID); err != nil {
		t.Fatalf("seed expired paired session: %v", err)
	}

	if got := meStatus(t, client, apiURL, pairedSession, pairedCSRF); got != http.StatusUnauthorized {
		t.Fatalf("SEC-34: an expired paired session's own (real, redeemed) cookie = %d, want 401", got)
	}
	t.Log("PASS WD-11 SEC-34 (expired) — a genuinely redeemed paired session, seeded stale directly in Postgres, is refused live (401) via the real per-request liveness check")
}

func TestWD11OutOfScopeDenied(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	client := &http.Client{Timeout: 60 * time.Second}
	deskSession, deskCSRF, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("desk login: status=%d err=%v", status, err)
	}

	label := "wd11-scope-" + w9suffix()
	reqID, secret := createPairingRequest(t, client, apiURL, deskSession, deskCSRF, label)
	pairedSession, pairedCSRF, redeemStatus := redeemPairingRequest(t, client, apiURL, reqID, secret)
	if redeemStatus != http.StatusOK || pairedSession == "" {
		t.Fatalf("redeem: status=%d session-empty=%v", redeemStatus, pairedSession == "")
	}

	// SEC-35's named refusals (release criteria §4), the exact set
	// TestPairedSessionDeniesNamedActions asserts at the handler level.
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
			got := pairedRouteStatus(t, client, apiURL, pairedSession, pairedCSRF, tc.method, tc.path, tc.body)
			if got != http.StatusForbidden {
				t.Fatalf("SEC-35: %s %s from a live paired session = %d, want 403 (refused)", tc.method, tc.path, got)
			}
			t.Logf("PASS SEC-35 — %s %s refused live (403)", tc.method, tc.path)
		})
	}

	// Positive control: the SAME live session still reaches its own in-scope surface
	// (read-own-session) — the denials above are the scope working, not the session
	// being generally broken.
	if got := meStatus(t, client, apiURL, pairedSession, pairedCSRF); got != http.StatusOK {
		t.Fatalf("positive control: /v1/auth/me from the same live paired session = %d, want 200", got)
	}

	t.Log("PASS WD-11 SEC-35 — a live paired session is refused every out-of-scope action the criterion names, live over real HTTP with a genuinely redeemed cookie; its own in-scope surface still works")
}
