// Package server is connector-service's HTTP surface (ADR-0043: chi + the shared
// capability-edge middleware).
//
// The credential-lease endpoint (POST /v1/credentials/lease) is gated by
// RequireCapability over the service-service edge. It is deliberately an
// INTERNAL endpoint — NOT in the shared OpenAPI fragment — because its response
// carries decrypted credential material that must never surface in the
// browser-facing TS client generated from that fragment (T-029b consult; ADR-0053).
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/connector-service/internal/store"
)

// Config carries the lease handler's dependencies.
type Config struct {
	Pool      *pgxpool.Pool
	Verifier  mw.Verifier // verifies the broker's service-service token (audience connector-service)
	SecretKey string      // AIREOS_RESOURCE_SECRET_KEY; empty => lease answers 503 until provisioned
}

// New builds the connector-service HTTP handler: an ungated readiness endpoint
// plus the service-service-gated credential-lease endpoint.
func New(cfg Config) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireCapability(cfg.Verifier, "service-service"))
		r.Post("/v1/credentials/lease", leaseHandler(cfg))
	})
	return r
}

type leaseRequest struct {
	ResourceID      string `json:"resource_id"`
	SessionID       string `json:"session_id"`
	OperatorSubject string `json:"operator_subject"`
}

type leaseResponse struct {
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase,omitempty"`
}

// leaseHandler serves a credential lease to the session-broker. The egress is
// audited atomically with the decrypt (store.Lease); only the secret material is
// returned (host/port/user/ssh_host_key stay on resources.labels, which the
// broker reads itself).
func leaseHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// R5-ext (PIN-2, ADR-0060): the lease plane is session-broker-only — reject any
		// other workload's audience-matched service token (defense in depth over the
		// service-service edge). The broker is the sole credential lessee (ADR-0053).
		if capb, ok := mw.CapabilityFromContext(r.Context()); !ok || capb.Subject != "session-broker" {
			http.Error(w, "lease plane is session-broker-only", http.StatusForbidden)
			return
		}
		if cfg.SecretKey == "" {
			http.Error(w, "credential decryption not configured", http.StatusServiceUnavailable)
			return
		}
		var req leaseRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.ResourceID == "" || req.SessionID == "" || req.OperatorSubject == "" {
			http.Error(w, "resource_id, session_id and operator_subject are required", http.StatusBadRequest)
			return
		}
		m, err := store.Lease(r.Context(), cfg.Pool, req.ResourceID, req.SessionID, req.OperatorSubject, cfg.SecretKey)
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "no credential for resource", http.StatusNotFound)
			return
		}
		if err != nil {
			log.Printf("connector lease: %v", err)
			http.Error(w, "lease failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(leaseResponse{PrivateKey: m.PrivateKey, Passphrase: m.Passphrase})
	}
}
