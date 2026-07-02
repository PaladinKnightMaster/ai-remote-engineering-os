package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/run"
)

// remoteExecVerb is the operation every Run step performs (a remote.exec); plan-vs-grant
// checks it is within the grant's verbs (ADR-0062, SEC-29).
const remoteExecVerb = "exec"

// delegatedRunRequest is the gateway-forwarded launch (ADR-0062 §3): the agent the gateway
// verified, the named grant, the agent's idempotency key, and the ordered plan. T-065b's
// run.launch tool populates it; the gateway is the sole caller (onlyGateway).
type delegatedRunRequest struct {
	AgentSubject   string                `json:"agentSubject"`
	GrantID        string                `json:"grantId"`
	IdempotencyKey string                `json:"idempotencyKey"`
	Steps          []delegatedRunReqStep `json:"steps"`
}

type delegatedRunReqStep struct {
	ResourceID string `json:"resourceId"`
	Command    string `json:"command"`
}

type delegatedRunResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

// LaunchDelegatedRun (POST /v1/internal/delegated-runs) is api's inbound forward target for
// an agent-initiated Run (ADR-0062 §3). The gateway — the keyless agent-facing surface — is
// the ONLY caller: pinned to subject "mcp-gateway" (onlyGateway) on top of the service-service
// edge, so the symmetric-key-holding api is not agent-reachable (SEC-27). api validates the
// plan against the named grant (SEC-29, the relocated SEC-16), then creates a delegated Run
// (operator = the grant's principal, actor = the agent, grant_id = the grant) — the sequencer
// drives it. The agent mints nothing; api is the sole authority. A retried launch (same
// idempotency key) re-attaches to the original Run (no double-launch, no double-budget).
func (a *apiHandlers) LaunchDelegatedRun(w http.ResponseWriter, r *http.Request) {
	capb, ok := mw.CapabilityFromContext(r.Context())
	if !ok || capb.Subject != "mcp-gateway" {
		writeError(w, http.StatusForbidden, "forbidden", "the delegated-launch plane is gateway-only")
		return
	}
	if a.grants == nil || a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the delegation engine is not configured")
		return
	}
	var body delegatedRunRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.AgentSubject == "" || body.GrantID == "" || len(body.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "agentSubject, grantId and at least one step are required")
		return
	}

	g, err := a.grants.Get(r.Context(), body.GrantID)
	if errors.Is(err, grant.ErrNotFound) {
		a.denyLaunch(r, body.AgentSubject, body.GrantID)
		writeError(w, http.StatusForbidden, "forbidden", "no such grant for this agent")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}

	// Plan-vs-grant (SEC-29): the gateway-vouched agent must match the grant; the grant must
	// be live; every step must target a granted resource with a granted verb; the plan must
	// fit the per-run step bound. Budget + concurrency are T-066.
	resourceIDs := make([]string, 0, len(body.Steps))
	for _, s := range body.Steps {
		if s.ResourceID == "" || s.Command == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "each step requires resourceId and command")
			return
		}
		resourceIDs = append(resourceIDs, s.ResourceID)
	}
	if reason := validatePlanAgainstGrant(g, body.AgentSubject, resourceIDs); reason != "" {
		a.denyLaunch(r, body.AgentSubject, body.GrantID)
		writeError(w, http.StatusForbidden, "forbidden", reason)
		return
	}

	// PERF-7: per-grant concurrency + step-budget bounds (beyond the per-plan scope above).
	// Non-atomic soft ceilings — the concurrency cap itself bounds concurrent launches, and the
	// budget counts total planned steps across the grant's Runs (the ratified semantics).
	active, err := a.runs.CountActiveForGrant(r.Context(), g.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	if active >= g.MaxConcurrentRuns {
		a.denyLaunch(r, body.AgentSubject, body.GrantID)
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "the grant's concurrent-run limit is reached")
		return
	}
	// PERF-7 cap layering (criteria): a delegated Run's operator = the principal, so it also
	// counts against the principal's global per-operator active-run ceiling — the safety
	// ceiling above the per-grant cap. (Grants should set max_concurrent_runs well below this
	// so agent activity does not starve the operator's own launches.)
	opActive, err := a.runs.CountActiveForOperator(r.Context(), g.Principal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	if opActive >= maxActiveRunsPerOperator {
		a.denyLaunch(r, body.AgentSubject, body.GrantID)
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "too many active runs for this operator")
		return
	}
	consumed, err := a.runs.CountStepsForGrant(r.Context(), g.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	if consumed+len(body.Steps) > g.StepBudget {
		a.denyLaunch(r, body.AgentSubject, body.GrantID)
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "the grant's step budget is exhausted")
		return
	}

	in := run.Input{Operator: g.Principal, Actor: body.AgentSubject, GrantID: g.ID, LaunchKey: body.IdempotencyKey}
	for _, s := range body.Steps {
		in.Steps = append(in.Steps, run.StepInput{ResourceID: s.ResourceID, Command: s.Command})
	}
	created, err := a.runs.Create(r.Context(), in)
	if errors.Is(err, run.ErrDuplicateLaunch) {
		// Agent retry: re-attach to the original Run (idempotent), no second launch.
		existing, gerr := a.runs.GetByLaunchKey(r.Context(), g.ID, body.IdempotencyKey)
		if gerr != nil {
			writeError(w, http.StatusInternalServerError, "internal", "launch failed")
			return
		}
		writeJSON(w, http.StatusOK, delegatedRunResponse{RunID: existing.ID, Status: existing.Status})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	writeJSON(w, http.StatusCreated, delegatedRunResponse{RunID: created.ID, Status: created.Status})
}

// validatePlanAgainstGrant returns "" when the plan is within the grant, else a denial
// reason (SEC-29). Pure — unit-tested without HTTP/PG.
func validatePlanAgainstGrant(g grant.Grant, agentSubject string, resourceIDs []string) string {
	switch {
	case g.Agent != agentSubject:
		return "the grant does not authorize this agent"
	case g.Status != grant.StatusActive:
		return "the grant is not active"
	case !time.Now().Before(g.ExpiresAt):
		return "the grant has expired"
	case !slices.Contains(g.Verbs, remoteExecVerb):
		return "the grant does not authorize remote execution"
	case len(resourceIDs) > g.MaxStepsPerRun:
		return "the plan exceeds the grant's maximum steps per run"
	}
	for _, rid := range resourceIDs {
		if !slices.Contains(g.Resources, rid) {
			return "a step targets a resource outside the grant"
		}
	}
	return ""
}

// denyLaunch audits a plan-vs-grant rejection (N1), best-effort — a denied launch creates
// no Run and drives no egress regardless.
func (a *apiHandlers) denyLaunch(r *http.Request, agent, grantID string) {
	if err := a.grants.AuditLaunchDenied(r.Context(), agent, grantID); err != nil {
		log.Printf("audit run.launch.denied (grant %s): %v", grantID, err)
	}
}
