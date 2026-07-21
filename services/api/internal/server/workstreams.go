package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/workstream"
)

// defaultWorkstreamMinInterval is the production interval floor (PERF-8, ADR-0063 §5):
// charters may not fire faster than this. Env-overridable at wiring
// (AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS — the e2e compose lowers it to prove live
// recurrence without a minute-scale gate).
const defaultWorkstreamMinInterval = 60

// workstreamStepView is one template step on the wire (detail only; operator-authored,
// operator-readable — the run-step posture).
type workstreamStepView struct {
	ResourceID string `json:"resourceId"`
	Command    string `json:"command"`
}

// workstreamPlanRequestView is one plan request on the wire (D3, ADR-0065 §5, T-091):
// the workstream detail's bounded history — open (with its TTL), submitted (its Run),
// or expired. Detail view only, like Steps below.
type workstreamPlanRequestView struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	ExpiresAt time.Time `json:"expiresAt"`
	RunID     string    `json:"runId,omitempty"`
}

func toWorkstreamPlanRequestView(pr workstream.PlanRequest) workstreamPlanRequestView {
	return workstreamPlanRequestView{ID: pr.ID, Status: pr.Status, ExpiresAt: pr.ExpiresAt, RunID: pr.RunID}
}

// workstreamView is the wire shape for a charter. The principal is the caller
// (implicit), so it is not echoed. Steps (the template) and PlanRequests ride the
// detail view only (GetWorkstream); list items carry neither.
type workstreamView struct {
	ID              string               `json:"id"`
	Label           string               `json:"label,omitempty"`
	Objective       string               `json:"objective"`
	GrantID         string               `json:"grantId"`
	IntervalSeconds int                  `json:"intervalSeconds"`
	NextFireAt      time.Time            `json:"nextFireAt"`
	Status          string               `json:"status"`
	CreatedAt       time.Time            `json:"createdAt"`
	Steps           []workstreamStepView `json:"steps,omitempty"`
	// Kind is template | adaptive (WS-5, ADR-0065 §4); existing pre-v0.9 charters are
	// template. Composer names the composing agent for an adaptive charter (R2);
	// absent (omitted) for a template charter.
	Kind     string `json:"kind"`
	Composer string `json:"composer,omitempty"`
	// PlanRequests is the charter's bounded plan-request history (D3, T-091),
	// newest first, capped at 10 — set by GetWorkstream only (an extra store call
	// toWorkstreamView itself cannot make); absent on list items and omitted for a
	// template charter (which never opens one).
	PlanRequests []workstreamPlanRequestView `json:"planRequests,omitempty"`
}

func toWorkstreamView(w workstream.Workstream, withSteps bool) workstreamView {
	v := workstreamView{
		ID: w.ID, Label: w.Label, Objective: w.Objective, GrantID: w.GrantID,
		IntervalSeconds: w.IntervalSeconds, NextFireAt: w.NextFireAt,
		Status: w.Status, CreatedAt: w.CreatedAt, Kind: w.Kind, Composer: w.Composer,
	}
	if withSteps {
		for _, st := range w.Steps {
			v.Steps = append(v.Steps, workstreamStepView{ResourceID: st.ResourceID, Command: st.Command})
		}
	}
	return v
}

// CreateWorkstream (POST /v1/workstreams) charters a Workstream (ADR-0063; WS-1/WS-2):
// human-agent + double-submit CSRF via the shared gate; the chartering operator is the
// principal. The ONE consent act: the charter row + its IMMUTABLE sealed template + the
// auto-issued delegation grant (grantee = workstream:<id>, or the named composer for an
// adaptive charter, R2) are created in one transaction, with workstream.chartered +
// grant.issued chained from it (AUD-18). The 422 matrix here is the charter-time half
// of the never-wedge property: a charter that could never fire (template outside its
// own scope/bounds, a sub-floor interval) is rejected at consent, not discovered at
// tick time. Steps is now OPTIONAL on the wire (D4, ADR-0065 §4): kind/composer select
// the charter shape, and the store's kind matrix (workstream.Store.Create) is the
// SINGLE validator for "template requires steps" / "adaptive requires a composer and
// zero steps" — this handler passes kind/composer/steps through nil-safe and maps the
// store's ErrValidation to 422, rather than duplicating the matrix here.
func (a *apiHandlers) CreateWorkstream(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.workstreams == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the workstream engine is not configured")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Label               string   `json:"label"`
		Objective           string   `json:"objective"`
		Resources           []string `json:"resources"`
		Verbs               []string `json:"verbs"`
		TTLSeconds          int      `json:"ttlSeconds"`
		IntervalSeconds     int      `json:"intervalSeconds"`
		MaxStepsPerRun      int      `json:"maxStepsPerRun"`
		MaxConcurrentRuns   int      `json:"maxConcurrentRuns"`
		StepBudget          int      `json:"stepBudget"`
		BudgetWindowSeconds int      `json:"budgetWindowSeconds"` // optional; 0 → defaults to the interval
		Kind                string   `json:"kind"`                // "" | "template" | "adaptive" (D4); "" → the store defaults to template
		Composer            string   `json:"composer"`            // required for kind=adaptive; must be absent/empty for template
		Steps               []struct {
			ResourceID string `json:"resourceId"`
			Command    string `json:"command"`
		} `json:"steps"` // OPTIONAL (D4): required only for a template charter — the store enforces this
	}
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.Objective == "" || len(body.Resources) == 0 || len(body.Verbs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "a workstream requires an objective and a resource+verb scope")
		return
	}
	for _, st := range body.Steps {
		if st.ResourceID == "" || st.Command == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "each template step requires resourceId and command")
			return
		}
	}

	// The SHARED issuance-bounds core (SEC-30b) — the same validator CreateGrant runs, so
	// the in-process issuance path cannot drift from the HTTP one.
	if msg := validateGrantIssuanceBounds(body.Resources, body.Verbs, body.TTLSeconds,
		body.MaxStepsPerRun, body.MaxConcurrentRuns, body.StepBudget); msg != "" {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", msg)
		return
	}
	// Charter-time 422 matrix (WS-2; the F2 no-wedged-by-construction check + PERF-8 floor
	// + template ⊆ scope — the SEC-29 mirror at consent time).
	if body.IntervalSeconds < a.workstreamMinInterval {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "intervalSeconds is below the schedule floor")
		return
	}
	if len(body.Steps) > body.MaxStepsPerRun || len(body.Steps) > body.StepBudget {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "the template exceeds the charter's own step bounds (it could never fire)")
		return
	}
	if body.BudgetWindowSeconds < 0 {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "budgetWindowSeconds must not be negative")
		return
	}
	for _, st := range body.Steps {
		if !slices.Contains(body.Resources, st.ResourceID) {
			writeError(w, http.StatusUnprocessableEntity, "unprocessable", "a template step targets a resource outside the charter scope")
			return
		}
	}

	// The windowed budget defaults to the interval ("steps per period", WS-4).
	window := body.BudgetWindowSeconds
	if window == 0 {
		window = body.IntervalSeconds
	}
	in := workstream.Input{
		Principal: operator, Label: body.Label, Objective: body.Objective,
		IntervalSeconds: body.IntervalSeconds,
		Kind:            body.Kind,
		Composer:        body.Composer,
		Grant: grant.Input{
			// Principal + Agent are bound by the store (Agent = workstream:<id>, minted
			// there) — never caller-supplied.
			Label:     "workstream: " + firstNonEmptyStr(body.Label, body.Objective),
			Resources: body.Resources, Verbs: body.Verbs,
			ExpiresAt:           time.Now().Add(time.Duration(body.TTLSeconds) * time.Second),
			MaxStepsPerRun:      body.MaxStepsPerRun,
			MaxConcurrentRuns:   body.MaxConcurrentRuns,
			StepBudget:          body.StepBudget,
			BudgetWindowSeconds: window,
		},
	}
	for _, st := range body.Steps {
		in.Steps = append(in.Steps, workstream.StepInput{ResourceID: st.ResourceID, Command: st.Command})
	}
	created, err := a.workstreams.Create(r.Context(), in)
	if err != nil {
		if errors.Is(err, workstream.ErrValidation) {
			writeError(w, http.StatusUnprocessableEntity, "unprocessable", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "charter failed")
		return
	}
	writeJSON(w, http.StatusCreated, toWorkstreamView(created, true))
}

type workstreamPage struct {
	Items []workstreamView `json:"items"`
	Page  pageMeta         `json:"page"`
}

// ListWorkstreams (GET /v1/workstreams) returns a page of the caller's charters
// (principal-scoped; no templates — the runs-list pattern).
func (a *apiHandlers) ListWorkstreams(w http.ResponseWriter, r *http.Request, params ListWorkstreamsParams) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.workstreams == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the workstream engine is not configured")
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
	items, last, err := a.workstreams.ListForOperator(r.Context(), operator, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list workstreams failed")
		return
	}
	page := workstreamPage{Items: make([]workstreamView, 0, len(items))}
	for _, ws := range items {
		page.Items = append(page.Items, toWorkstreamView(ws, false))
	}
	if len(items) == limit {
		page.Page.NextCursor = last
	}
	writeJSON(w, http.StatusOK, page)
}

// planRequestHistoryLimit bounds GetWorkstream's plan-request history (D3, ADR-0065
// §5, T-091): most recent 10, newest first — the charter detail's bounded surface.
const planRequestHistoryLimit = 10

// GetWorkstream (GET /v1/workstreams/{id}) returns a charter with its template and its
// bounded plan-request history (D3). Owner-scoped: a cross-operator id answers 404
// (existence-hiding, the grants pattern).
func (a *apiHandlers) GetWorkstream(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.workstreams == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the workstream engine is not configured")
		return
	}
	ws, err := a.workstreams.Get(r.Context(), id)
	if errors.Is(err, workstream.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "workstream not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get workstream failed")
		return
	}
	if ws.Principal != operator {
		writeError(w, http.StatusNotFound, "not_found", "workstream not found") // owner-scoped existence-hiding
		return
	}
	v := toWorkstreamView(ws, true)
	prs, err := a.workstreams.ListPlanRequestsForWorkstream(r.Context(), id, planRequestHistoryLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get workstream failed")
		return
	}
	for _, pr := range prs {
		v.PlanRequests = append(v.PlanRequests, toWorkstreamPlanRequestView(pr))
	}
	writeJSON(w, http.StatusOK, v)
}

// ownedWorkstream resolves an id to a charter the caller owns, writing the error
// response (404 unknown/foreign — existence-hiding — or 500/503) when it returns false.
func (a *apiHandlers) ownedWorkstream(w http.ResponseWriter, r *http.Request, id string) (workstream.Workstream, bool) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return workstream.Workstream{}, false
	}
	if a.workstreams == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the workstream engine is not configured")
		return workstream.Workstream{}, false
	}
	ws, err := a.workstreams.Get(r.Context(), id)
	if errors.Is(err, workstream.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "workstream not found")
		return workstream.Workstream{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get workstream failed")
		return workstream.Workstream{}, false
	}
	if ws.Principal != operator {
		writeError(w, http.StatusNotFound, "not_found", "workstream not found") // owner-scoped existence-hiding
		return workstream.Workstream{}, false
	}
	return ws, true
}

// PauseWorkstream (POST /v1/workstreams/{id}/pause) soft-stops an active charter
// (WS-5): ticks stop; in-flight Runs complete; the grant stays active. 409 when not
// active (already paused, or retired).
func (a *apiHandlers) PauseWorkstream(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := a.ownedWorkstream(w, r, id); !ok {
		return
	}
	paused, err := a.workstreams.Pause(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "pause workstream failed")
		return
	}
	if !paused {
		writeError(w, http.StatusConflict, "conflict", "workstream is not active")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": workstream.StatusPaused})
}

// ResumeWorkstream (POST /v1/workstreams/{id}/resume) re-arms a paused charter: the
// next fire is now + interval — no catch-up fire for the paused stretch (WS-5). 409
// when not paused.
func (a *apiHandlers) ResumeWorkstream(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := a.ownedWorkstream(w, r, id); !ok {
		return
	}
	resumed, err := a.workstreams.Resume(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "resume workstream failed")
		return
	}
	if !resumed {
		writeError(w, http.StatusConflict, "conflict", "workstream is not paused")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": workstream.StatusActive})
}

// RetireWorkstream (POST /v1/workstreams/{id}/retire) terminally ends a charter (WS-5,
// the kill switch; from active OR paused): one tx flips the charter + revokes its grant,
// then the in-flight cascade runs best-effort (the RevokeGrant mirror). The documented
// v0.6 residual carries: a crash between the tx and the cascade leaves bounded,
// self-completing Runs (no security impact) — as does the tiny launch-vs-retire race
// (a tick's Run created after the cascade sweep completes on its own; the next tick
// self-retires nothing further — the charter is already retired). 409 when already
// retired.
func (a *apiHandlers) RetireWorkstream(w http.ResponseWriter, r *http.Request, id string) {
	ws, ok := a.ownedWorkstream(w, r, id)
	if !ok {
		return
	}
	retired, err := a.workstreams.Retire(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "retire workstream failed")
		return
	}
	if !retired {
		writeError(w, http.StatusConflict, "conflict", "workstream is already retired")
		return
	}
	// DUR-7 extension: the retire cascades — cancel the charter grant's in-flight Runs so
	// a retired charter stops running work too, not just future ticks. Best-effort (the
	// retire already committed); the sequencer's between-step RunStatus check halts each.
	if a.runs != nil {
		if n, cerr := a.runs.CancelRunsForGrant(r.Context(), ws.GrantID); cerr != nil {
			log.Printf("retire workstream %s: cancel in-flight runs: %v", id, cerr)
		} else if n > 0 {
			log.Printf("retire workstream %s: canceled %d in-flight run(s)", id, n)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": workstream.StatusRetired})
}

// firstNonEmptyStr returns the first non-empty string (the grant label falls back to the
// objective when the charter has no label).
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
