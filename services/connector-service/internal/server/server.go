// Package server is connector-service's HTTP surface (ADR-0043: chi + the shared
// capability-edge middleware).
//
// T-029b-i lands only the ungated readiness endpoint (/healthz) so connector is
// a running, relay-draining service. The gated credential-lease endpoint
// (POST /v1/credentials/lease, RequireCapability service-service) is T-029b-ii —
// deliberately an internal endpoint, NOT in the shared OpenAPI fragment, because
// its response carries decrypted credential material that must never surface in
// the browser-facing TS client generated from that fragment.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New builds the connector-service HTTP handler. (Grows a Config with the
// credential store + capability verifier in T-029b-ii.)
func New() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}
