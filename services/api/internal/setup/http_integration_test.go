//go:build integration

// End-to-end first-run setup against a real Postgres through the real HTTP handler (T-175b):
// the real store behind server.New, nothing faked. Lives beside the store tests (external
// package, same test binary, so it is serial with them) because both reset the singleton
// setup_codes/operators tables. Run exactly as the store tests are (see store_integration_test.go).
package setup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/server"
	"aireos.local/services/api/internal/setup"
)

// stubStore is server.Store with only the method the setup/login path calls.
type stubStore struct{ server.Store }

func (stubStore) RecordEvent(context.Context, outbox.Event) error { return nil }

func call(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestSetupEndToEndOverHTTP(t *testing.T) {
	dsn := os.Getenv("API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("API_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		if _, err := pool.Exec(ctx, `TRUNCATE operators, setup_codes; DELETE FROM api_outbox WHERE payload->>'outcome' LIKE 'setup.%'`); err != nil {
			t.Fatal(err)
		}
	}
	reset()
	t.Cleanup(func() { reset(); pool.Close() })

	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "c")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "c", "api"))
	st := setup.NewStore(pool, "api_outbox")
	holder := account.NewHolder(nil)
	h := server.New(stubStore{}, holder, signer, verifier, "", "", server.WithSetup(st, holder))
	const password = "correct horse battery"
	complete := func(h http.Handler, code string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"code": code, "username": "alice", "password": password})
		return call(h, http.MethodPost, "/v1/setup/complete", string(b))
	}

	locked, err := st.Issue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rec := call(h, http.MethodGet, "/v1/setup", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/setup: %d", rec.Code)
	}
	if rec := call(h, http.MethodGet, "/v1/resources", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("setup mode is not total: %d", rec.Code)
	}
	for i := 0; i < 5; i++ { // wrong attempts -> lockout; the RIGHT code is then refused too
		if rec := complete(h, "ZZZZZ-ZZZZZ-ZZZZZ-ZZZZZ"); rec.Code != http.StatusForbidden {
			t.Fatalf("wrong code %d: %d", i, rec.Code)
		}
	}
	if rec := complete(h, locked); rec.Code != http.StatusForbidden {
		t.Fatalf("right code after lockout: %d", rec.Code)
	}

	code, err := st.Issue(ctx) // re-issue, as `setup.sh --new-setup-code` does
	if err != nil {
		t.Fatal(err)
	}
	if rec := complete(h, strings.ToLower(code)); rec.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	// No restart: setup routes are gone, login works with the chosen password.
	unknown := call(h, http.MethodPost, "/v1/not-a-route", "{}")
	if rec := complete(h, code); rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() {
		t.Fatalf("replay after setup is not an unknown route: %d %q", rec.Code, rec.Body)
	}
	login := `{"username":"alice","password":"` + password + `"}`
	if rec := call(h, http.MethodPost, "/v1/auth/login", login); rec.Code != http.StatusOK {
		t.Fatalf("login after setup: %d %s", rec.Code, rec.Body)
	}

	// A restart: a fresh handler wired the way main wires a table operator (no WithSetup).
	op, err := st.Operator(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h2 := server.New(stubStore{}, op, signer, verifier, "", "")
	if rec := call(h2, http.MethodPost, "/v1/auth/login", login); rec.Code != http.StatusOK {
		t.Fatalf("login after restart: %d", rec.Code)
	}
	if rec := call(h2, http.MethodGet, "/v1/setup", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /v1/setup after restart: %d", rec.Code)
	}
	if _, err := st.Issue(ctx); err != setup.ErrOperatorExists {
		t.Fatalf("issue after setup: %v", err)
	}

	// SEC-67 api half over the whole real flow (issue, wrong, lockout, re-issue, completion):
	// the log must be free of code and password, and the check must first find a planted one.
	secrets := []string{locked, setup.Normalize(locked), code, setup.Normalize(code), password}
	has := func() bool {
		for _, s := range secrets {
			if strings.Contains(logs.String(), s) {
				return true
			}
		}
		return false
	}
	if !strings.Contains(logs.String(), "setup: complete; operator") {
		t.Fatalf("the flow logged nothing, so absence proves nothing: %q", logs.String())
	}
	if has() {
		t.Fatalf("an api log line carries the code or password: %q", logs.String())
	}
	log.Printf("PLANTED %s", code) // the check must be seen to FIND a secret
	if !has() {
		t.Fatal("PLANTED CONTROL FAILED: the leak check missed a planted code")
	}
}
