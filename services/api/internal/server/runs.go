package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"aireos.local/services/api/internal/run"
)

// maxStepsPerRun caps the operator-direct plan size (PERF-6, ADR-0061). The per-operator
// active-Run ceiling moved to run.MaxActiveRunsPerOperator at T-072 — the engine owns it,
// since BOTH launch paths (this operator-direct one and the shared bounded-launch core)
// score against it.
const maxStepsPerRun = 50

// runStepView / runView are the wire shapes for a Run's status. The operator-authored
// command is NOT echoed back (the operator submitted it; this view is for watching
// progress). The full plan + commands live in the operator-only store.
type runStepView struct {
	StepIndex    int    `json:"stepIndex"`
	ResourceID   string `json:"resourceId"`
	Status       string `json:"status"`
	Outcome      string `json:"outcome,omitempty"`
	InvocationID string `json:"invocationId,omitempty"` // the chain anchor (AUD-15); absent until dispatched
}

type runView struct {
	ID      string        `json:"id"`
	Status  string        `json:"status"`
	Actor   string        `json:"actor,omitempty"`   // the agent that drove a delegated Run (FE-19, ADR-0062); absent for operator-direct
	GrantID string        `json:"grantId,omitempty"` // the bounding delegation grant (FE-19); absent for operator-direct
	Steps   []runStepView `json:"steps"`
}

func toRunView(r run.Run) runView {
	v := runView{ID: r.ID, Status: r.Status, Actor: r.Actor, GrantID: r.GrantID, Steps: make([]runStepView, 0, len(r.Steps))}
	for _, s := range r.Steps {
		v.Steps = append(v.Steps, runStepView{
			StepIndex: s.Index, ResourceID: s.ResourceID, Status: s.Status,
			Outcome: s.Outcome, InvocationID: s.InvocationID,
		})
	}
	return v
}

// CreateRun (POST /v1/runs) launches an operator-authored governed Run (ADR-0061):
// SEC-23 human-agent + double-submit CSRF via the shared gate; GOV-1 informed launch
// (the full plan is the authorized payload); PERF-6 bounded (plan size + the
// operator's concurrent active Runs). The Run is created 'pending' and run.launched is
// audited in the SAME transaction as the insert (in the store, AUD-9); the sequencer
// drives it. Launch is not idempotent — a double-submit creates two Runs.
func (a *apiHandlers) CreateRun(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the run engine is not configured")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Steps []struct {
			ResourceID string `json:"resourceId"`
			Command    string `json:"command"`
		} `json:"steps"`
	}
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if len(body.Steps) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "a run requires at least one step")
		return
	}
	if len(body.Steps) > maxStepsPerRun {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "the plan exceeds the maximum step count")
		return
	}
	in := run.Input{Operator: operator}
	for _, s := range body.Steps {
		if s.ResourceID == "" || s.Command == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "each step requires resourceId and command")
			return
		}
		in.Steps = append(in.Steps, run.StepInput{ResourceID: s.ResourceID, Command: s.Command})
	}

	active, err := a.runs.CountActiveForOperator(r.Context(), operator)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	if active >= run.MaxActiveRunsPerOperator {
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "too many active runs for this operator")
		return
	}

	created, err := a.runs.Create(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "launch failed")
		return
	}
	writeJSON(w, http.StatusCreated, toRunView(created))
}

type runPage struct {
	Items []runView `json:"items"`
	Page  pageMeta  `json:"page"`
}

// ListRuns (GET /v1/runs) returns a page of the caller's Runs (operator-scoped; list
// items omit per-step detail).
func (a *apiHandlers) ListRuns(w http.ResponseWriter, r *http.Request, params ListRunsParams) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the run engine is not configured")
		return
	}
	limit := 50
	if params.Limit != nil {
		n := int(*params.Limit)
		if n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer in [1,200]")
			return
		}
		limit = n
	}
	var after string
	if params.Cursor != nil {
		after = string(*params.Cursor)
	}
	runs, last, err := a.runs.ListForOperator(r.Context(), operator, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list runs failed")
		return
	}
	page := runPage{Items: make([]runView, 0, len(runs))}
	for _, rn := range runs {
		page.Items = append(page.Items, toRunView(rn))
	}
	if len(runs) == limit {
		page.Page.NextCursor = last
	}
	writeJSON(w, http.StatusOK, page)
}

// CancelRun (POST /v1/runs/{id}/cancel) cancels a pending/running Run the operator owns
// (DUR-5; stop-future-steps + run.canceled). An in-flight (e.g. parked) step is not
// force-released — the operator denies it via /invocations. 404 for an unknown/
// cross-operator id; 409 if the Run is no longer cancelable (already terminal).
func (a *apiHandlers) CancelRun(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the run engine is not configured")
		return
	}
	rn, err := a.runs.Get(r.Context(), id)
	if errors.Is(err, run.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "cancel run failed")
		return
	}
	if rn.Operator != operator {
		writeError(w, http.StatusNotFound, "not_found", "run not found") // owner-scoped existence-hiding
		return
	}
	canceled, err := a.runs.CancelRun(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "cancel run failed")
		return
	}
	if !canceled {
		writeError(w, http.StatusConflict, "conflict", "run is not cancelable (already terminal)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": run.StatusCanceled})
}

// GetRun (GET /v1/runs/{id}) returns a Run's status + per-step status. Owner-scoped:
// a cross-operator id answers 404 (existence-hiding, the session pattern).
func (a *apiHandlers) GetRun(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.runs == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the run engine is not configured")
		return
	}
	rn, err := a.runs.Get(r.Context(), id)
	if errors.Is(err, run.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get run failed")
		return
	}
	if rn.Operator != operator {
		writeError(w, http.StatusNotFound, "not_found", "run not found") // owner-scoped existence-hiding
		return
	}
	writeJSON(w, http.StatusOK, toRunView(rn))
}
