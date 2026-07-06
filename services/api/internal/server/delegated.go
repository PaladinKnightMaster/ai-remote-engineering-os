package server

import (
	"encoding/json"
	"net/http"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/run"
)

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
// edge, so the symmetric-key-holding api is not agent-reachable (SEC-27).
//
// Since T-072 the handler is a thin HTTP adapter: plan-vs-grant (SEC-29), the PERF-7
// bounds, the deny-audit, and the idempotent create/re-attach all live in the SHARED
// run.LaunchBounded core (SEC-30a, ADR-0063 §4) — the same function the T-073 workstream
// ticker calls. The handler only decodes, calls the core, and maps the typed Refusal to
// the unchanged wire statuses (out-of-grant/dead → 403, bounds → 429, re-attach → 200).
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
	steps := make([]run.StepInput, 0, len(body.Steps))
	for _, s := range body.Steps {
		if s.ResourceID == "" || s.Command == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "each step requires resourceId and command")
			return
		}
		steps = append(steps, run.StepInput{ResourceID: s.ResourceID, Command: s.Command})
	}

	launched, reattached, refusal, err := a.runs.LaunchBounded(
		r.Context(), a.grants, body.AgentSubject, body.GrantID, body.IdempotencyKey, steps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	if refusal != nil {
		if refusal.Kind == run.RefusalBounds {
			writeError(w, http.StatusTooManyRequests, "too_many_requests", refusal.Reason)
			return
		}
		writeError(w, http.StatusForbidden, "forbidden", refusal.Reason) // out-of-grant + grant-dead
		return
	}
	if reattached {
		// Agent retry: re-attached to the original Run (idempotent), no second launch.
		writeJSON(w, http.StatusOK, delegatedRunResponse{RunID: launched.ID, Status: launched.Status})
		return
	}
	writeJSON(w, http.StatusCreated, delegatedRunResponse{RunID: launched.ID, Status: launched.Status})
}
