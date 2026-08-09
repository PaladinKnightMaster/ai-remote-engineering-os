//go:build e2e || wd1

// Shared QR-pairing HTTP driver helpers (ADR-0068 §D4-§D6/§D9-§D11) — used by the
// compose e2e WD-11 capstone (wd11_test.go / wd11_negatives_test.go, tag e2e) and, at
// T-122, the installed-stack WD-11 driver (tag wd1). HTTP-only (no DB), the
// context_driver.go/delegated_driver.go/run_driver.go pattern; wd11_negatives_test.go's
// seeded-stale-expiry leg (T-056's direct-DB precedent) lives in that e2e-only file
// instead, mirroring run_engine_test.go's DB/HTTP split.
//
// Two roles, two credential pairs, nothing shared implicitly: doLogin returns the
// DESK session+csrf as strings; redeemPairingRequest below is the redeem-side
// equivalent, returning the PAIRED session+csrf as its OWN strings captured straight
// off Set-Cookie. There is no cookie jar anywhere in this harness (cookieHeader
// builds the header per request), so callers simply must not let one role's
// credentials reach the other's requests — nothing more is required.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// pairedSessionView mirrors services/api/internal/server's PairedSession wire shape
// (ADR-0068 §D4/§D11) — the desk's "your paired devices" list.
type pairedSessionView struct {
	ID        string     `json:"id"`
	Label     string     `json:"label"`
	Scope     string     `json:"scope"`
	ExpiresAt time.Time  `json:"expiresAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// createPairingRequest POSTs a desk-initiated pairing request (POST
// /v1/pairing-requests, ADR-0068 §D6; GOV-12) and returns its id plus the ONE-TIME
// plaintext secret the QR fragment would carry (never sent as a query param — GOV-13).
func createPairingRequest(t *testing.T, client *http.Client, apiURL, session, csrf, label string) (id, secret string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"label": label})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/pairing-requests", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create pairing request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create pairing request: status %d, want 201", resp.StatusCode)
	}
	var out struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" || out.Secret == "" {
		t.Fatalf("decode pairing request: err=%v id=%q secret-empty=%v", err, out.ID, out.Secret == "")
	}
	return out.ID, out.Secret
}

// redeemPairingRequest POSTs the fragment-carried secret from the device being paired
// (POST /v1/pairing-requests/{id}/redeem — PUBLIC, no bearer at all, ADR-0068 §D6) and
// captures the new paired session's cookies straight off Set-Cookie — the redeem-side
// equivalent of doLogin (harness_test.go), returned in its OWN pair of variables.
func redeemPairingRequest(t *testing.T, client *http.Client, apiURL, id, secret string) (session, csrf string, status int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"secret": secret})
	resp, err := client.Post(apiURL+"/v1/pairing-requests/"+id+"/redeem", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("redeem pairing request: %v", err)
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
	return session, csrf, resp.StatusCode
}

// listPairedSessionsHTTP GETs the desk's paired-session list (GET /v1/paired-sessions,
// owner-scoped) — how a test discovers the id a redeem just created (the redeem
// response itself deliberately never carries the raw session id, ADR-0068 §D6).
func listPairedSessionsHTTP(t *testing.T, client *http.Client, apiURL, session, csrf string) []pairedSessionView {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/paired-sessions", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list paired sessions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list paired sessions: status %d, want 200", resp.StatusCode)
	}
	var page struct {
		Items []pairedSessionView `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode paired sessions: %v", err)
	}
	return page.Items
}

// revokePairedSessionHTTP POSTs the desk's revoke (POST /v1/paired-sessions/{id}/revoke,
// ADR-0068 §D4 — the product's first revocable operator session) and asserts 200.
func revokePairedSessionHTTP(t *testing.T, client *http.Client, apiURL, session, csrf, id string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/paired-sessions/"+id+"/revoke", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("revoke paired session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke paired session: status %d, want 200", resp.StatusCode)
	}
}

// meStatus GETs /v1/auth/me under the given cookie pair and returns ONLY the HTTP
// status — the SEC-34 negative legs' shared probe (a revoked or expired paired
// session must be refused live; the status code is what those legs assert, deliberately
// not the body, mirroring SEC-34's per-request liveness posture).
func meStatus(t *testing.T, client *http.Client, apiURL, session, csrf string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/auth/me", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get me: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// pairedRouteStatus fires an arbitrary (method, path, body) request under a live
// paired session's cookie and returns ONLY the status — SEC-35's live negative leg.
// TestPairedSessionDeniesNamedActions (services/api/internal/server/pairing_test.go)
// already proves the same refusals at the handler level; this drives them live, over
// real HTTP, against a genuinely redeemed cookie. CSRF is set correctly so a 403 here
// is provably the approvals@1 scope gate, not an incidental CSRF failure.
func pairedRouteStatus(t *testing.T, client *http.Client, apiURL, session, csrf, method, path, body string) int {
	t.Helper()
	var r *http.Request
	var err error
	if body == "" {
		r, err = http.NewRequest(method, apiURL+path, nil)
	} else {
		r, err = http.NewRequest(method, apiURL+path, bytes.NewReader([]byte(body)))
	}
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, path, err)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Cookie", cookieHeader(session, csrf))
	r.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
