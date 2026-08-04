package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/pairing"
)

// --- fakePairingStore --------------------------------------------------------
//
// An in-memory pairingStore for handler tests (the fakeGrantStore/fakeStore
// precedent). It reimplements the store's OWN refusal shapes (single-use,
// live-only GetLive, idempotent Revoke) rather than delegating to the real
// package, so these tests exercise the HTTP layer's own logic in isolation
// from services/api/internal/pairing (which has its own live-Postgres
// integration suite).

type fakePairingRequest struct {
	operator string
	label    string
	secret   []byte
	expires  time.Time
	consumed bool
}

type fakePairingStore struct {
	mu       sync.Mutex
	requests map[string]fakePairingRequest
	sessions map[string]pairing.Session
	n        int
}

func newFakePairingStore() *fakePairingStore {
	return &fakePairingStore{requests: map[string]fakePairingRequest{}, sessions: map[string]pairing.Session{}}
}

func (f *fakePairingStore) Create(_ context.Context, in pairing.CreateInput) (pairing.Request, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("req-%05d", f.n)
	secret := []byte(fmt.Sprintf("secret-%05d-000000000000000", f.n))
	exp := time.Now().Add(pairing.RequestTTL)
	f.requests[id] = fakePairingRequest{operator: in.Operator, label: in.Label, secret: secret, expires: exp}
	return pairing.Request{ID: id, Operator: in.Operator, Label: in.Label, ExpiresAt: exp}, secret, nil
}

func (f *fakePairingStore) Redeem(_ context.Context, id string, secret []byte) (pairing.Session, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reqRow, ok := f.requests[id]
	if !ok || reqRow.consumed || time.Now().After(reqRow.expires) || !bytes.Equal(reqRow.secret, secret) {
		return pairing.Session{}, false, nil
	}
	reqRow.consumed = true
	f.requests[id] = reqRow
	f.n++
	sessID := fmt.Sprintf("sess-%05d", f.n)
	sess := pairing.Session{
		ID: sessID, Operator: reqRow.operator, Label: reqRow.label, Scope: pairing.ApprovalsScopeV1,
		ExpiresAt: time.Now().Add(pairing.PairedTTL), CreatedAt: time.Now(),
	}
	f.sessions[sessID] = sess
	return sess, true, nil
}

func (f *fakePairingStore) GetLive(_ context.Context, id string) (pairing.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[id]
	if !ok || sess.RevokedAt != nil || !sess.ExpiresAt.After(time.Now()) {
		return pairing.Session{}, pairing.ErrNotFound
	}
	return sess, nil
}

func (f *fakePairingStore) Get(_ context.Context, id string) (pairing.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[id]
	if !ok {
		return pairing.Session{}, pairing.ErrNotFound
	}
	return sess, nil
}

func (f *fakePairingStore) ListForOperator(_ context.Context, operator, after string, limit int) ([]pairing.Session, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.sessions))
	for id := range f.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []pairing.Session
	var last string
	for _, id := range ids {
		if id <= after {
			continue
		}
		sess := f.sessions[id]
		if sess.Operator != operator {
			continue
		}
		out = append(out, sess)
		last = id
		if len(out) == limit {
			break
		}
	}
	return out, last, nil
}

func (f *fakePairingStore) Revoke(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sess, ok := f.sessions[id]
	if !ok || sess.RevokedAt != nil {
		return false, nil
	}
	now := time.Now()
	sess.RevokedAt = &now
	f.sessions[id] = sess
	return true, nil
}

// seedLive directly inserts a live session (bypassing Create/Redeem) so tests
// can mint a bearer against a known-live jti without an HTTP round trip.
func (f *fakePairingStore) seedLive(operator string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("sess-%05d", f.n)
	f.sessions[id] = pairing.Session{
		ID: id, Operator: operator, Scope: pairing.ApprovalsScopeV1,
		ExpiresAt: time.Now().Add(pairing.PairedTTL), CreatedAt: time.Now(),
	}
	return id
}

// seedExpired directly inserts an already-expired (never revoked) session.
func (f *fakePairingStore) seedExpired(operator string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("sess-%05d", f.n)
	f.sessions[id] = pairing.Session{
		ID: id, Operator: operator, Scope: pairing.ApprovalsScopeV1,
		ExpiresAt: time.Now().Add(-time.Minute), CreatedAt: time.Now().Add(-2 * time.Hour),
	}
	return id
}

// pairedHandler builds a real-signer/real-verifier handler wired with a fresh
// fakePairingStore and pairing enabled — the harness every test below shares.
func pairedHandler(t *testing.T) (http.Handler, *auth.Signer, *fakePairingStore) {
	t.Helper()
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "cluster-test", "api"))
	fp := newFakePairingStore()
	h := New(newFake(), nil, signer, verifier, "", "", WithPairingStore(fp), WithPairingEnabled(true))
	return h, signer, fp
}

func bearerRequest(method, path, body, tok string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

// --- happy path --------------------------------------------------------------

// TestCreateRedeemListRevokeHappyPath drives the full pairing lifecycle through
// the real HTTP handlers: an authenticated desktop operator requests a
// pairing, the (unauthenticated) device redeems it for a narrowed session
// cookie, the desk lists it, revokes it, and the revoked session's own bearer
// is then refused live (SEC-34) — one test proving GOV-12/GOV-14/SEC-34/SEC-36
// together on the real wire shapes.
func TestCreateRedeemListRevokeHappyPath(t *testing.T) {
	h, signer, _ := pairedHandler(t)
	deskTok, err := signer.IssueOperator("operator:wendy", "api")
	if err != nil {
		t.Fatalf("issue desk token: %v", err)
	}

	createRec := httptest.NewRecorder()
	h.ServeHTTP(createRec, bearerRequest(http.MethodPost, "/v1/pairing-requests", `{"label":"my-phone"}`, deskTok))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", createRec.Code, createRec.Body)
	}
	var created PairingRequestCreated
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.Id == "" || created.Secret == "" {
		t.Fatalf("create: missing id/secret: %+v", created)
	}

	// Redeem — PUBLIC, no bearer at all.
	redeemReq := httptest.NewRequest(http.MethodPost, "/v1/pairing-requests/"+created.Id+"/redeem",
		strings.NewReader(fmt.Sprintf(`{"secret":%q}`, created.Secret)))
	redeemReq.Header.Set("Content-Type", "application/json")
	redeemRec := httptest.NewRecorder()
	h.ServeHTTP(redeemRec, redeemReq)
	if redeemRec.Code != http.StatusOK {
		t.Fatalf("redeem: want 200, got %d: %s", redeemRec.Code, redeemRec.Body)
	}
	sessionCk := findCookie(t, redeemRec, sessionCookieName)
	if sessionCk == nil || sessionCk.Value == "" {
		t.Fatal("redeem: no session cookie set")
	}
	if findCookie(t, redeemRec, csrfCookieName) == nil {
		t.Fatal("redeem: no csrf cookie set")
	}
	if strings.Contains(redeemRec.Body.String(), created.Secret) {
		t.Fatal("redeem response must never echo the secret back")
	}

	// List — the desk operator sees the new session (and never its secret).
	listRec := httptest.NewRecorder()
	h.ServeHTTP(listRec, bearerRequest(http.MethodGet, "/v1/paired-sessions", "", deskTok))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d: %s", listRec.Code, listRec.Body)
	}
	var page pairedSessionsPage
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Scope != pairing.ApprovalsScopeV1 {
		t.Fatalf("list: unexpected items: %+v", page.Items)
	}
	if strings.Contains(listRec.Body.String(), created.Secret) {
		t.Fatal("list response must never carry the secret")
	}
	sessID := page.Items[0].Id

	// The redeemed session's own bearer works BEFORE revoke.
	meBefore := httptest.NewRecorder()
	h.ServeHTTP(meBefore, bearerRequest(http.MethodGet, "/v1/auth/me", "", sessionCk.Value))
	if meBefore.Code != http.StatusOK {
		t.Fatalf("me before revoke: want 200, got %d: %s", meBefore.Code, meBefore.Body)
	}

	// Revoke.
	revokeRec := httptest.NewRecorder()
	h.ServeHTTP(revokeRec, bearerRequest(http.MethodPost, "/v1/paired-sessions/"+sessID+"/revoke", "", deskTok))
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke: want 200, got %d: %s", revokeRec.Code, revokeRec.Body)
	}

	// GOV-14 contract: idempotent — a second revoke by the SAME owner still 200s.
	revokeRec2 := httptest.NewRecorder()
	h.ServeHTTP(revokeRec2, bearerRequest(http.MethodPost, "/v1/paired-sessions/"+sessID+"/revoke", "", deskTok))
	if revokeRec2.Code != http.StatusOK {
		t.Fatalf("second (idempotent) revoke: want 200, got %d: %s", revokeRec2.Code, revokeRec2.Body)
	}

	// SEC-34: the revoked session's own bearer is now refused live.
	meAfter := httptest.NewRecorder()
	h.ServeHTTP(meAfter, bearerRequest(http.MethodGet, "/v1/auth/me", "", sessionCk.Value))
	if meAfter.Code != http.StatusUnauthorized {
		t.Fatalf("me after revoke: want 401, got %d: %s", meAfter.Code, meAfter.Body)
	}
}

// --- SEC-34: revoked / expired refused ---------------------------------------

func TestPairedSessionRevokedAndExpiredRefused(t *testing.T) {
	h, signer, fp := pairedHandler(t)

	revokedID := fp.seedLive("operator:grace")
	if ok, err := fp.Revoke(context.Background(), revokedID); err != nil || !ok {
		t.Fatalf("setup revoke: ok=%v err=%v", ok, err)
	}
	revokedTok, err := signer.IssuePaired("operator:grace", "api", revokedID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue revoked: %v", err)
	}

	expiredID := fp.seedExpired("operator:heidi")
	expiredTok, err := signer.IssuePaired("operator:heidi", "api", expiredID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue expired: %v", err)
	}

	for name, tok := range map[string]string{"revoked": revokedTok, "expired": expiredTok} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/auth/me", "", tok))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s paired session: want 401, got %d: %s", name, rec.Code, rec.Body)
			}
		})
	}
}

// --- SEC-35: the approval surface is reachable; everything else is refused --

func TestPairedSessionReachesApprovalSurface(t *testing.T) {
	h, signer, fp := pairedHandler(t)
	sessID := fp.seedLive("operator:frank")
	tok, err := signer.IssuePaired("operator:frank", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/auth/me", "", tok))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth/me via a live paired session: want 200, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"subject":"operator:frank"`) {
		t.Fatalf("me: unexpected body: %s", rec.Body)
	}
}

// TestPairedSessionDeniesNamedActions asserts SEC-35's own named denials
// explicitly, on a LIVE paired session (so the refusal is provably the
// approvals@1 route check, not a liveness failure): launching a Run, issuing
// or revoking a grant, registering or removing a context source, minting an
// agent token, opening a terminal session, and creating a further pairing.
func TestPairedSessionDeniesNamedActions(t *testing.T) {
	h, signer, fp := pairedHandler(t)
	sessID := fp.seedLive("operator:eve")
	tok, err := signer.IssuePaired("operator:eve", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	cases := []struct {
		name, method, path, body string
	}{
		{"launch a Run", http.MethodPost, "/v1/runs", `{}`},
		{"issue a grant", http.MethodPost, "/v1/grants", `{}`},
		{"revoke a grant", http.MethodPost, "/v1/grants/g-1/revoke", ""},
		{"register a context source", http.MethodPost, "/v1/context-sources", `{}`},
		{"remove a context source", http.MethodDelete, "/v1/context-sources/cs-1", ""},
		{"mint an agent token", http.MethodPost, "/v1/agent-tokens", `{}`},
		{"open a terminal session", http.MethodPost, "/v1/sessions", `{}`},
		{"create a further pairing", http.MethodPost, "/v1/pairing-requests", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, bearerRequest(tc.method, tc.path, tc.body, tok))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s %s: want 403 (refused), got %d: %s", tc.method, tc.path, rec.Code, rec.Body)
			}
		})
	}
}

// --- SEC-39: no sliding refresh on the paired plane --------------------------

func TestSlidingRefreshSkipsPairedSession(t *testing.T) {
	h, signer, fp := pairedHandler(t)
	sessID := fp.seedLive("operator:paired-refresh")

	// Deep inside the refresh window (mirrors TestSlidingRefresh's OperatorTTL/4
	// trick; PairedTTL == OperatorTTL by design, ADR-0068 §D4).
	pairedTok, err := signer.Issue(auth.IssueParams{
		Subject: "operator:paired-refresh", Edge: "human-agent", Audience: "api",
		Scope: auth.Scope{Name: pairing.ApprovalsScopeV1}, TTL: auth.PairedTTL / 4, TokenID: sessID,
	})
	if err != nil {
		t.Fatalf("issue paired: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/auth/me", "", pairedTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", rec.Code, rec.Body)
	}
	if findCookie(t, rec, sessionCookieName) != nil {
		t.Fatal("SEC-39: a paired session in the refresh window must NOT get a rotated cookie")
	}

	// Sanity: a desktop token in the SAME window still refreshes.
	deskTok, err := signer.Issue(auth.IssueParams{
		Subject: "operator:desk-refresh", Edge: "human-agent", Audience: "api", TTL: auth.OperatorTTL / 4,
	})
	if err != nil {
		t.Fatalf("issue desk: %v", err)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, bearerRequest(http.MethodGet, "/v1/auth/me", "", deskTok))
	if rec2.Code != http.StatusOK {
		t.Fatalf("me desk: want 200, got %d: %s", rec2.Code, rec2.Body)
	}
	if findCookie(t, rec2, sessionCookieName) == nil {
		t.Fatal("a desktop token in the same refresh window must still be rotated")
	}
}

// --- GOV-14: logout from the phone revokes its own row -----------------------

func TestLogoutRevokesPairedSession(t *testing.T) {
	h, signer, fp := pairedHandler(t)
	sessID := fp.seedLive("operator:phone-owner")
	tok, err := signer.IssuePaired("operator:phone-owner", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if _, err := fp.GetLive(context.Background(), sessID); err != nil {
		t.Fatalf("setup: session not live: %v", err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodPost, "/v1/auth/logout", "", tok))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", rec.Code)
	}

	if _, err := fp.GetLive(context.Background(), sessID); err == nil {
		t.Fatal("GOV-14: logout from a paired session must revoke its own row")
	}
}

// --- AUD-34: an approval from a paired session is attributable as such ------

// TestPairingResolvedByOriginMarker unit-tests pairingResolvedBy directly (via
// the real RequireCapability middleware, so the context is populated exactly
// as production does it): unchanged for a desktop caller (byte-identical to
// every pre-T-114b resolvedBy value — the existing TestResolveInvocationProxied
// asserting resolvedBy=="operator" is the regression proof), suffixed
// "#paired" for a live paired-scope caller. Never the raw session id, never a
// label.
func TestPairingResolvedByOriginMarker(t *testing.T) {
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "cluster-test", "api"))

	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = pairingResolvedBy("operator:zelda", r)
	})
	wrapped := mw.RequireCapability(verifier, "human-agent")(inner)

	deskTok, err := signer.IssueOperator("operator:zelda", "api")
	if err != nil {
		t.Fatalf("issue desk: %v", err)
	}
	wrapped.ServeHTTP(httptest.NewRecorder(), bearerRequest(http.MethodPost, "/x", "", deskTok))
	if got != "operator:zelda" {
		t.Fatalf("desk: pairingResolvedBy = %q, want unchanged operator:zelda", got)
	}

	pairedTok, err := signer.IssuePaired("operator:zelda", "api", "sess-1", auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue paired: %v", err)
	}
	wrapped.ServeHTTP(httptest.NewRecorder(), bearerRequest(http.MethodPost, "/x", "", pairedTok))
	if got != "operator:zelda#paired" {
		t.Fatalf("paired: pairingResolvedBy = %q, want operator:zelda#paired", got)
	}
}

// TestResolveInvocationAUD34OriginMarker proves the marker is actually wired
// into the real approve/deny wire call (gateway.Client.Resolve's resolvedBy
// field — the only channel available without touching mcp-gateway/
// policy-service, which SEC-38 requires stay byte-unchanged).
func TestResolveInvocationAUD34OriginMarker(t *testing.T) {
	var gotBody string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"status":"resolved"}`))
	}))
	defer stub.Close()

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "cluster-test", "api"))
	sk, _, _, _ := auth.GenerateAsymmetricKey()
	agentSigner := auth.NewAsymmetricSigner(sk, "cluster-test")
	fp := newFakePairingStore()
	sessID := fp.seedLive("operator:zelda")
	h := New(newFake(), nil, signer, verifier, "", "", WithGateway(stub.URL), WithAgentSigner(agentSigner),
		WithPairingStore(fp), WithPairingEnabled(true))

	pairedTok, err := signer.IssuePaired("operator:zelda", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodPost, "/v1/invocations/inv-1/resolve", `{"decision":"approve"}`, pairedTok))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve via paired session: want 200, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(gotBody, `"resolvedBy":"operator:zelda#paired"`) {
		t.Fatalf("gateway body = %s, want resolvedBy operator:zelda#paired (AUD-34)", gotBody)
	}
}

// --- SEC-35/SEC-36 made mechanical: the route-walk completeness test --------

// TestPairingRouteWalkDenyByDefault is the ticket's named test (§2): it walks
// the REAL router (chi.Walk) and classifies every registered route as either
// (a) public (security: [], never reaches the pairing gate at all), (b)
// mapped to a permitted approvals@1 action, or (c) empirically refused for a
// LIVE paired session (a real HTTP round trip, not a table lookup) — deny-by-
// default made mechanical rather than aspirational. A route added later
// without a pairingRouteActions/pairingPublicRoutes entry falls into case (c)
// and must ALSO be refused, or this test fails.
//
// Scope note, stated honestly: the five /v1/internal/* routes (the gateway's
// service-service-only forwards) are excluded from this walk because this
// harness does not wire the stores that gate their registration (grants/runs/
// workstreams/memories/context-index) — they simply never appear in
// chi.Walk's output here. They are refused for a paired session by a
// completely separate, pre-existing mechanism (mw.RequireCapability(gatewayVerifier,
// "service-service") — a different verifier keyed on a different edge,
// SEC-20/SEC-38/SEC-40), not by gateByDeclaredSecurity at all.
func TestPairingRouteWalkDenyByDefault(t *testing.T) {
	h, signer, fp := pairedHandler(t)
	sessID := fp.seedLive("operator:mallory")
	tok, err := signer.IssuePaired("operator:mallory", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	router, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("server.New did not return a chi.Routes — cannot walk its route table")
	}

	var permitted, public, denied []string
	walkErr := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := pairingRouteKey{Method: method, Pattern: route}
		switch {
		case pairingPublicRoutes[key]:
			public = append(public, method+" "+route)
		case pairingRouteActions[key] != nil:
			actions := pairingRouteActions[key]
			if !allPermitted(actions) {
				t.Errorf("%s %s maps to an action outside ApprovalsV1Permit: %v", method, route, actions)
			}
			permitted = append(permitted, fmt.Sprintf("%s %s -> %v", method, route, actions))
		default:
			req := bearerRequest(method, fillPathParams(route), "", tok)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code >= 200 && rec.Code < 300 {
				t.Errorf("%s %s: unmapped route was NOT refused for a live paired session (got %d: %s)",
					method, route, rec.Code, rec.Body.String())
			}
			denied = append(denied, fmt.Sprintf("%s %s -> %d", method, route, rec.Code))
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("chi.Walk: %v", walkErr)
	}

	sort.Strings(permitted)
	sort.Strings(public)
	sort.Strings(denied)
	t.Logf("PERMITTED (approvals@1) routes (%d):\n  %s", len(permitted), strings.Join(permitted, "\n  "))
	t.Logf("PUBLIC (security: []) routes (%d):\n  %s", len(public), strings.Join(public, "\n  "))
	t.Logf("DENIED routes, empirically refused for a live paired session (%d):\n  %s", len(denied), strings.Join(denied, "\n  "))

	if len(permitted) == 0 {
		t.Fatal("no permitted routes were walked — pairingRouteActions is out of sync with the router")
	}
	if len(denied) == 0 {
		t.Fatal("no denied routes were walked — the deny-by-default posture was not exercised")
	}
}

// fillPathParams substitutes every {name} chi path parameter with a fixed
// placeholder — good enough since the pairing gate refuses these requests
// before any path-param-dependent handler logic ever runs.
func fillPathParams(pattern string) string {
	var b strings.Builder
	depth := 0
	for _, r := range pattern {
		switch {
		case r == '{':
			depth++
			if depth == 1 {
				b.WriteRune('x')
			}
		case r == '}':
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}
