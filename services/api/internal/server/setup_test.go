package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/setup"
)

// First-run setup over HTTP (T-175b; ADR-0078; FRX-2/FRX-3, SEC-64, SEC-65, SEC-67 api half).
// The store's own guarantees (hash-only, single-use, DB singletons, concurrency) are proven
// against real Postgres in internal/setup; these tests prove what the HTTP layer adds, with
// a fake store that mimics only the observable contract.

const (
	goodCode     = "7K4MP-Q9RTV-XW2HB-3DNGS"
	goodPassword = "correct horse battery"
)

type fakeSetup struct {
	mu       sync.Mutex
	attempts int
	dead     bool // locked, used or expired -- the store collapses them all
	failWith error
	calls    int
	lastAddr string
}

func (f *fakeSetup) Complete(_ context.Context, code, username, password, addr string) (*account.Operator, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastAddr = addr
	if f.failWith != nil {
		return nil, f.failWith
	}
	if err := setup.ValidateCredentials(username, password); err != nil {
		return nil, err
	}
	if f.dead {
		return nil, setup.ErrInvalidCode
	}
	if setup.Normalize(code) != setup.Normalize(goodCode) {
		if f.attempts++; f.attempts >= setup.MaxAttempts {
			f.dead = true
		}
		return nil, setup.ErrInvalidCode
	}
	f.dead = true
	hash, _ := auth.HashPassword(password)
	return account.New(username, hash), nil
}

// setupHandler builds api in SETUP MODE, exactly as main wires it.
func setupHandler(t *testing.T) (http.Handler, *fakeSetup, *account.Holder, *fakeStore) {
	t.Helper()
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "cluster-test", "api"))
	holder, fs, f := account.NewHolder(nil), newFake(), &fakeSetup{}
	return New(fs, holder, signer, verifier, "", "", WithSetup(f, holder)), f, holder, fs
}

func do(h http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func completeBody(code, user, pw string) string {
	b, _ := json.Marshal(map[string]string{"code": code, "username": user, "password": pw})
	return string(b)
}

func complete(h http.Handler, code string) *httptest.ResponseRecorder {
	return do(h, http.MethodPost, "/v1/setup/complete", "application/json", completeBody(code, "alice", goodPassword))
}

// contractRoutes lists every method+path the OpenAPI fragment declares, so "every route"
// in FRX-2 is derived from the contract, not from a hand-kept list that could miss a route.
func contractRoutes(t *testing.T) [][2]string {
	t.Helper()
	f, err := os.Open("../../../../docs/api-contracts/openapi.v3.fragment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pathRE := regexp.MustCompile(`^  (/v1/\S+):\s*$`)
	methodRE := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	var routes [][2]string
	var cur string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "components:") {
			break
		}
		if m := pathRE.FindStringSubmatch(line); m != nil {
			cur = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(m[1], "x")
		} else if m := methodRE.FindStringSubmatch(line); m != nil && cur != "" {
			routes = append(routes, [2]string{strings.ToUpper(m[1]), cur})
		}
	}
	return routes
}

// FRX-2: while no operator exists EVERY route except the two setup routes is 503
// setup_required -- including login, /auth/me, resources, runs, audit reads and pairing --
// even for a caller who sends a bearer token and a session cookie.
func TestSetupModeIsTotal(t *testing.T) {
	h, _, _, _ := setupHandler(t)
	routes := contractRoutes(t)
	if len(routes) < 60 { // negative control: a scan that matched nothing must not pass
		t.Fatalf("contract scan found only %d routes; the scanner is broken", len(routes))
	}
	routes = append(routes,
		[2]string{"GET", "/"}, [2]string{"GET", "/v1/not-a-route"}, [2]string{"GET", "/healthz"},
		[2]string{"POST", "/v1/internal/delegated-runs"}, [2]string{"POST", "/v1/internal/plan-submissions"},
		[2]string{"GET", "/v1/setup/"}, [2]string{"POST", "/v1/setup/complete/"}, [2]string{"GET", "/v1/Setup"},
		[2]string{"GET", "/v1/%73etup"}, [2]string{"DELETE", "/v1/setup"}, [2]string{"GET", "/v1/setup/complete"})
	exempt := map[string]bool{"GET /v1/setup": true, "POST /v1/setup/complete": true}
	checked := 0
	for _, rt := range routes {
		if exempt[rt[0]+" "+rt[1]] {
			continue
		}
		r := httptest.NewRequest(rt[0], rt[1], strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer anything")
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "anything"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var e struct{ Code string }
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != http.StatusServiceUnavailable || e.Code != "setup_required" {
			t.Errorf("%s %s in setup mode: %d %s, want 503 setup_required", rt[0], rt[1], rec.Code, rec.Body)
		}
		checked++
	}
	t.Logf("%d routes checked; exempt: GET /v1/setup, POST /v1/setup/complete (none else)", checked)

	rec := do(h, http.MethodGet, "/v1/setup", "", "")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"setupRequired":true}` {
		t.Fatalf("GET /v1/setup: %d %s", rec.Code, rec.Body)
	}
}

// FRX-3: completion creates the operator, signs them in with the normal session cookies,
// ends setup mode in-process (no restart), and login then works with the new password.
func TestSetupCompletionSignsInAndEndsSetupMode(t *testing.T) {
	h, f, holder, fs := setupHandler(t)
	rec := do(h, http.MethodPost, "/v1/setup/complete", "application/json; charset=utf-8", completeBody("7k4mp q9rtv xw2hb 3dngs", "alice", goodPassword))
	if rec.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	sess, csrf := findCookie(t, rec, sessionCookieName), findCookie(t, rec, csrfCookieName)
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteStrictMode || csrf == nil || len(csrf.Value) != 64 {
		t.Fatalf("session cookies not set exactly as login sets them: %+v %+v", sess, csrf)
	}
	var lr struct{ ExpiresAt string }
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.ExpiresAt == "" || strings.Contains(rec.Body.String(), "token") {
		t.Fatalf("body is not login's: %s", rec.Body)
	}
	if f.lastAddr != "192.0.2.1" {
		t.Fatalf("client address passed to the store = %q", f.lastAddr)
	}
	if _, ok := holder.Authenticate("alice", goodPassword); !ok {
		t.Fatal("holder was not given the operator")
	}
	if len(fs.events) == 0 || fs.events[len(fs.events)-1].Actor != "operator:alice" {
		t.Fatalf("no session-establishment audit event for operator:alice: %+v", fs.events)
	}

	// The SAME handler, no restart: the cookie works, every route is live, login works.
	me := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	me.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sess.Value})
	if mrec := httptest.NewRecorder(); func() int { h.ServeHTTP(mrec, me); return mrec.Code }() != http.StatusOK || !strings.Contains(mrec.Body.String(), "operator:alice") {
		t.Fatalf("/v1/auth/me after setup: %s", mrec.Body)
	}
	if rec := do(h, http.MethodPost, "/v1/auth/login", "application/json", `{"username":"alice","password":"`+goodPassword+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("login with the new password: %d %s", rec.Code, rec.Body)
	}
	if rec := do(h, http.MethodPost, "/v1/auth/login", "application/json", `{"username":"alice","password":"not the password"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("login with a wrong password: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/v1/resources", "", ""); rec.Code == http.StatusServiceUnavailable {
		t.Fatalf("routes still answer 503 after setup: %s", rec.Body)
	}
}

// SEC-65: after setup, and when the operator came from the environment, both setup routes
// answer exactly as a route that does not exist: same status, same headers, same body --
// for every method, including a client replaying the page it saved.
func TestSetupRoutesAreUnknownRoutesAfterSetup(t *testing.T) {
	setupDone, _, _, _ := setupHandler(t)
	if rec := complete(setupDone, goodCode); rec.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	key, _ := auth.GenerateKey()
	hash, _ := auth.HashPassword("hunter2")
	envOperator := New(newFake(), account.New("admin", hash), auth.NewSigner(key, "c"),
		auth.AsMiddlewareVerifier(auth.NewVerifier(key, "c", "api")), "", "")

	for name, h := range map[string]http.Handler{"after setup": setupDone, "env operator": envOperator} {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
			unknown := do(h, method, "/v1/definitely-not-a-route", "application/json", completeBody(goodCode, "alice", goodPassword))
			if unknown.Code != http.StatusNotFound {
				t.Fatalf("[%s] control: an unknown route answered %d", name, unknown.Code)
			}
			for _, path := range []string{"/v1/setup", "/v1/setup/complete"} {
				got := do(h, method, path, "application/json", completeBody(goodCode, "alice", goodPassword))
				if got.Code != unknown.Code || got.Body.String() != unknown.Body.String() || !headersEqual(got.Header(), unknown.Header()) {
					t.Errorf("[%s] %s %s differs from an unknown route:\n got %d %v %q\nwant %d %v %q", name, method, path,
						got.Code, got.Header(), got.Body, unknown.Code, unknown.Header(), unknown.Body)
				}
			}
		}
	}
}

func headersEqual(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if strings.Join(v, ",") != strings.Join(b[k], ",") {
			return false
		}
	}
	return true
}

// The environment operator always wins (ADR-0078 §D4): with it configured no setup mode
// exists, the store is never reached, and login is the ordinary one.
func TestEnvOperatorWinsOverSetup(t *testing.T) {
	key, _ := auth.GenerateKey()
	hash, _ := auth.HashPassword("hunter2")
	f := &fakeSetup{}
	// A server given an env operator is built WITHOUT WithSetup (main.buildAuth never
	// passes it when the environment supplies one); the store must be unreachable.
	h := New(newFake(), account.New("admin", hash), auth.NewSigner(key, "c"),
		auth.AsMiddlewareVerifier(auth.NewVerifier(key, "c", "api")), "", "")
	if rec := do(h, http.MethodPost, "/v1/auth/login", "application/json", `{"username":"admin","password":"hunter2"}`); rec.Code != http.StatusOK {
		t.Fatalf("env operator login: %d", rec.Code)
	}
	if rec := complete(h, goodCode); rec.Code != http.StatusNotFound || f.calls != 0 {
		t.Fatalf("setup route with an env operator: %d, store calls %d", rec.Code, f.calls)
	}
}

// SEC-64 over HTTP, from the attacker's side: wrong, locked (the RIGHT code refused
// afterwards), expired and used codes all get the SAME refusal; none leaks the code; none
// creates an operator or ends setup mode.
func TestSetupCodeRefusals(t *testing.T) {
	h, f, holder, _ := setupHandler(t)
	wrong := complete(h, "AAAAA-AAAAA-AAAAA-AAAAA")
	if wrong.Code != http.StatusForbidden || !strings.Contains(wrong.Body.String(), "invalid_setup_code") ||
		strings.Contains(wrong.Body.String(), "AAAAA") {
		t.Fatalf("wrong code: %d %s", wrong.Code, wrong.Body)
	}
	for i := 1; i < setup.MaxAttempts; i++ {
		complete(h, "AAAAA-AAAAA-AAAAA-AAAAA")
	}
	locked := complete(h, goodCode) // the RIGHT code, after five wrong ones
	if locked.Code != wrong.Code || locked.Body.String() != wrong.Body.String() {
		t.Fatalf("locked answer differs from wrong-code answer: %d %s", locked.Code, locked.Body)
	}
	f.dead = true // expired / already-used codes are the same collapsed refusal at the store
	if rec := complete(h, goodCode); rec.Body.String() != wrong.Body.String() {
		t.Fatalf("dead-code answer is distinguishable: %s", rec.Body)
	}
	if _, ok := holder.Authenticate("alice", goodPassword); ok {
		t.Fatal("a refused code created an operator")
	}
	if rec := do(h, http.MethodGet, "/v1/setup", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("setup mode ended without a successful completion: %d", rec.Code)
	}
	// Input errors are 400, say why, and echo neither secret.
	h2, _, _, _ := setupHandler(t)
	rec := do(h2, http.MethodPost, "/v1/setup/complete", "application/json", completeBody(goodCode, "alice", "short"))
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), goodCode) || strings.Contains(rec.Body.String(), "short\"") {
		t.Fatalf("weak password: %d %s", rec.Code, rec.Body)
	}
}

// The code and password travel in a JSON body and nowhere else: a non-JSON type -- the
// only kinds a cross-site HTML form can send -- is refused before the store is reached, and
// a code in the URL is ignored.
func TestSetupCompleteRequiresJSONBodyOnly(t *testing.T) {
	h, f, _, _ := setupHandler(t)
	body := completeBody(goodCode, "alice", goodPassword)
	for _, ct := range []string{"", "text/plain", "text/plain;charset=UTF-8", "application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x", "application/jsonx", "application/json-patch+json"} {
		if rec := do(h, http.MethodPost, "/v1/setup/complete", ct, body); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: %d, want 415", ct, rec.Code)
		}
	}
	if rec := do(h, http.MethodPost, "/v1/setup/complete?code="+goodCode+"&username=alice&password=correct%20horse%20battery", "application/json", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("code in the URL: %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/v1/setup/complete", "application/json", `{"code":"x","username":"a","password":"b","extra":1}`); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d, want 400", rec.Code)
	}
	if rec := do(h, http.MethodPost, "/v1/setup/complete", "application/json", `{"code":"`+strings.Repeat("A", 5000)+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body: %d, want 400", rec.Code)
	}
	if f.calls != 0 {
		t.Fatalf("the store was reached %d times by requests that must not reach it", f.calls)
	}
}

// SEC-67, api half: no api log line carries the code or the password -- across a wrong
// attempt, a lockout, an internal failure and a completion. An absence check proves
// nothing until it is seen to find a secret, so it is first run on a log with one planted.
func TestSetupLogsNeverCarryCodeOrPassword(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	secrets := []string{goodCode, setup.Normalize(goodCode), goodPassword, "7k4mp q9rtv"}
	leaked := func() bool {
		for _, s := range secrets {
			if strings.Contains(buf.String(), s) {
				return true
			}
		}
		return false
	}

	log.Printf("PLANTED: submitted code %s", goodCode)
	if !leaked() {
		t.Fatal("PLANTED CONTROL FAILED: the leak check did not find a planted code")
	}
	buf.Reset()

	h, f, _, _ := setupHandler(t)
	for i := 0; i < setup.MaxAttempts; i++ { // wrong attempts, ending in the lockout
		complete(h, "AAAAA-AAAAA-AAAAA-AAAAA")
	}
	f.failWith = context.DeadlineExceeded
	complete(h, goodCode) // the 500 path logs the store error
	h2, _, _, _ := setupHandler(t)
	if rec := complete(h2, goodCode); rec.Code != http.StatusOK {
		t.Fatalf("complete: %d", rec.Code)
	}
	if !strings.Contains(buf.String(), "setup: completion failed") || !strings.Contains(buf.String(), "setup: complete; operator") {
		t.Fatalf("the paths under test logged nothing, so absence proves nothing: %q", buf.String())
	}
	if leaked() {
		t.Fatalf("a log line carries the code or password: %q", buf.String())
	}
}
