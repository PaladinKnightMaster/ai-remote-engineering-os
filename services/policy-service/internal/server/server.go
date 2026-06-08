// Package server is policy-service's HTTP surface (ADR-0043: chi + the shared
// capability-edge middleware).
//
// T-031a-i (this slice) exposes an ungated readiness endpoint only. The gated
// internal decide/resolve RPCs (`edge: service-service`, consulted by
// services/api at session-create) land in T-031a-ii; like connector-service's
// lease RPC they are deliberately NOT in the shared OpenAPI fragment.
package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// New builds the policy-service HTTP handler. At T-031a-i that is the ungated
// readiness endpoint; T-031a-ii adds the service-service-gated decide/resolve
// RPCs (and the dependencies they need — a pool + a capability verifier).
func New() http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}
