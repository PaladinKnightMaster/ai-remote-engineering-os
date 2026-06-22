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

// stubVerifier accepts any non-empty bearer as a service-service token. The gate
// itself rejects a missing Authorization header before Verify is ever called.
type stubVerifier struct{}

func (stubVerifier) Verify(_ context.Context, bearer string) (mw.Capability, error) {
	if bearer == "" {
		return mw.Capability{}, errors.New("empty token")
	}
	return mw.Capability{Subject: "session-broker", Edge: "service-service"}, nil
}

// wrongSubjectVerifier is a valid service-service token whose subject is NOT the
// broker — the lease plane's subject-pin (PIN-2, ADR-0060) must reject it.
type wrongSubjectVerifier struct{}

func (wrongSubjectVerifier) Verify(_ context.Context, bearer string) (mw.Capability, error) {
	if bearer == "" {
		return mw.Capability{}, errors.New("empty token")
	}
	return mw.Capability{Subject: "mcp-gateway", Edge: "service-service"}, nil
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(New(Config{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
}

// lease posts to the lease endpoint with an optional bearer; returns the status.
func leaseStatus(t *testing.T, cfg Config, bearer, body string) int {
	t.Helper()
	srv := httptest.NewServer(New(cfg))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/credentials/lease", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
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

func TestLeaseUnauthorized(t *testing.T) {
	// No Authorization header → the gate 401s before the handler runs.
	if got := leaseStatus(t, Config{Verifier: stubVerifier{}, SecretKey: "k"}, "", `{}`); got != http.StatusUnauthorized {
		t.Fatalf("no-token lease status = %d, want 401", got)
	}
}

func TestLeaseUnconfigured(t *testing.T) {
	// Valid token but no secret key → 503 (decryption not configured).
	if got := leaseStatus(t, Config{Verifier: stubVerifier{}, SecretKey: ""}, "tok", `{"resource_id":"r","session_id":"s","operator_subject":"operator:admin"}`); got != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured lease status = %d, want 503", got)
	}
}

func TestLeaseBadRequest(t *testing.T) {
	// Gate passed, key configured, but missing required fields → 400 (before any DB).
	if got := leaseStatus(t, Config{Verifier: stubVerifier{}, SecretKey: "k"}, "tok", `{"resource_id":""}`); got != http.StatusBadRequest {
		t.Fatalf("bad-request lease status = %d, want 400", got)
	}
}

func TestLeaseRejectsNonBrokerSubject(t *testing.T) {
	// Valid service-service token, wrong workload subject → 403 (PIN-2), before any DB.
	if got := leaseStatus(t, Config{Verifier: wrongSubjectVerifier{}, SecretKey: "k"}, "tok",
		`{"resource_id":"r","session_id":"s","operator_subject":"operator:admin"}`); got != http.StatusForbidden {
		t.Fatalf("non-broker lease status = %d, want 403 (PIN-2)", got)
	}
}
