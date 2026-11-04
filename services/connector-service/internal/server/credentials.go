package server

// Key-generation routes (ADR-0079): service-service AND pinned to subject "api"
// (PIN-2 pattern, mirroring the lease). Responses are public material only; error
// bodies are fixed sentences that never echo request content or key bytes.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/connector-service/internal/store"
)

func requireAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capb, ok := mw.CapabilityFromContext(r.Context()); !ok || capb.Subject != "api" {
			http.Error(w, "credential generation plane is api-only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type publicResponse struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

// OperatorSubject is REQUIRED (as on the lease): the human the authenticated api
// claims to act for, recorded as the audit actor. connector cannot verify the
// claim - it trusts the api workload, exactly as the lease trusts the broker - but
// attributing a rotation to the operator, not to "api", is what ADR-0079 asks.
type generateRequest struct {
	Rotate          bool   `json:"rotate"`
	OperatorSubject string `json:"operator_subject"`
}

type bindRequest struct {
	ResourceID      string `json:"resource_id"`
	OperatorSubject string `json:"operator_subject"`
}

type pendingResponse struct {
	publicResponse
	Account string `json:"account"`
	HostKey string `json:"host_key"`
}

func writePublic(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// fail maps store errors to fixed, key-free responses.
func fail(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, store.ErrNoPublicKey):
		http.Error(w, "the credential was imported and has no recorded public key", http.StatusNotFound)
	case errors.Is(err, store.ErrExists):
		http.Error(w, "a credential already exists; request rotation explicitly to replace it", http.StatusConflict)
	default:
		log.Printf("connector %s: %v", op, err)
		http.Error(w, op+" failed", http.StatusInternalServerError)
	}
}

func resourceParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "resource_id"))
	if err != nil {
		http.Error(w, "resource_id must be a UUID", http.StatusBadRequest)
		return "", false
	}
	return id.String(), true
}

func mountCredentialRoutes(r chi.Router, cfg Config) {
	r.Use(requireAPI)
	r.Post("/v1/credentials/{resource_id}/generate", func(w http.ResponseWriter, r *http.Request) {
		id, ok := resourceParam(w, r)
		if !ok {
			return
		}
		if cfg.SecretKey == "" {
			http.Error(w, "credential encryption not configured", http.StatusServiceUnavailable)
			return
		}
		var req generateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.OperatorSubject == "" {
			http.Error(w, "operator_subject is required", http.StatusBadRequest)
			return
		}
		pub, err := store.Generate(r.Context(), cfg.Pool, id, cfg.SecretKey, req.OperatorSubject, req.Rotate)
		if err != nil {
			fail(w, "generate", err)
			return
		}
		writePublic(w, publicResponse{PublicKey: pub.PublicKey, Fingerprint: pub.Fingerprint})
	})
	r.Get("/v1/credentials/{resource_id}/public", func(w http.ResponseWriter, r *http.Request) {
		id, ok := resourceParam(w, r)
		if !ok {
			return
		}
		pub, err := store.PublicKey(r.Context(), cfg.Pool, id)
		if err != nil {
			fail(w, "public read", err)
			return
		}
		writePublic(w, publicResponse{PublicKey: pub.PublicKey, Fingerprint: pub.Fingerprint})
	})
	r.Get("/v1/credentials/pending/{label}", func(w http.ResponseWriter, r *http.Request) {
		p, err := store.PendingMetadata(r.Context(), cfg.Pool, chi.URLParam(r, "label"))
		if err != nil {
			fail(w, "pending read", err)
			return
		}
		writePublic(w, pendingResponse{
			publicResponse: publicResponse{PublicKey: p.PublicKey, Fingerprint: p.Fingerprint},
			Account:        p.Account, HostKey: p.HostKey,
		})
	})
	r.Post("/v1/credentials/pending/{label}/bind", func(w http.ResponseWriter, r *http.Request) {
		var req bindRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.OperatorSubject == "" {
			http.Error(w, "operator_subject is required", http.StatusBadRequest)
			return
		}
		id, err := uuid.Parse(req.ResourceID)
		if err != nil {
			http.Error(w, "resource_id must be a UUID", http.StatusBadRequest)
			return
		}
		pub, err := store.Bind(r.Context(), cfg.Pool, chi.URLParam(r, "label"), id.String(), req.OperatorSubject)
		if err != nil {
			fail(w, "bind", err)
			return
		}
		writePublic(w, publicResponse{PublicKey: pub.PublicKey, Fingerprint: pub.Fingerprint})
	})
}
