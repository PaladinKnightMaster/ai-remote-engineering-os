package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"aireos.local/services/policy-service/internal/server"
)

// TestHealthzOK — the readiness endpoint is ungated and returns 200 (the only
// surface at T-031a-i; the gated decide/resolve RPCs are T-031a-ii).
func TestHealthzOK(t *testing.T) {
	h := server.New()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz: want 200, got %d", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Fatalf("healthz body = %q, want ok", rec.Body.String())
	}
}
