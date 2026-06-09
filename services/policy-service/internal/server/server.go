// Package server is policy-service's HTTP surface (ADR-0043: chi + the shared
// capability-edge middleware).
//
// The decide + resolve endpoints are gated by RequireCapability over the
// service-service edge and are deliberately INTERNAL — NOT in the shared OpenAPI
// fragment — the same posture as connector-service's lease RPC: services/api
// consults them (ADR-0054 §5), and the operator-facing approve/deny stays on
// services/api. /healthz is public.
package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/policy-service/internal/store"
)

// Config carries the policy handler's dependencies.
type Config struct {
	Pool     *pgxpool.Pool
	Verifier mw.Verifier   // verifies api's service-service token (audience policy-service)
	TTL      time.Duration // pending-approval timeout written at decide time
}

// New builds the policy-service HTTP handler: an ungated readiness endpoint plus
// the service-service-gated decide + resolve RPCs.
func New(cfg Config) http.Handler {
	st := store.New(cfg.Pool)
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireCapability(cfg.Verifier, "service-service"))
		r.Post("/v1/decisions", decideHandler(st, cfg.TTL))
		r.Post("/v1/approvals/{sessionID}/resolve", resolveHandler(st))
	})
	return r
}

type decideRequest struct {
	SessionID        string `json:"session_id"`
	Subject          string `json:"subject"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Action           string `json:"action"`
	RequiresApproval bool   `json:"requires_approval"`
}

type approvalRef struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type decisionResponse struct {
	Decision string       `json:"decision"`
	Approval *approvalRef `json:"approval,omitempty"`
}

// decideHandler returns the typed decision for a session-start (ADR-0054 §1).
// requires_approval (the resource's label, supplied by api) parks a pending
// approval and returns requires_approval; otherwise allow (no row, no audit at
// v0.2.0 — api consults policy only for flagged resources; the allow audit + the
// rule engine are v0.3.0).
func decideHandler(st *store.Store, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req decideRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.SessionID == "" || req.Subject == "" || req.Edge == "" || req.Target == "" || req.Action == "" {
			http.Error(w, "session_id, subject, edge, target and action are required", http.StatusBadRequest)
			return
		}
		if !req.RequiresApproval {
			writeJSON(w, http.StatusOK, decisionResponse{Decision: store.DecisionAllow})
			return
		}
		pa, err := st.CreatePending(r.Context(), store.CreatePendingInput{
			SessionID: req.SessionID, Subject: req.Subject, Edge: req.Edge,
			Target: req.Target, Action: req.Action,
		}, ttl)
		if err != nil {
			log.Printf("policy decide: %v", err)
			http.Error(w, "decide failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, decisionResponse{
			Decision: store.DecisionRequiresApproval,
			Approval: &approvalRef{ID: pa.ID, ExpiresAt: pa.ExpiresAt},
		})
	}
}

type resolveRequest struct {
	Decision   string `json:"decision"` // granted | denied
	ResolvedBy string `json:"resolved_by"`
}

type resolveResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// resolveHandler records the operator's approve/deny on a pending approval. A
// session with no still-pending approval (already resolved or timed out) is 409.
func resolveHandler(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		var req resolveRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Decision != "granted" && req.Decision != "denied" {
			http.Error(w, "decision must be granted or denied", http.StatusBadRequest)
			return
		}
		if req.ResolvedBy == "" {
			http.Error(w, "resolved_by is required", http.StatusBadRequest)
			return
		}
		pa, err := st.Resolve(r.Context(), sessionID, req.Decision, req.ResolvedBy)
		if errors.Is(err, store.ErrNotPending) {
			http.Error(w, "no pending approval for session", http.StatusConflict)
			return
		}
		if err != nil {
			log.Printf("policy resolve: %v", err)
			http.Error(w, "resolve failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, resolveResponse{Status: pa.Status, Reason: pa.Reason})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
