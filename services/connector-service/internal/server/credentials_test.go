package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mw "aireos.local/packages/go/middleware"
)

// apiVerifier is a valid service-service token for subject "api".
type apiVerifier struct{}

func (apiVerifier) Verify(_ context.Context, bearer string) (mw.Capability, error) {
	if bearer == "" {
		return mw.Capability{}, errors.New("empty token")
	}
	return mw.Capability{Subject: "api", Edge: "service-service"}, nil
}

const someID = "0190abcd-0000-7000-8000-000000000001"

var credRoutes = []struct{ method, path, body string }{
	{"POST", "/v1/credentials/" + someID + "/generate", `{"rotate":false,"operator_subject":"operator:admin"}`},
	{"GET", "/v1/credentials/" + someID + "/public", ``},
	{"GET", "/v1/credentials/pending/loopback-demo", ``},
	{"POST", "/v1/credentials/pending/loopback-demo/bind", `{"resource_id":"` + someID + `","operator_subject":"operator:admin"}`},
}

func status(t *testing.T, cfg Config, bearer, method, path, body string) int {
	t.Helper()
	srv := httptest.NewServer(New(cfg))
	defer srv.Close()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// SEC-66 (ADR-0079): the generation plane is api-only. A session-broker token (the
// lease plane's own subject) and a gateway token are valid service-service
// tokens and must still be refused, before any DB access.
func TestGenerationRoutesArePinnedToAPI(t *testing.T) {
	for _, v := range []mw.Verifier{stubVerifier{} /* session-broker */, wrongSubjectVerifier{} /* mcp-gateway */} {
		for _, rt := range credRoutes {
			if got := status(t, Config{Verifier: v, SecretKey: "k"}, "tok", rt.method, rt.path, rt.body); got != http.StatusForbidden {
				t.Errorf("%s %s with non-api subject = %d, want 403", rt.method, rt.path, got)
			}
		}
	}
}

func TestLeaseStillRefusesAPISubject(t *testing.T) {
	if got := leaseStatus(t, Config{Verifier: apiVerifier{}, SecretKey: "k"}, "tok", `{"resource_id":"r","session_id":"s","operator_subject":"o"}`); got != http.StatusForbidden {
		t.Fatalf("api-subject lease = %d, want 403", got)
	}
}

func TestGenerationInputValidation(t *testing.T) {
	cfg := Config{Verifier: apiVerifier{}, SecretKey: "k"}
	if got := status(t, cfg, "tok", "POST", "/v1/credentials/not-a-uuid/generate", `{}`); got != http.StatusBadRequest {
		t.Errorf("bad resource id = %d, want 400", got)
	}
	if got := status(t, cfg, "tok", "POST", "/v1/credentials/"+someID+"/generate", `{not json`); got != http.StatusBadRequest {
		t.Errorf("bad body = %d, want 400", got)
	}
	if got := status(t, cfg, "tok", "POST", "/v1/credentials/pending/x/bind", `{"resource_id":"nope"}`); got != http.StatusBadRequest {
		t.Errorf("bad bind resource = %d, want 400", got)
	}
	if got := status(t, cfg, "tok", "POST", "/v1/credentials/"+someID+"/generate", `{"rotate":true}`); got != http.StatusBadRequest {
		t.Errorf("generate without operator_subject = %d, want 400", got)
	}
	if got := status(t, cfg, "tok", "POST", "/v1/credentials/pending/x/bind", `{"resource_id":"`+someID+`"}`); got != http.StatusBadRequest {
		t.Errorf("bind without operator_subject = %d, want 400", got)
	}
	if got := status(t, Config{Verifier: apiVerifier{}}, "tok", "POST", "/v1/credentials/"+someID+"/generate", `{}`); got != http.StatusServiceUnavailable {
		t.Errorf("unconfigured key = %d, want 503", got)
	}
}
