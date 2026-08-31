package server

// policy_rules.go — the operator-facing /v1/policy-rules CRUD surface (T-151b,
// ADR-0074). api does NOT re-implement rule storage: every handler here
// forwards to policy-service's internal, off-fragment /v1/rules plane
// (T-151a, services/policy-service/internal/server/rules_handler.go) through
// a.policy (policyDecider, extended at T-151b — see server.go), the same
// forwarding posture the session handlers already use for Decide/Resolve.
//
// SEC-53 (block): "Rules are operator-authored only. No agent, run, charter,
// grant or agent-token subject can create, edit, enable or disable a rule."
// gateByDeclaredSecurity (server.go) applies RequireCapability(v,
// "human-agent", "service-service") uniformly to every declared-security
// route — correct for the rest of the operator surface, but it means
// mcp-gateway's service-service token would otherwise sail straight through
// to these routes. Every handler below additionally calls
// requireHumanAgentRule FIRST, closing that one gap. The other three
// initiator classes the audit sorted (agent-tool, paired device, run/charter/
// grant-via-gateway) are already refused upstream of this file — proven by a
// negative test per class in policy_rules_test.go, not by inspecting a
// contract-level capability tag (SEC-53's own wording: there is no per-route
// tag to inspect, gateByDeclaredSecurity's check is uniform).

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/policy"
)

// requireHumanAgentRule is SEC-53's handler-level guard. Called first by
// every policy-rule handler, before operatorSubject — a service-service
// caller (mcp-gateway) has a valid capability (subject non-empty) but the
// wrong edge, so operatorSubject alone would let it through.
func requireHumanAgentRule(w http.ResponseWriter, r *http.Request) bool {
	c, ok := mw.CapabilityFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return false
	}
	if c.Edge != "human-agent" {
		writeError(w, http.StatusForbidden, "forbidden", "policy rules are operator-authored only")
		return false
	}
	return true
}

// policyRuleView is the wire shape for a policy rule (PolicyRule schema).
type policyRuleView struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Subject   string    `json:"subject"`
	Edge      string    `json:"edge"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Outcome   string    `json:"outcome"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// toPolicyRuleView converts policy-service's wire shape (RFC3339Nano strings)
// to api's JSON contract (a proper date-time). A parse failure on
// policy-service's own timestamp would be an internal inconsistency, not a
// client error — it degrades to the zero time rather than panicking or
// dropping the field.
func toPolicyRuleView(r policy.Rule) policyRuleView {
	created, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
	updated, _ := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	return policyRuleView{
		ID: r.ID, Label: r.Label, Subject: r.Subject, Edge: r.Edge,
		Action: r.Action, Target: r.Target, Outcome: r.Outcome, Enabled: r.Enabled,
		CreatedAt: created, UpdatedAt: updated,
	}
}

// writePolicyRuleError maps the typed policy-client rule errors to their HTTP
// status. Mirrors rules_handler.go's writeRuleError on policy-service — the
// same three-way split, one hop further out.
func writePolicyRuleError(w http.ResponseWriter, err error) {
	var invalid policy.ErrInvalidRule
	switch {
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, "bad_request", invalid.Error())
	case errors.Is(err, policy.ErrRuleNotFound):
		writeError(w, http.StatusNotFound, "not_found", "policy rule not found")
	case errors.Is(err, policy.ErrDuplicateRule):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "policy rule request failed")
	}
}

// policyRuleIDNotFound validates id is a well-formed UUID BEFORE forwarding to
// policy-service (CON-7: a malformed id must not surface as a 500 through the
// whole chain — api answers 404 directly, fast, and with a clean message,
// mirroring the pairing store's uuid.Parse-before-query pattern
// (services/api/internal/pairing/store.go's GetLive/Get)). Returns true (and
// writes the 404) when id is NOT well-formed.
func policyRuleIDNotFound(w http.ResponseWriter, id string) bool {
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "policy rule not found")
		return true
	}
	return false
}

// createPolicyRuleBody is the create request (PolicyRuleInput). Decoded by
// hand with DisallowUnknownFields — the createContextSourceBody precedent —
// so a client-supplied id/enabled/createdAt/updatedAt answers 400 rather than
// being silently ignored.
type createPolicyRuleBody struct {
	Label   string `json:"label"`
	Subject string `json:"subject"`
	Edge    string `json:"edge"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Outcome string `json:"outcome"`
}

// CreatePolicyRule (POST /v1/policy-rules) authors a new rule. actor is
// ALWAYS the verified caller (operatorSubject(r)) — never a body field — the
// same server-side-only-actor posture as CreateResource/CreateContextSource.
// Field validation of the (subject, edge, action, target, outcome) tuple
// happens policy-service-side (store.CreateRule -> ErrInvalidRule), so the
// rule vocabulary stays defined in exactly one place.
func (a *apiHandlers) CreatePolicyRule(w http.ResponseWriter, r *http.Request) {
	if !requireHumanAgentRule(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.policy == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the policy decision point is not configured")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body createPolicyRuleBody
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	rule, err := a.policy.CreateRule(r.Context(), policy.CreateRuleInput{
		Label: body.Label, Subject: body.Subject, Edge: body.Edge, Action: body.Action,
		Target: body.Target, Outcome: body.Outcome, Actor: operator,
	})
	if err != nil {
		writePolicyRuleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toPolicyRuleView(rule))
}

// ListPolicyRules (GET /v1/policy-rules) returns every rule, enabled and
// disabled alike — the same no-filter contract as policy-service's store; no
// pagination (the fragment doesn't declare one — CON-2, rules are expected to
// stay small and operator-curated, unlike the resource registry).
func (a *apiHandlers) ListPolicyRules(w http.ResponseWriter, r *http.Request) {
	if !requireHumanAgentRule(w, r) {
		return
	}
	if a.policy == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the policy decision point is not configured")
		return
	}
	rules, err := a.policy.ListRules(r.Context())
	if err != nil {
		writePolicyRuleError(w, err)
		return
	}
	views := make([]policyRuleView, 0, len(rules))
	for _, ru := range rules {
		views = append(views, toPolicyRuleView(ru))
	}
	writeJSON(w, http.StatusOK, views)
}

// GetPolicyRule (GET /v1/policy-rules/{id}) fetches one rule by id.
func (a *apiHandlers) GetPolicyRule(w http.ResponseWriter, r *http.Request, id string) {
	if !requireHumanAgentRule(w, r) {
		return
	}
	if a.policy == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the policy decision point is not configured")
		return
	}
	if policyRuleIDNotFound(w, id) {
		return
	}
	rule, err := a.policy.GetRule(r.Context(), id)
	if err != nil {
		writePolicyRuleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPolicyRuleView(rule))
}

// patchPolicyRuleBody is the PATCH request (PolicyRulePatch). Enabled is a
// pointer — the same reason as policy-service's internal patchRuleRequest
// (rules_handler.go): encoding/json does not enforce "required" from the
// OpenAPI schema at decode time, so an absent field silently decodes as the
// zero value unless the handler distinguishes "absent" from "false" itself.
type patchPolicyRuleBody struct {
	Enabled *bool `json:"enabled"`
}

// PatchPolicyRule (PATCH /v1/policy-rules/{id}) flips a rule's enabled flag —
// the only field this route mutates (SEC-54). actor is ALWAYS the verified
// caller (operatorSubject(r)), same posture as CreatePolicyRule (AUD-40,
// T-151c — enable/disable is chained just like create is).
func (a *apiHandlers) PatchPolicyRule(w http.ResponseWriter, r *http.Request, id string) {
	if !requireHumanAgentRule(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.policy == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the policy decision point is not configured")
		return
	}
	if policyRuleIDNotFound(w, id) {
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body patchPolicyRuleBody
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "enabled is required")
		return
	}
	rule, err := a.policy.SetRuleEnabled(r.Context(), id, *body.Enabled, operator)
	if err != nil {
		writePolicyRuleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPolicyRuleView(rule))
}

// DeletePolicyRule (DELETE /v1/policy-rules/{id}) removes a rule. The
// decisions it previously produced stay intact on the chain — deleting a rule
// only removes the policy_rules row (SEC-54). actor is ALWAYS the verified
// caller (operatorSubject(r)), same posture as CreatePolicyRule (AUD-40,
// T-151c).
func (a *apiHandlers) DeletePolicyRule(w http.ResponseWriter, r *http.Request, id string) {
	if !requireHumanAgentRule(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.policy == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the policy decision point is not configured")
		return
	}
	if policyRuleIDNotFound(w, id) {
		return
	}
	if err := a.policy.DeleteRule(r.Context(), id, operator); err != nil {
		writePolicyRuleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
