package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/policy-service/internal/server"
)

// allowVerifier accepts any bearer as a service-service caller (the audience
// policy-service expects). A request with no bearer is rejected by
// RequireCapability before this is consulted.
type allowVerifier struct{}

func (allowVerifier) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "api", Edge: "service-service"}, nil
}

func newHandler() http.Handler {
	return server.New(server.Config{Verifier: allowVerifier{}})
}

func TestHealthzOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: got %d %q", rec.Code, rec.Body.String())
	}
}

// TestDecideRequiresCapability — the gated RPC rejects a request with no bearer
// (401) before any handler/store work.
func TestDecideRequiresCapability(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader([]byte(`{}`)))
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: want 401, got %d", rec.Code)
	}
}

// TestDecideAllowUnflagged — an unflagged session-start (requires_approval=false)
// returns allow with no pending row and no DB access (so a nil pool is fine).
func TestDecideAllowUnflagged(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "subject": "operator:admin", "edge": "agent-remote-session",
		"target": "resource:r1", "action": "session.start", "requires_approval": false,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("decide allow: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("want allow, got %q", resp.Decision)
	}
	// allow carries no pending approval ref.
	if bytes.Contains(rec.Body.Bytes(), []byte("approval")) {
		t.Fatalf("allow decision must not carry an approval ref: %s", rec.Body.String())
	}
}

func TestDecideBadBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Authorization", "Bearer t")
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: want 400, got %d", rec.Code)
	}
}

func TestResolveBadDecision(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"decision": "maybe", "resolved_by": "operator:admin"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/s1/resolve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: want 400, got %d", rec.Code)
	}
}
