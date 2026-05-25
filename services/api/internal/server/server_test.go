package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/resource"
	"aireos.local/services/api/internal/store"
)

// fakeStore is an in-memory Store for handler tests.
type fakeStore struct {
	byID map[string]resource.Resource
	ids  []string
	n    int
}

func newFake() *fakeStore { return &fakeStore{byID: map[string]resource.Resource{}} }

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

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "operator", Edge: "human-agent"}, nil
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

func TestCreateGetUpdateDelete(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{})

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
	h := New(f, nil, nil, allowAll{})
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
	h := New(newFake(), nil, nil, allowAll{})
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
	h := New(newFake(), nil, nil, allowAll{})
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
	return New(newFake(), op, signer, verifier), signer, key
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
func TestSlidingRefresh(t *testing.T) {
	h, signer, _ := loginHandler(t)

	// Token with 30m TTL — well under OperatorTTL/2 (4h) → must refresh.
	shortTok, err := signer.Issue(auth.IssueParams{
		Subject:  "operator:admin",
		Edge:     "human-agent",
		Audience: "api",
		TTL:      30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("issue short: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: shortTok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", rec.Code, rec.Body)
	}
	rotated := findCookie(t, rec, sessionCookieName)
	if rotated == nil {
		t.Fatal("refresh: no new session cookie issued on response")
	}
	if rotated.Value == shortTok {
		t.Fatal("refresh: session value unchanged (expected rotation)")
	}
	if findCookie(t, rec, csrfCookieName) == nil {
		t.Fatal("refresh: csrf cookie not rotated alongside session")
	}

	// Sanity: a long-TTL token (full 8h) must NOT trigger refresh.
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
