package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/resource"
	"aireos.local/services/api/internal/session"
	"aireos.local/services/api/internal/store"
)

// fakeStore is an in-memory Store for handler tests.
type fakeStore struct {
	byID map[string]resource.Resource
	ids  []string
	n    int

	sessByID map[string]session.Session
	sessIDs  []string
	sn       int

	events []outbox.Event // RecordEvent captures auth/control-plane evidence (T-022)
}

// RecordEvent satisfies the Store interface (T-022); tests can assert f.events.
func (f *fakeStore) RecordEvent(_ context.Context, e outbox.Event) error {
	f.events = append(f.events, e)
	return nil
}

func newFake() *fakeStore {
	return &fakeStore{
		byID:     map[string]resource.Resource{},
		sessByID: map[string]session.Session{},
	}
}

func (f *fakeStore) Create(_ context.Context, in resource.Input) (resource.Resource, error) {
	f.n++
	id := fmt.Sprintf("res-%05d", f.n)
	r := resource.Resource{ID: id, Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}
	f.byID[id] = r
	f.ids = append(f.ids, id)
	return r, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (resource.Resource, error) {
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return resource.Resource{}, store.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, after string, limit int) ([]resource.Resource, string, error) {
	var out []resource.Resource
	var last string
	for _, id := range f.ids {
		if id <= after {
			continue
		}
		out = append(out, f.byID[id])
		last = id
		if len(out) == limit {
			break
		}
	}
	return out, last, nil
}

func (f *fakeStore) Update(_ context.Context, id string, in resource.Input) (resource.Resource, error) {
	if _, ok := f.byID[id]; !ok {
		return resource.Resource{}, store.ErrNotFound
	}
	r := resource.Resource{ID: id, Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}
	f.byID[id] = r
	return r, nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	if _, ok := f.byID[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

// --- session fake (mirrors the operator-scoped store semantics) -------------

func (f *fakeStore) CreateSession(_ context.Context, operatorID string, in session.CreateInput) (session.Session, error) {
	if _, ok := f.byID[in.ResourceID]; !ok { // unknown resource → FK 404
		return session.Session{}, store.ErrNotFound
	}
	f.sn++
	id := fmt.Sprintf("sess-%05d", f.sn)
	s := session.Session{
		ID: id, ResourceID: in.ResourceID, OperatorID: operatorID,
		Mode: in.Mode, Status: session.StatusPending, StartedAt: time.Now().UTC(),
	}
	f.sessByID[id] = s
	f.sessIDs = append(f.sessIDs, id)
	return s, nil
}

func (f *fakeStore) GetSession(_ context.Context, operatorID, id string) (session.Session, error) {
	if s, ok := f.sessByID[id]; ok && s.OperatorID == operatorID {
		return s, nil
	}
	return session.Session{}, store.ErrNotFound
}

func (f *fakeStore) ListSessions(_ context.Context, operatorID, after string, limit int) ([]session.Session, string, error) {
	var out []session.Session
	var last string
	for _, id := range f.sessIDs {
		if id <= after {
			continue
		}
		s := f.sessByID[id]
		if s.OperatorID != operatorID {
			continue
		}
		out = append(out, s)
		last = id
		if len(out) == limit {
			break
		}
	}
	return out, last, nil
}

func (f *fakeStore) EndSession(_ context.Context, operatorID, id string) (session.Session, error) {
	s, ok := f.sessByID[id]
	if !ok || s.OperatorID != operatorID {
		return session.Session{}, store.ErrNotFound
	}
	if s.Status == session.StatusPending || s.Status == session.StatusActive {
		now := time.Now().UTC()
		s.Status = session.StatusClosed
		s.EndedAt = &now
		s.ClosedReason = session.ReasonOperator
		f.sessByID[id] = s
	}
	return s, nil
}

func (f *fakeStore) ParkForApproval(_ context.Context, operatorID string, in session.CreateInput) (session.Session, error) {
	if _, ok := f.byID[in.ResourceID]; !ok {
		return session.Session{}, store.ErrNotFound
	}
	f.sn++
	id := fmt.Sprintf("sess-%05d", f.sn)
	s := session.Session{
		ID: id, ResourceID: in.ResourceID, OperatorID: operatorID,
		Mode: in.Mode, Status: session.StatusPendingApproval, StartedAt: time.Now().UTC(),
	}
	f.sessByID[id] = s
	f.sessIDs = append(f.sessIDs, id)
	return s, nil
}

func (f *fakeStore) ApproveSession(_ context.Context, operatorID, id string) (session.Session, error) {
	s, ok := f.sessByID[id]
	if !ok || s.OperatorID != operatorID || s.Status != session.StatusPendingApproval {
		return session.Session{}, store.ErrNotFound
	}
	s.Status = session.StatusPending
	f.sessByID[id] = s
	return s, nil
}

func (f *fakeStore) MarkClosed(_ context.Context, operatorID, id, reason string) (session.Session, error) {
	s, ok := f.sessByID[id]
	if !ok || s.OperatorID != operatorID {
		return session.Session{}, store.ErrNotFound
	}
	now := time.Now().UTC()
	s.Status = session.StatusClosed
	s.EndedAt = &now
	s.ClosedReason = reason
	f.sessByID[id] = s
	return s, nil
}

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "operator", Edge: "human-agent"}, nil
}

// TestIssueAgentToken — POST /v1/agent-tokens mints a scoped agent-tool token,
// audit-chains the mint, and the token verifies at the gateway (T-036, AG-1..3).
func TestIssueAgentToken(t *testing.T) {
	f := newFake()
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	// ADR-0060: agent-tool tokens are minted on the asymmetric agent plane. api
	// holds the secret; the gateway verifies with the public half.
	sk, pk, _, _ := auth.GenerateAsymmetricKey()
	agentSigner := auth.NewAsymmetricSigner(sk, "cluster-test")
	h := New(f, nil, signer, allowAll{}, "", "", WithAgentSigner(agentSigner))

	const resID = "01900000-0000-7000-8000-0000000000a2"
	body := `{"agentId":"wedge","resourceId":"` + resID + `","operations":["exec"]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-tokens", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; no cookie → no CSRF
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Token == "" || resp.ExpiresAt == "" {
		t.Fatalf("missing token/expiresAt: %+v", resp)
	}

	// G1 cross-check: the api-minted token verifies under the GATEWAY's asymmetric
	// verifier config (api's PUBLIC key, audience mcp-gateway, agent-tool edge) — the
	// cross-service contract (ADR-0060; audience-alignment class behind ADR-0050).
	ct, err := auth.NewAsymmetricVerifier(pk, "cluster-test", "mcp-gateway").Verify(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("minted token must verify at the gateway: %v", err)
	}
	if ct.Edge != "agent-tool" || ct.Subject != "agent:wedge" {
		t.Fatalf("unexpected minted claims: %+v", ct)
	}
	if ct.Scope.Resource != resID {
		t.Fatalf("scope resource not bound: %+v", ct.Scope)
	}

	// AG-3: the mint is audit-chained (actor=operator, target=agent:<id>).
	var minted *outbox.Event
	for i := range f.events {
		if f.events[i].Outcome == "auth.agent-token.mint" {
			minted = &f.events[i]
		}
	}
	if minted == nil {
		t.Fatal("no auth.agent-token.mint audit event recorded")
	}
	if minted.Actor != "operator" || minted.Target != "agent:wedge" || minted.Edge != "human-agent" {
		t.Fatalf("mint audit fields wrong: %+v", *minted)
	}
}

// TestIssueAgentTokenValidation — agentId + resourceId are required.
func TestIssueAgentTokenValidation(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "")
	r := httptest.NewRequest(http.MethodPost, "/v1/agent-tokens", strings.NewReader(`{"agentId":"x"}`))
	r.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing resourceId: status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

// --- invocation approval proxy (T-040b) -------------------------------------
// The api routes proxy to the gateway's internal plane. The stub gateway is a REAL
// httptest server, so these exercise the real api gateway client (path/verb/body/
// bearer); the live api->real-gateway->policy->exec chain is the T-041 capstone.

type capturedReq struct {
	method, path, auth, body string
}

// TestResolveInvocationProxied — the operator's approve is mapped to the policy verb
// and proxied to the gateway's exact route with a service-service bearer; the bearer
// verifies under the gateway's verifier config (the audience-alignment guard, ADR-0050).
func TestResolveInvocationProxied(t *testing.T) {
	var got capturedReq
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = capturedReq{method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"), body: string(b)}
		_, _ = w.Write([]byte(`{"status":"resolved"}`))
	}))
	defer stub.Close()

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	// ADR-0060: the api→gateway service token is signed on the asymmetric agent
	// plane (same key as agent-tool); the gateway verifies it with api's public key.
	sk, pk, _, _ := auth.GenerateAsymmetricKey()
	agentSigner := auth.NewAsymmetricSigner(sk, "cluster-test")
	h := New(newFake(), nil, signer, allowAll{}, "", "", WithGateway(stub.URL), WithAgentSigner(agentSigner))

	r := httptest.NewRequest(http.MethodPost, "/v1/invocations/inv-1/resolve", strings.NewReader(`{"decision":"approve"}`))
	r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if got.method != http.MethodPost || got.path != "/v1/invocations/inv-1/resolve" {
		t.Fatalf("gateway got %s %s, want POST /v1/invocations/inv-1/resolve", got.method, got.path)
	}
	if !strings.Contains(got.body, `"decision":"granted"`) || !strings.Contains(got.body, `"resolvedBy":"operator"`) {
		t.Fatalf("gateway body = %s, want granted + resolvedBy operator", got.body)
	}
	tok := strings.TrimPrefix(got.auth, "Bearer ")
	if tok == "" || tok == got.auth {
		t.Fatalf("api->gateway call missing a service-service bearer: %q", got.auth)
	}
	ct, err := auth.NewAsymmetricVerifier(pk, "cluster-test", "mcp-gateway").Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("the api-minted token must verify at the gateway (audience alignment): %v", err)
	}
	if ct.Edge != "service-service" {
		t.Fatalf("api->gateway token edge = %q, want service-service", ct.Edge)
	}
}

func TestResolveInvocationStatusMapping(t *testing.T) {
	cases := []struct {
		name             string
		gwStatus, wanted int
	}{
		{"not found", http.StatusNotFound, http.StatusNotFound},
		{"not pending", http.StatusConflict, http.StatusConflict},
		{"gateway outage", http.StatusInternalServerError, http.StatusBadGateway},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.gwStatus)
			}))
			defer stub.Close()
			key, _ := auth.GenerateKey()
			h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithGateway(stub.URL))
			r := httptest.NewRequest(http.MethodPost, "/v1/invocations/x/resolve", strings.NewReader(`{"decision":"deny"}`))
			r.Header.Set("Authorization", "Bearer x")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != c.wanted {
				t.Fatalf("gateway %d → api %d, want %d", c.gwStatus, rec.Code, c.wanted)
			}
		})
	}
}

func TestResolveInvocationBadDecision(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithGateway("http://unused.invalid"))
	r := httptest.NewRequest(http.MethodPost, "/v1/invocations/x/resolve", strings.NewReader(`{"decision":"maybe"}`))
	r.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
	}
}

// No WithGateway → the routes exist but answer 503 (dependency absent), not 404.
func TestInvocationRoutesUnconfigured(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/invocations", ""},
		{http.MethodPost, "/v1/invocations/x/resolve", `{"decision":"approve"}`},
	} {
		var rdr io.Reader
		if tc.body != "" {
			rdr = strings.NewReader(tc.body)
		}
		r := httptest.NewRequest(tc.method, tc.path, rdr)
		r.Header.Set("Authorization", "Bearer x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s unconfigured: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}

func TestListInvocationsProxied(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invocations" || r.Method != http.MethodGet {
			t.Errorf("gateway got %s %s, want GET /v1/invocations", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"invocations":[{"id":"inv-1","agent":"agent:x","resourceId":"r1","tool":"remote.exec","command":"uname -a","status":"pending_approval"}]}`))
	}))
	defer stub.Close()
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithGateway(stub.URL))
	rec := req(t, h, http.MethodGet, "/v1/invocations", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"command":"uname -a"`) {
		t.Fatalf("list body missing the operator command payload (ADR-0059): %s", rec.Body.String())
	}
}

// R4: the invocation resolve is a cookie-path mutation, so a cookie-authed POST
// without a matching X-CSRF-Token is rejected at the gate (before the handler).
func TestResolveInvocationCSRFRequired(t *testing.T) {
	h, _, _ := loginHandler(t)
	loginRec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	session := findCookie(t, loginRec, sessionCookieName)
	csrf := findCookie(t, loginRec, csrfCookieName)
	if session == nil || csrf == nil {
		t.Fatal("setup: cookies missing")
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/invocations/inv-1/resolve", strings.NewReader(`{"decision":"approve"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf.Value})
	// no X-CSRF-Token header → double-submit fails
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("resolve without csrf header: want 403, got %d", rec.Code)
	}
}

func req(t *testing.T, h http.Handler, method, path, body string, authHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if authHeader {
		r.Header.Set("Authorization", "Bearer test")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

const validBody = `{"type":"ssh","name":"edge-01","siteId":"eu","labels":{"env":"prod"}}`

// TestListAuditEventsProxies — the operator audit-timeline read-proxy (T-024):
// api forwards GET /v1/audit/events to audit-service's /v1/events with a service
// token, and passes the page through verbatim.
func TestListAuditEventsProxies(t *testing.T) {
	gotAuth := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/events" {
			t.Errorf("upstream path = %q, want /v1/events", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":"e1","outcome":"auth.login.success"}],"page":{}}`))
	}))
	defer upstream.Close()

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "")
	h := New(newFake(), nil, signer, allowAll{}, upstream.URL, "")

	rec := req(t, h, http.MethodGet, "/v1/audit/events?limit=50", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "auth.login.success") {
		t.Fatalf("proxied body missing the upstream event: %s", rec.Body.String())
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("upstream did not receive a service token: %q", gotAuth)
	}
}

// TestListAuditEventsUnavailableWithoutAuditURL — no AIREOS_AUDIT_URL => 503,
// not a crash or a silent empty page.
func TestListAuditEventsUnavailableWithoutAuditURL(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	rec := req(t, h, http.MethodGet, "/v1/audit/events", "", true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestCreateGetUpdateDelete(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")

	// create
	rec := req(t, h, http.MethodPost, "/v1/resources", validBody, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"id":"res-00001"`) {
		t.Fatalf("create: expected server-assigned id, got %s", rec.Body)
	}

	// get
	if rec := req(t, h, http.MethodGet, "/v1/resources/res-00001", "", true); rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	// get missing
	if rec := req(t, h, http.MethodGet, "/v1/resources/nope", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", rec.Code)
	}

	// update
	if rec := req(t, h, http.MethodPut, "/v1/resources/res-00001", `{"type":"vnc","name":"e2"}`, true); rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d", rec.Code)
	}
	// update missing
	if rec := req(t, h, http.MethodPut, "/v1/resources/nope", `{"type":"vnc","name":"e2"}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: want 404, got %d", rec.Code)
	}

	// delete
	if rec := req(t, h, http.MethodDelete, "/v1/resources/res-00001", "", true); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	if rec := req(t, h, http.MethodDelete, "/v1/resources/res-00001", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: want 404, got %d", rec.Code)
	}
}

func TestListReturnsItems(t *testing.T) {
	f := newFake()
	h := New(f, nil, nil, allowAll{}, "", "")
	for i := 0; i < 3; i++ {
		req(t, h, http.MethodPost, "/v1/resources", validBody, true)
	}
	rec := req(t, h, http.MethodGet, "/v1/resources", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items"`) || strings.Count(rec.Body.String(), `"id"`) != 3 {
		t.Fatalf("list: expected 3 items, got %s", rec.Body)
	}
}

// Validation: bad type and client-supplied id are both 400.
func TestCreateValidation(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	for _, body := range []string{
		`{"type":"telnet","name":"x"}`,            // type not in enum
		`{"name":"x"}`,                            // missing type
		`{"type":"ssh","name":"x","id":"forced"}`, // client-supplied id rejected (DisallowUnknownFields)
	} {
		if rec := req(t, h, http.MethodPost, "/v1/resources", body, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestUnauthorizedWithoutBearer(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	if rec := req(t, h, http.MethodGet, "/v1/resources", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without bearer, got %d", rec.Code)
	}
}

// loginHandler builds a real-signer / real-account handler for the cookie-path
// tests. The audience is always "api" — the same value the api verifier expects.
func loginHandler(t *testing.T) (http.Handler, *auth.Signer, auth.Key) {
	t.Helper()
	key, _ := auth.GenerateKey()
	const cluster = "cluster-test"
	hash, _ := auth.HashPassword("hunter2")
	op := account.New("admin", hash)
	signer := auth.NewSigner(key, cluster)
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, cluster, "api"))
	return New(newFake(), op, signer, verifier, "", ""), signer, key
}

// findCookie returns the Set-Cookie cookie with the given name, or nil.
func findCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	res := http.Response{Header: rec.Header()}
	for _, c := range res.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestLoginSetsCookies — login establishes the session via two cookies and
// returns expiresAt only in the body (no token; ADR-0047 §Decision).
func TestLoginSetsCookies(t *testing.T) {
	h, _, _ := loginHandler(t)

	rec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", rec.Code, rec.Body)
	}

	// Body must NOT contain a token (PASETO stays opaque to JS by construction).
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("login: body must not include token, got %s", rec.Body)
	}
	var lr struct {
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.ExpiresAt == "" {
		t.Fatalf("login: missing expiresAt in body: %s", rec.Body)
	}

	session := findCookie(t, rec, sessionCookieName)
	if session == nil || session.Value == "" {
		t.Fatal("login: aireos_session cookie not set")
	}
	if !session.HttpOnly {
		t.Fatal("login: aireos_session must be HttpOnly")
	}
	if !session.Secure {
		t.Fatal("login: aireos_session must be Secure")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: aireos_session SameSite want Strict, got %v", session.SameSite)
	}
	if session.Path != "/" {
		t.Fatalf("login: aireos_session Path want /, got %q", session.Path)
	}

	csrf := findCookie(t, rec, csrfCookieName)
	if csrf == nil || csrf.Value == "" {
		t.Fatal("login: aireos_csrf cookie not set")
	}
	if csrf.HttpOnly {
		t.Fatal("login: aireos_csrf must NOT be HttpOnly (SPA reads it)")
	}
	if !csrf.Secure || csrf.SameSite != http.SameSiteStrictMode {
		t.Fatal("login: aireos_csrf must be Secure + SameSite=Strict")
	}
	// 32B hex → 64 chars.
	if len(csrf.Value) != 64 {
		t.Fatalf("login: aireos_csrf len want 64 hex chars, got %d (%q)", len(csrf.Value), csrf.Value)
	}
}

// TestSessionCookieAuthenticates — the cookie path is the SPA's auth surface;
// /v1/auth/me accepts it without an Authorization header.
func TestSessionCookieAuthenticates(t *testing.T) {
	h, _, _ := loginHandler(t)

	loginRec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	session := findCookie(t, loginRec, sessionCookieName)
	if session == nil {
		t.Fatal("setup: no session cookie")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	meRec := httptest.NewRecorder()
	h.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me via cookie: want 200, got %d: %s", meRec.Code, meRec.Body)
	}
	if !strings.Contains(meRec.Body.String(), `"subject":"operator:admin"`) ||
		!strings.Contains(meRec.Body.String(), `"edge":"human-agent"`) {
		t.Fatalf("me: unexpected identity: %s", meRec.Body)
	}
}

// TestAuthorizationHeaderStillWorks — service-to-service callers send a raw
// bearer; the cookie-to-Authorization adapter must not regress that path.
func TestAuthorizationHeaderStillWorks(t *testing.T) {
	h, signer, _ := loginHandler(t)
	tok, err := signer.IssueOperator("operator:admin", "api")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("me via header: want 200, got %d", rec.Code)
	}
}

// TestCSRFRequiredOnMutationsWithCookie — POST/PUT/DELETE with a session cookie
// must carry a matching X-CSRF-Token header. No header / mismatch → 403.
func TestCSRFRequiredOnMutationsWithCookie(t *testing.T) {
	h, _, _ := loginHandler(t)
	loginRec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	session := findCookie(t, loginRec, sessionCookieName)
	csrf := findCookie(t, loginRec, csrfCookieName)
	if session == nil || csrf == nil {
		t.Fatal("setup: cookies missing")
	}

	// Helper to mint a POST to /v1/resources with cookies (and an optional
	// X-CSRF-Token header).
	post := func(headerVal string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/resources", strings.NewReader(validBody))
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrf.Value})
		if headerVal != "" {
			r.Header.Set(csrfHeaderName, headerVal)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	if code := post(""); code != http.StatusForbidden {
		t.Fatalf("no csrf header: want 403, got %d", code)
	}
	if code := post("nope"); code != http.StatusForbidden {
		t.Fatalf("mismatched csrf header: want 403, got %d", code)
	}
	if code := post(csrf.Value); code != http.StatusCreated {
		t.Fatalf("matching csrf header: want 201, got %d", code)
	}
}

// TestLogoutClearsCookies — POST /v1/auth/logout responds 204 with Set-Cookie
// headers that delete both session and csrf cookies (Max-Age<0).
func TestLogoutClearsCookies(t *testing.T) {
	h, _, _ := loginHandler(t)
	rec := req(t, h, http.MethodPost, "/v1/auth/logout", "", false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: want 204, got %d", rec.Code)
	}
	for _, name := range []string{sessionCookieName, csrfCookieName} {
		c := findCookie(t, rec, name)
		if c == nil {
			t.Fatalf("logout: %s clear-cookie not set", name)
		}
		if c.MaxAge >= 0 {
			t.Fatalf("logout: %s MaxAge want <0 (delete), got %d", name, c.MaxAge)
		}
	}
}

// TestSlidingRefresh — when an operator's verified token is in the second half
// of its TTL, the next request rotates both cookies inline with the response.
// We force the condition by issuing a custom-TTL token directly via Signer.
//
// The short TTL is computed from auth.OperatorTTL so the test stays correct
// if OperatorTTL is ever tuned (refresh threshold is OperatorTTL/2; we pick
// OperatorTTL/4 to be comfortably inside the refresh window without
// approaching expiry).
func TestSlidingRefresh(t *testing.T) {
	h, signer, _ := loginHandler(t)

	// Establish a baseline csrf cookie via Login so we can assert that the
	// refresh ROTATES it (not just sets one where none existed).
	loginRec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	originalCSRF := findCookie(t, loginRec, csrfCookieName)
	if originalCSRF == nil {
		t.Fatal("setup: login did not set csrf cookie")
	}

	// Token with OperatorTTL/4 TTL — comfortably inside the refresh window.
	shortTok, err := signer.Issue(auth.IssueParams{
		Subject:  "operator:admin",
		Edge:     "human-agent",
		Audience: "api",
		TTL:      auth.OperatorTTL / 4,
	})
	if err != nil {
		t.Fatalf("issue short: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: shortTok})
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: originalCSRF.Value})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", rec.Code, rec.Body)
	}
	rotatedSession := findCookie(t, rec, sessionCookieName)
	if rotatedSession == nil {
		t.Fatal("refresh: no new session cookie issued on response")
	}
	if rotatedSession.Value == shortTok {
		t.Fatal("refresh: session value unchanged (expected rotation)")
	}
	rotatedCSRF := findCookie(t, rec, csrfCookieName)
	if rotatedCSRF == nil {
		t.Fatal("refresh: csrf cookie not rotated alongside session")
	}
	if rotatedCSRF.Value == originalCSRF.Value {
		t.Fatal("refresh: csrf value unchanged (expected rotation alongside session)")
	}

	// Sanity: a fresh full-TTL operator token must NOT trigger refresh.
	longTok, _ := signer.IssueOperator("operator:admin", "api")
	r2 := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	r2.AddCookie(&http.Cookie{Name: sessionCookieName, Value: longTok})
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("me long: want 200, got %d", rec2.Code)
	}
	if findCookie(t, rec2, sessionCookieName) != nil {
		t.Fatal("refresh: long-TTL token must not trigger rotation")
	}
}

// TestOperatorLoginAndMe — end-to-end: login establishes session via cookies;
// the same session cookie unlocks /v1/auth/me; bad password → 401;
// /v1/auth/me without anything → 401.
func TestOperatorLoginAndMe(t *testing.T) {
	h, _, _ := loginHandler(t)

	rec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", rec.Code, rec.Body)
	}
	session := findCookie(t, rec, sessionCookieName)
	if session == nil {
		t.Fatal("login: no session cookie")
	}

	meReq := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	meReq.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session.Value})
	meRec := httptest.NewRecorder()
	h.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", meRec.Code, meRec.Body)
	}

	// wrong password → 401
	if rec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"nope"}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: want 401, got %d", rec.Code)
	}
	// /v1/auth/me without a token → 401
	if rec := req(t, h, http.MethodGet, "/v1/auth/me", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: want 401, got %d", rec.Code)
	}
}

// seedResource creates a resource through the API and returns its server-assigned
// id, so the session tests reference a real (FK-satisfying) resourceId.
func seedResource(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := req(t, h, http.MethodPost, "/v1/resources", validBody, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed resource: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.ID == "" {
		t.Fatalf("seed resource: no id in %s", rec.Body)
	}
	return r.ID
}

// TestSessionCreateGetEnd — happy path across the four routes: create against a
// real resource (201 pending), fetch it (200), end it (200 closed/operator),
// re-end is idempotent (still 200, still closed).
func TestSessionCreateGetEnd(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	resID := seedResource(t, h)

	rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create session: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create session: bad body %s", rec.Body)
	}
	if created.ID == "" || created.Status != session.StatusPending || created.Mode != session.DefaultMode {
		t.Fatalf("create session: unexpected %+v", created)
	}
	if created.OperatorID != "operator" {
		t.Fatalf("create session: operator scope want 'operator', got %q", created.OperatorID)
	}

	if rec := req(t, h, http.MethodGet, "/v1/sessions/"+created.ID, "", true); rec.Code != http.StatusOK {
		t.Fatalf("get session: want 200, got %d", rec.Code)
	}
	if rec := req(t, h, http.MethodGet, "/v1/sessions/nope", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing session: want 404, got %d", rec.Code)
	}

	endRec := req(t, h, http.MethodPost, "/v1/sessions/"+created.ID+"/end", "", true)
	if endRec.Code != http.StatusOK {
		t.Fatalf("end session: want 200, got %d: %s", endRec.Code, endRec.Body)
	}
	var ended session.Session
	if err := json.Unmarshal(endRec.Body.Bytes(), &ended); err != nil {
		t.Fatalf("end session: bad body %s", endRec.Body)
	}
	if ended.Status != session.StatusClosed || ended.ClosedReason != session.ReasonOperator || ended.EndedAt == nil {
		t.Fatalf("end session: not closed/operator/stamped: %+v", ended)
	}
	// Idempotent re-end.
	if rec := req(t, h, http.MethodPost, "/v1/sessions/"+created.ID+"/end", "", true); rec.Code != http.StatusOK {
		t.Fatalf("re-end session: want 200, got %d", rec.Code)
	}
	if rec := req(t, h, http.MethodPost, "/v1/sessions/nope/end", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("end missing session: want 404, got %d", rec.Code)
	}
}

// TestSessionCreateValidation — unknown resourceId is 404; a malformed/over-posted
// body is 400 (missing resourceId, bad mode, or a client-supplied server field).
func TestSessionCreateValidation(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")

	if rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"missing"}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown resource: want 404, got %d", rec.Code)
	}
	for _, body := range []string{
		`{}`,                                   // missing resourceId
		`{"resourceId":"x","mode":"rdp"}`,      // mode not in enum
		`{"resourceId":"x","status":"active"}`, // client-supplied server field rejected
	} {
		if rec := req(t, h, http.MethodPost, "/v1/sessions", body, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

// TestSessionListReturnsItems — list is operator-scoped and returns the items
// envelope, same shape as resources.
func TestSessionListReturnsItems(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	resID := seedResource(t, h)
	for i := 0; i < 3; i++ {
		if rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true); rec.Code != http.StatusCreated {
			t.Fatalf("seed session %d: got %d", i, rec.Code)
		}
	}
	rec := req(t, h, http.MethodGet, "/v1/sessions", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions: want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items"`) || strings.Count(rec.Body.String(), `"id"`) != 3 {
		t.Fatalf("list sessions: expected 3 items, got %s", rec.Body)
	}
}

// TestSessionsRequireBearer — every session route is gated; no bearer → 401.
func TestSessionsRequireBearer(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/sessions"},
		{http.MethodPost, "/v1/sessions"},
		{http.MethodGet, "/v1/sessions/x"},
		{http.MethodPost, "/v1/sessions/x/end"},
	}
	for _, c := range cases {
		if rec := req(t, h, c.method, c.path, "", false); rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without bearer: want 401, got %d", c.method, c.path, rec.Code)
		}
	}
}

// seedFlaggedResource creates a requires_approval=true resource and returns its id.
func seedFlaggedResource(t *testing.T, h http.Handler) string {
	t.Helper()
	body := `{"type":"ssh","name":"gated-01","labels":{"requires_approval":"true"}}`
	rec := req(t, h, http.MethodPost, "/v1/resources", body, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed flagged resource: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var r struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.ID == "" {
		t.Fatalf("seed flagged resource: no id in %s", rec.Body)
	}
	return r.ID
}

// assertOneClosedPolicyUnavailable asserts the operator owns exactly one session,
// closed with reason policy_unavailable (the fail-closed parked session).
func assertOneClosedPolicyUnavailable(t *testing.T, h http.Handler) {
	t.Helper()
	rec := req(t, h, http.MethodGet, "/v1/sessions", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list sessions: %d", rec.Code)
	}
	var page struct {
		Items []session.Session `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("want 1 fail-closed session, got %d", len(page.Items))
	}
	if page.Items[0].Status != session.StatusClosed || page.Items[0].ClosedReason != session.ReasonPolicyUnavailable {
		t.Fatalf("parked session not fail-closed: %+v", page.Items[0])
	}
}

// TestSessionCreateRequiresApprovalParks — a session-start against a
// requires_approval resource is parked 'pending_approval' and the policy decision
// point is consulted with the session id + the gated-action tuple (ADR-0054).
func TestSessionCreateRequiresApprovalParks(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	pol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"requires_approval"}`))
	}))
	defer pol.Close()

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "")
	h := New(newFake(), nil, signer, allowAll{}, "", pol.URL)

	resID := seedFlaggedResource(t, h)
	rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create flagged session: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("bad body %s", rec.Body)
	}
	if created.Status != session.StatusPendingApproval {
		t.Fatalf("status = %q, want pending_approval", created.Status)
	}
	if gotPath != "POST /v1/decisions" {
		t.Fatalf("policy request line = %q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("policy did not receive a service token: %q", gotAuth)
	}
	if gotBody["session_id"] != created.ID || gotBody["target"] != "resource:"+resID ||
		gotBody["requires_approval"] != true || gotBody["edge"] != "agent-remote-session" {
		t.Fatalf("decide body = %+v", gotBody)
	}
}

// TestSessionCreateApprovalFailsClosedOnPolicyError — a decision-point error closes
// the parked session (policy_unavailable) and returns 502.
func TestSessionCreateApprovalFailsClosedOnPolicyError(t *testing.T) {
	pol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer pol.Close()

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "")
	h := New(newFake(), nil, signer, allowAll{}, "", pol.URL)

	resID := seedFlaggedResource(t, h)
	rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("policy error: want 502, got %d: %s", rec.Code, rec.Body)
	}
	assertOneClosedPolicyUnavailable(t, h)
}

// TestSessionCreateRequiresApprovalNoPolicyConfigured — a flagged resource with no
// decision point configured fails closed (503) + closes the parked session.
func TestSessionCreateRequiresApprovalNoPolicyConfigured(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "") // policyURL ""
	resID := seedFlaggedResource(t, h)
	rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no policy configured: want 503, got %d: %s", rec.Code, rec.Body)
	}
	assertOneClosedPolicyUnavailable(t, h)
}

// --- T-031b-ii: operator approve/deny resolution ----------------------------

// policyStub captures + scripts the httptest policy server's resolve response.
type policyStub struct {
	resolveCode    int    // status for /v1/approvals/.../resolve (0 => 200)
	resolveBody    string // body for that response
	gotResolvePath string
	gotResolveAuth string
	gotResolveBody map[string]any
}

// parkedHarness stands up an api handler wired to an httptest policy (decisions
// always requires_approval; resolve scripted by stub), seeds a flagged resource,
// and creates a parked 'pending_approval' session — returning the handler + id.
func parkedHarness(t *testing.T, stub *policyStub) (http.Handler, string) {
	t.Helper()
	pol := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/decisions":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"decision":"requires_approval"}`))
		case strings.HasSuffix(r.URL.Path, "/resolve"):
			stub.gotResolvePath = r.URL.Path
			stub.gotResolveAuth = r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&stub.gotResolveBody)
			code := stub.resolveCode
			if code == 0 {
				code = http.StatusOK
			}
			w.WriteHeader(code)
			if stub.resolveBody != "" {
				_, _ = w.Write([]byte(stub.resolveBody))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(pol.Close)

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "")
	h := New(newFake(), nil, signer, allowAll{}, "", pol.URL)
	resID := seedFlaggedResource(t, h)
	rec := req(t, h, http.MethodPost, "/v1/sessions", `{"resourceId":"`+resID+`"}`, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("park: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("park: bad body %s", rec.Body)
	}
	if created.Status != session.StatusPendingApproval {
		t.Fatalf("park: status %q, want pending_approval", created.Status)
	}
	return h, created.ID
}

func getSession(t *testing.T, h http.Handler, id string) session.Session {
	t.Helper()
	rec := req(t, h, http.MethodGet, "/v1/sessions/"+id, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("get session %s: %d", id, rec.Code)
	}
	var s session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("get session: bad body %s", rec.Body)
	}
	return s
}

// TestResolveApproveFlipsToPending — approve resolves the policy approval (granted,
// attributed to the operator) and flips the session pending_approval → pending.
func TestResolveApproveFlipsToPending(t *testing.T) {
	stub := &policyStub{resolveBody: `{"status":"granted","reason":"operator"}`}
	h, id := parkedHarness(t, stub)

	rec := req(t, h, http.MethodPost, "/v1/sessions/"+id+"/resolve", `{"decision":"approve"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var s session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("approve: bad body %s", rec.Body)
	}
	if s.Status != session.StatusPending {
		t.Fatalf("approve: status %q, want pending", s.Status)
	}
	if stub.gotResolvePath != "/v1/approvals/"+id+"/resolve" {
		t.Fatalf("resolve path = %q", stub.gotResolvePath)
	}
	if stub.gotResolveBody["decision"] != "granted" || stub.gotResolveBody["resolved_by"] != "operator" {
		t.Fatalf("resolve body = %+v", stub.gotResolveBody)
	}
	if !strings.HasPrefix(stub.gotResolveAuth, "Bearer ") {
		t.Fatalf("policy resolve did not get a service token: %q", stub.gotResolveAuth)
	}
}

// TestResolveDenyClosesSession — deny resolves (denied) and closes the session
// with reason approval_denied.
func TestResolveDenyClosesSession(t *testing.T) {
	stub := &policyStub{resolveBody: `{"status":"denied","reason":"operator"}`}
	h, id := parkedHarness(t, stub)

	rec := req(t, h, http.MethodPost, "/v1/sessions/"+id+"/resolve", `{"decision":"deny"}`, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("deny: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var s session.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("deny: bad body %s", rec.Body)
	}
	if s.Status != session.StatusClosed || s.ClosedReason != session.ReasonApprovalDenied {
		t.Fatalf("deny: %+v, want closed/approval_denied", s)
	}
	if stub.gotResolveBody["decision"] != "denied" {
		t.Fatalf("resolve body = %+v", stub.gotResolveBody)
	}
}

// TestResolveExpiredReconciles — policy reports no pending approval (409, the
// approval timed out); api reconciles the parked session to closed/approval_timeout
// and answers 409.
func TestResolveExpiredReconciles(t *testing.T) {
	stub := &policyStub{resolveCode: http.StatusConflict}
	h, id := parkedHarness(t, stub)

	rec := req(t, h, http.MethodPost, "/v1/sessions/"+id+"/resolve", `{"decision":"approve"}`, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expired: want 409, got %d: %s", rec.Code, rec.Body)
	}
	if s := getSession(t, h, id); s.Status != session.StatusClosed || s.ClosedReason != session.ReasonApprovalTimeout {
		t.Fatalf("expired: session %+v, want closed/approval_timeout", s)
	}
}

// TestResolveDoubleApproveConflicts — a second approve sees the session no longer
// pending_approval and answers 409 (idempotency / not-awaiting-approval).
func TestResolveDoubleApproveConflicts(t *testing.T) {
	stub := &policyStub{resolveBody: `{"status":"granted","reason":"operator"}`}
	h, id := parkedHarness(t, stub)

	if rec := req(t, h, http.MethodPost, "/v1/sessions/"+id+"/resolve", `{"decision":"approve"}`, true); rec.Code != http.StatusOK {
		t.Fatalf("first approve: want 200, got %d", rec.Code)
	}
	if rec := req(t, h, http.MethodPost, "/v1/sessions/"+id+"/resolve", `{"decision":"approve"}`, true); rec.Code != http.StatusConflict {
		t.Fatalf("second approve: want 409, got %d", rec.Code)
	}
}

// TestResolveUnknownSession — resolving an absent/not-owned session is 404.
func TestResolveUnknownSession(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "")
	rec := req(t, h, http.MethodPost, "/v1/sessions/nope/resolve", `{"decision":"approve"}`, true)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session: want 404, got %d", rec.Code)
	}
}

// TestResolveNoPolicyConfigured — a parked session with no decision point
// configured cannot be resolved: 503, the session stays parked.
func TestResolveNoPolicyConfigured(t *testing.T) {
	f := newFake()
	res, _ := f.Create(context.Background(), resource.Input{Type: "ssh", Name: "x", Labels: map[string]string{"requires_approval": "true"}})
	parked, _ := f.ParkForApproval(context.Background(), "operator", session.CreateInput{ResourceID: res.ID, Mode: session.DefaultMode})
	h := New(f, nil, nil, allowAll{}, "", "") // policyURL ""

	rec := req(t, h, http.MethodPost, "/v1/sessions/"+parked.ID+"/resolve", `{"decision":"approve"}`, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no policy: want 503, got %d: %s", rec.Code, rec.Body)
	}
	if got := f.sessByID[parked.ID]; got.Status != session.StatusPendingApproval {
		t.Fatalf("no policy: session must stay parked, got %q", got.Status)
	}
}

// TestResolveCSRFRequired — the resolve route is a cookie-path mutation, so it
// requires a matching X-CSRF-Token (rejected in the gate before the handler).
func TestResolveCSRFRequired(t *testing.T) {
	h, _, _ := loginHandler(t)
	loginRec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	sessionCk := findCookie(t, loginRec, sessionCookieName)
	if sessionCk == nil {
		t.Fatal("setup: no session cookie")
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/x/resolve", strings.NewReader(`{"decision":"approve"}`))
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionCk.Value})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("resolve without csrf: want 403, got %d", rec.Code)
	}
}
