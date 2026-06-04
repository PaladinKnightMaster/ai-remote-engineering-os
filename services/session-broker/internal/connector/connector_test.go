package connector_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aireos.local/services/session-broker/internal/connector"
)

// TestLeaseSendsContractAndDecodesMaterial verifies the wire contract the broker
// holds with connector-service's internal lease RPC: POST /v1/credentials/lease,
// the bearer token, the {resource_id,session_id,operator_subject} body, and a
// decoded {private_key,passphrase} response. connector-service itself is mocked
// here (httptest) — the LIVE broker->connector path is proven in T-030b.
func TestLeaseSendsContractAndDecodesMaterial(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"private_key": "-----BEGIN KEY-----\nx\n-----END KEY-----\n",
			"passphrase":  "pp",
		})
	}))
	defer srv.Close()

	c := &connector.Client{
		BaseURL: srv.URL,
		Token:   func(context.Context) (string, error) { return "svc-token", nil },
	}
	m, err := c.Lease(context.Background(), "res-1", "sess-1", "operator:admin")
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if gotPath != "POST /v1/credentials/lease" {
		t.Fatalf("request line = %q", gotPath)
	}
	if gotAuth != "Bearer svc-token" {
		t.Fatalf("authorization = %q, want Bearer svc-token", gotAuth)
	}
	if gotBody["resource_id"] != "res-1" || gotBody["session_id"] != "sess-1" || gotBody["operator_subject"] != "operator:admin" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if m.PrivateKey == "" || m.Passphrase != "pp" {
		t.Fatalf("decoded material = %+v", m)
	}
}

// TestLeaseMapsNotFound — connector's 404 (no credential stored) surfaces as the
// ErrNoCredential sentinel so the channel handler can answer 424.
func TestLeaseMapsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no credential for resource", http.StatusNotFound)
	}))
	defer srv.Close()

	c := &connector.Client{BaseURL: srv.URL}
	if _, err := c.Lease(context.Background(), "res-1", "sess-1", "operator:admin"); !errors.Is(err, connector.ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
}

// TestLeaseOtherStatusIsError — a non-200/404 (e.g. 503 key unset, 401 bad token)
// is a hard error, never a silent empty credential.
func TestLeaseOtherStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &connector.Client{BaseURL: srv.URL}
	_, err := c.Lease(context.Background(), "res-1", "sess-1", "operator:admin")
	if err == nil || errors.Is(err, connector.ErrNoCredential) {
		t.Fatalf("want a non-sentinel error for 503, got %v", err)
	}
}
