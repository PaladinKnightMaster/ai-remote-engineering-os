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
	"context"
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

// DecisionStore is the subset of *store.Store the decide/resolve handlers
// need, narrow enough that a unit test can satisfy it with a DB-free fake
// (same seam as session-broker's SessionStore) — decideHandler now touches
// Postgres on every call (EvaluateTuple), so the pre-T-150 "nil pool is fine"
// unit-test shape no longer holds.
type DecisionStore interface {
	EvaluateTuple(ctx context.Context, t store.Tuple) (outcome string, winner store.Rule, ok bool, err error)
	RecordDecision(ctx context.Context, sessionID, subject, edge, target, outcome string) error
	CreatePending(ctx context.Context, in store.CreatePendingInput, ttl time.Duration) (store.PendingApproval, error)
	Resolve(ctx context.Context, sessionID, decision, resolvedBy string) (store.PendingApproval, error)
}

// Config carries the policy handler's dependencies.
type Config struct {
	Pool     *pgxpool.Pool
	Verifier mw.Verifier   // verifies api's service-service token (audience policy-service)
	TTL      time.Duration // pending-approval timeout written at decide time
	// Store overrides the default *store.Store — nil (the production wiring)
	// uses store.New(Pool). Exists so tests can inject a DB-free fake.
	Store DecisionStore
}

// New builds the policy-service HTTP handler: an ungated readiness endpoint plus
// the service-service-gated decide + resolve RPCs.
func New(cfg Config) http.Handler {
	st := cfg.Store
	if st == nil {
		st = store.New(cfg.Pool)
	}
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

// decideHandler returns the typed decision for a session-start (ADR-0054 §1),
// now actually consulting the T-149a evaluator (ADR-0074 §D2/§D4, T-150): the
// tuple is evaluated against every enabled rule, the most severe match wins,
// and — because severity beats specificity, not "no rule fired" — a rule can
// now say deny without a human present. When NO rule matches (ok=false), the
// decision falls back to today's v0.14.0 behavior, the resource's own
// requires_approval label (CON-4, §D3): ok=false is deliberately never
// treated as a matched allow.
func decideHandler(st DecisionStore, ttl time.Duration) http.HandlerFunc {
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

		outcome, _, ok, err := st.EvaluateTuple(r.Context(), store.Tuple{
			Subject: req.Subject, Edge: req.Edge, Action: req.Action, Target: req.Target,
		})
		if err != nil {
			log.Printf("policy decide: evaluate tuple: %v", err)
			http.Error(w, "decide failed", http.StatusInternalServerError)
			return
		}
		if !ok {
			// CON-4 fallback: no enabled rule matched this tuple at all — fall
			// back to the resource's requires_approval label exactly as
			// v0.14.0 did. This is NOT the same code path as a matched allow
			// rule (below); collapsing the two would mean a corrupted/absent
			// evaluator silently starts approving flagged resources.
			if req.RequiresApproval {
				outcome = store.DecisionRequiresApproval
			} else {
				outcome = store.DecisionAllow
			}
		}

		switch outcome {
		case store.DecisionAllow:
			if err := st.RecordDecision(r.Context(), req.SessionID, req.Subject, req.Edge, req.Target, store.OutcomeDecisionAllow); err != nil {
				log.Printf("policy decide: record allow: %v", err)
				http.Error(w, "decide failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, decisionResponse{Decision: store.DecisionAllow})
		case store.DecisionRequiresApproval:
			// CreatePending chains its own policy.decision:requires_approval
			// in the same transaction as the pending row (store.go) — do not
			// also call RecordDecision here, or a duplicate decide would
			// double-emit even though CreatePending itself stays idempotent.
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
		case store.DecisionDeny:
			if err := st.RecordDecision(r.Context(), req.SessionID, req.Subject, req.Edge, req.Target, store.OutcomeDecisionDeny); err != nil {
				log.Printf("policy decide: record deny: %v", err)
				http.Error(w, "decide failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, decisionResponse{Decision: store.DecisionDeny})
		default:
			// A matched rule whose outcome is none of the three decision
			// values is a corrupt row, not a decision anyone made — deny is
			// deliberately NOT synthesized here (CEO ruling 2026-08-28):
			// severityRank is a plain map, so Evaluate can return ok=true
			// with an outcome that ranks 0, and recording that as a denial
			// would put a fiction on the ledger. Fail closed loudly instead.
			log.Printf("policy decide: evaluator returned unrecognized outcome %q for session %s", outcome, req.SessionID)
			http.Error(w, "decide failed", http.StatusInternalServerError)
		}
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
func resolveHandler(st DecisionStore) http.HandlerFunc {
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
