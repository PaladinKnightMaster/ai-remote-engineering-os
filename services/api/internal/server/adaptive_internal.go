package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/run"
	"aireos.local/services/api/internal/workstream"
)

// onlyGatewaySubject is the shared subject pin every gateway-forwarded internal route
// enforces on TOP of the route-scoped service-service verifier (mirrors
// LaunchDelegatedRun's onlyGateway check, delegated.go): the verifier only proves "a
// valid service-service token," this proves "the gateway's own token, not some other
// service-service caller" (SEC-27). Returns false (and already wrote the response)
// when the pin fails.
func onlyGatewaySubject(w http.ResponseWriter, r *http.Request) bool {
	capb, ok := mw.CapabilityFromContext(r.Context())
	if !ok || capb.Subject != "mcp-gateway" {
		writeError(w, http.StatusForbidden, "forbidden", "this plane is gateway-only")
		return false
	}
	return true
}

// --- workstream.poll (D1, ADR-0065 §5, T-090) --------------------------------------

// pollWorkstreamRequest is the gateway's workstream.poll forward body (mirrors
// mcp.Client.Poll's wire shape, services/mcp-gateway/internal/apiforward/client.go).
type pollWorkstreamRequest struct {
	AgentSubject string `json:"agentSubject"`
	WorkstreamID string `json:"workstreamId"`
}

// planRequestCompositionView is the wire shape of one open plan request (mirrors
// mcp.PlanRequestView field-for-field — the gateway forward decodes this verbatim).
type planRequestCompositionView struct {
	RequestID      string   `json:"requestId"`
	WorkstreamID   string   `json:"workstreamId"`
	Objective      string   `json:"objective"`
	Resources      []string `json:"resources"`
	Verbs          []string `json:"verbs"`
	MaxStepsPerRun int      `json:"maxStepsPerRun"`
	ExpiresAt      string   `json:"expiresAt"`
}

type pollWorkstreamResponse struct {
	Requests []planRequestCompositionView `json:"requests"`
}

// PollWorkstream (POST /v1/internal/plan-polls) is api's inbound forward target for the
// gateway's workstream.poll tool (D1, ADR-0065 §5): the caller's own open plan
// requests, derived ENTIRELY from its LIVE grants (SEC-32 containment BY
// CONSTRUCTION) — never a caller-supplied scope beyond the optional narrowing filter.
// Gateway-only (onlyGatewaySubject), the mirror of LaunchDelegatedRun's pin. Empty is a
// normal 200, never an error (no oracle, N1).
func (a *apiHandlers) PollWorkstream(w http.ResponseWriter, r *http.Request) {
	if !onlyGatewaySubject(w, r) {
		return
	}
	if a.workstreams == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the workstream engine is not configured")
		return
	}
	var body pollWorkstreamRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.AgentSubject == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "agentSubject is required")
		return
	}
	reqs, err := a.workstreams.PollOpenPlanRequests(r.Context(), body.AgentSubject, body.WorkstreamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "poll plan requests failed")
		return
	}
	views := make([]planRequestCompositionView, 0, len(reqs))
	for _, c := range reqs {
		views = append(views, planRequestCompositionView{
			RequestID: c.RequestID, WorkstreamID: c.WorkstreamID, Objective: c.Objective,
			Resources: c.Resources, Verbs: c.Verbs, MaxStepsPerRun: c.MaxStepsPerRun,
			ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, pollWorkstreamResponse{Requests: views})
}

// --- plan.submit (D3, ADR-0065 §6, T-090) -------------------------------------------

type submitPlanReqStep struct {
	ResourceID string `json:"resourceId"`
	Command    string `json:"command"`
}

// submitPlanRequest is the gateway's plan.submit forward body (mirrors
// mcp.Client.Submit's wire shape).
type submitPlanRequest struct {
	AgentSubject string              `json:"agentSubject"`
	RequestID    string              `json:"requestId"`
	Steps        []submitPlanReqStep `json:"steps"`
}

type submitPlanResponse struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
}

// submitPlanRefusalReason is the ONE refusal string plan.submit ever answers with for
// both an unknown request AND a request that exists but belongs to a different agent's
// charter (§6's impersonation-pin no-oracle: "a foreign agent cannot answer another
// charter's request" reads identically to "no such request exists"). It is, verbatim,
// the SAME reason validatePlanAgainstGrant (internal/run/launch.go) answers when
// g.Agent != agentSubject — reused rather than duplicated so the two paths that can
// reach it (the pre-check below, and LaunchBoundedProposed's own grant check when a
// wrong agent answers an EXISTING open request) are byte-identical by construction, not
// by convention.
const submitPlanRefusalReason = "the grant does not authorize this agent"

// SubmitPlan (POST /v1/internal/plan-submissions) is api's inbound forward target for
// the gateway's plan.submit tool (D3, ADR-0065 §6): validates the request is open and
// forwards through the SAME shared bounded-launch core every other launch path uses
// (LaunchBoundedProposed, SEC-29/SEC-30a — F-c's one validator), which ALSO enforces the
// impersonation pin (the composer's grant must name this agent) — so an unknown request
// and a foreign agent's request answer the IDENTICAL 403 (no oracle). On success, marks
// the request submitted and chains plan.submitted in the SAME transaction as that flip
// (MarkPlanRequestSubmitted). Gateway-only (onlyGatewaySubject).
func (a *apiHandlers) SubmitPlan(w http.ResponseWriter, r *http.Request) {
	if !onlyGatewaySubject(w, r) {
		return
	}
	if a.workstreams == nil || a.grants == nil || a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the plan-submission plane is not configured")
		return
	}
	var body submitPlanRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.AgentSubject == "" || body.RequestID == "" || len(body.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "agentSubject, requestId and at least one step are required")
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

	pr, err := a.workstreams.GetPlanRequest(r.Context(), body.RequestID)
	if err != nil && !errors.Is(err, workstream.ErrPlanRequestNotFound) {
		writeError(w, http.StatusInternalServerError, "internal", "load plan request failed")
		return
	}
	if errors.Is(err, workstream.ErrPlanRequestNotFound) || pr.Status != workstream.PlanRequestOpen {
		// Unknown / already-terminal request: the SAME refusal shape as a foreign
		// agent's request below — no oracle (§6).
		writeError(w, http.StatusForbidden, "forbidden", submitPlanRefusalReason)
		return
	}
	ws, err := a.workstreams.Get(r.Context(), pr.WorkstreamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "load charter failed")
		return
	}

	launched, _, refusal, err := a.runs.LaunchBoundedProposed(
		r.Context(), a.grants, body.AgentSubject, ws.GrantID, pr.LaunchKey, steps)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "submit failed")
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

	// Mark the request submitted regardless of reattach — the flip-guard makes a
	// re-mark (after a crash between run-create and flip, T-089/D3's two-tx design) a
	// safe no-op with no duplicate plan.submitted.
	if _, err := a.workstreams.MarkPlanRequestSubmitted(r.Context(), pr.ID, launched.ID, body.AgentSubject); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "mark plan request submitted failed")
		return
	}
	writeJSON(w, http.StatusOK, submitPlanResponse{RunID: launched.ID, Status: launched.Status})
}

// --- memory.read (D2, ADR-0065 §3, T-090) -------------------------------------------

// readMemoryRequest is the gateway's memory.read forward body (mirrors
// mcp.Client.Read's wire shape).
type readMemoryRequest struct {
	AgentSubject string `json:"agentSubject"`
	WorkstreamID string `json:"workstreamId"`
}

// memoryRowView is the wire shape of one agent-plane memory row (mirrors
// mcp.MemoryRowView field-for-field). Distinct from memories.go's memoryView (the
// operator-plane MemoryView, ADR-0064 §7) — the agent plane carries a different,
// narrower field set with no redaction/pin metadata (SEC-32).
type memoryRowView struct {
	ID          string  `json:"id"`
	ScopeType   string  `json:"scopeType"`
	ScopeID     string  `json:"scopeId"`
	Key         string  `json:"key,omitempty"`
	Ref         string  `json:"ref,omitempty"`
	Content     string  `json:"content,omitempty"`
	SourceTrace string  `json:"sourceTrace,omitempty"`
	Confidence  float32 `json:"confidence"`
	CreatedAt   string  `json:"createdAt"`
}

type readMemoryResponse struct {
	Items []memoryRowView `json:"items"`
}

// ReadMemory (POST /v1/internal/memory-reads) is api's inbound forward target for the
// gateway's memory.read tool — the ONE lit agent-memory path (D2, MEM-5, SEC-32,
// ADR-0065 §3): the readable set is derived ENTIRELY server-side by
// memory.Store.ReadForAgent from the caller's own live grants; this handler is a thin
// adapter, like LaunchDelegatedRun. Gateway-only (onlyGatewaySubject). A revoked grant
// or an out-of-scope filter answers an empty 200, never an error (no oracle, N1/N3).
func (a *apiHandlers) ReadMemory(w http.ResponseWriter, r *http.Request) {
	if !onlyGatewaySubject(w, r) {
		return
	}
	if a.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the memory store is not configured")
		return
	}
	var body readMemoryRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.AgentSubject == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "agentSubject is required")
		return
	}
	items, err := a.memories.ReadForAgent(r.Context(), body.AgentSubject, body.WorkstreamID, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "agent memory read failed")
		return
	}
	views := make([]memoryRowView, 0, len(items))
	for _, m := range items {
		views = append(views, memoryRowView{
			ID: m.ID, ScopeType: m.ScopeType, ScopeID: m.ScopeID, Key: m.Key, Ref: m.Ref,
			Content: m.Content, SourceTrace: m.SourceTrace, Confidence: m.Confidence,
			CreatedAt: m.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, readMemoryResponse{Items: views})
}
