package server

// rules_handler.go is the service-service CRUD surface over the T-148 rule
// store (ADR-0074, T-151a): create/list/get/enable-disable/delete on a
// policy_rules row. Mounted in the same RequireCapability("service-service")
// group as /v1/decisions and /v1/approvals/{sessionID}/resolve (server.go) —
// deliberately OFF the shared OpenAPI fragment, the same posture as
// ADR-0054's decide/resolve. api is the only intended caller; the
// operator-facing /v1/policy-rules surface and its SEC-53 human-agent guard
// are T-151b's, on services/api — nothing here enforces who authored a rule
// beyond "holds a service-service token".

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aireos.local/services/policy-service/internal/store"
)

// ruleBodyLimit bounds a rule request body. Rule fields are short tuple
// values (subject/edge/action/target/outcome/label), nowhere near decide's
// 4096-byte limit's justification, but there's no reason for a rules body to
// need a different ceiling than the rest of this file's handlers — reusing
// decideBodyLimit keeps one constant instead of two that must be kept in
// sync for no behavioral reason.
const ruleBodyLimit = 4096

// ruleResponse is the wire shape for a policy_rules row.
type ruleResponse struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Subject   string `json:"subject"`
	Edge      string `json:"edge"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Outcome   string `json:"outcome"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func toRuleResponse(r store.Rule) ruleResponse {
	return ruleResponse{
		ID: r.ID, Label: r.Label, Subject: r.Subject, Edge: r.Edge,
		Action: r.Action, Target: r.Target, Outcome: r.Outcome, Enabled: r.Enabled,
		CreatedAt: r.CreatedAt.Format(rfc3339), UpdatedAt: r.UpdatedAt.Format(rfc3339),
	}
}

const rfc3339 = "2006-01-02T15:04:05.999999999Z07:00" // time.RFC3339Nano

type createRuleRequest struct {
	Label   string `json:"label"`
	Subject string `json:"subject"`
	Edge    string `json:"edge"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Outcome string `json:"outcome"`
}

// createRuleHandler inserts a new rule. Field validation (non-empty tuple
// fields, outcome in {allow, requires_approval, deny}) happens store-side
// (store.CreateRule → ErrInvalidRule) so the rule vocabulary is defined once.
func createRuleHandler(st DecisionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRuleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, ruleBodyLimit)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		rule, err := st.CreateRule(r.Context(), store.CreateRuleInput{
			Label: req.Label, Subject: req.Subject, Edge: req.Edge,
			Action: req.Action, Target: req.Target, Outcome: req.Outcome,
		})
		if err != nil {
			writeRuleError(w, "create rule", err)
			return
		}
		writeJSON(w, http.StatusCreated, toRuleResponse(rule))
	}
}

// listRulesHandler returns every rule (enabled and disabled), newest first —
// the same no-filter contract as store.ListRules.
func listRulesHandler(st DecisionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rules, err := st.ListRules(r.Context())
		if err != nil {
			writeRuleError(w, "list rules", err)
			return
		}
		out := make([]ruleResponse, len(rules))
		for i, rule := range rules {
			out[i] = toRuleResponse(rule)
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// getRuleHandler returns one rule by id.
func getRuleHandler(st DecisionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		rule, err := st.GetRule(r.Context(), id)
		if err != nil {
			writeRuleError(w, "get rule", err)
			return
		}
		writeJSON(w, http.StatusOK, toRuleResponse(rule))
	}
}

type patchRuleRequest struct {
	// Enabled is a pointer so a missing field (vs. an explicit false) is
	// distinguishable — PATCH's only settable field must be present.
	Enabled *bool `json:"enabled"`
}

// patchRuleHandler flips a rule's enabled flag — the only field this route
// mutates (SEC-54: a disabled rule must go inert without deleting its row).
func patchRuleHandler(st DecisionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req patchRuleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, ruleBodyLimit)).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Enabled == nil {
			http.Error(w, "enabled is required", http.StatusBadRequest)
			return
		}
		rule, err := st.SetRuleEnabled(r.Context(), id, *req.Enabled)
		if err != nil {
			writeRuleError(w, "set rule enabled", err)
			return
		}
		writeJSON(w, http.StatusOK, toRuleResponse(rule))
	}
}

// deleteRuleHandler removes a rule. Deleting a rule only removes the
// policy_rules row — the policy_outbox / audit-chain events any past decision
// under this rule already produced are a separate store and are untouched
// (SEC-54).
func deleteRuleHandler(st DecisionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := st.DeleteRule(r.Context(), id); err != nil {
			writeRuleError(w, "delete rule", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeRuleError maps the typed store errors from rules.go to their HTTP
// status, never leaking a raw (e.g. pg) error string to the caller. op names
// the operation in the server log line for the 500 path, matching the
// decide/resolve handlers' log.Printf shape.
func writeRuleError(w http.ResponseWriter, op string, err error) {
	var invalid store.ErrInvalidRule
	switch {
	case errors.As(err, &invalid):
		http.Error(w, invalid.Error(), http.StatusBadRequest)
	case errors.Is(err, store.ErrRuleNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, store.ErrDuplicateRule):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.Printf("policy %s: %v", op, err)
		http.Error(w, op+" failed", http.StatusInternalServerError)
	}
}
