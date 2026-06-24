package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"aireos.local/services/api/internal/run"
)

// Run launch bounds (PERF-6, ADR-0061): cap the plan size and an operator's
// concurrent active Runs so a pathological or runaway launch cannot flood the
// governed plane.
const (
	maxStepsPerRun           = 50
	maxActiveRunsPerOperator = 10
)

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
	ID     string        `json:"id"`
	Status string        `json:"status"`
	Steps  []runStepView `json:"steps"`
}

func toRunView(r run.Run) runView {
	v := runView{ID: r.ID, Status: r.Status, Steps: make([]runStepView, 0, len(r.Steps))}
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
	if active >= maxActiveRunsPerOperator {
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
