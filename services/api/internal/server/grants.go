package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"aireos.local/services/api/internal/grant"
)

// Grant issuance ceilings (ADR-0062 / SEC-29): the api-layer caps on the grant scope
// size; the operator-chosen per-grant bounds live within these.
const (
	maxGrantResources = 100
	maxGrantVerbs     = 20
)

// grantView is the wire shape for a grant. The principal is the caller (implicit), so it
// is not echoed.
type grantView struct {
	ID                string    `json:"id"`
	Agent             string    `json:"agent"`
	Label             string    `json:"label,omitempty"`
	Resources         []string  `json:"resources"`
	Verbs             []string  `json:"verbs"`
	ExpiresAt         time.Time `json:"expiresAt"`
	MaxStepsPerRun    int       `json:"maxStepsPerRun"`
	MaxConcurrentRuns int       `json:"maxConcurrentRuns"`
	StepBudget        int       `json:"stepBudget"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
}

func toGrantView(g grant.Grant) grantView {
	return grantView{
		ID: g.ID, Agent: g.Agent, Label: g.Label, Resources: g.Resources, Verbs: g.Verbs,
		ExpiresAt: g.ExpiresAt, MaxStepsPerRun: g.MaxStepsPerRun, MaxConcurrentRuns: g.MaxConcurrentRuns,
		StepBudget: g.StepBudget, Status: g.Status, CreatedAt: g.CreatedAt,
	}
}

// CreateGrant (POST /v1/grants) issues an operator-authored delegation grant (ADR-0062):
// SEC-26 human-agent + double-submit CSRF via the shared gate; the issuing operator is the
// principal. The grant is created 'active' and grant.issued is audited in the SAME
// transaction as the insert (AUD-16). Issuance is not idempotent (a double-submit issues
// two grants). The agent's launch authority is the grant, validated at launch (T-065) — the
// agent mints nothing.
func (a *apiHandlers) CreateGrant(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.grants == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the grant engine is not configured")
		return
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body struct {
		Agent             string   `json:"agent"`
		Label             string   `json:"label"`
		Resources         []string `json:"resources"`
		Verbs             []string `json:"verbs"`
		TTLSeconds        int      `json:"ttlSeconds"`
		MaxStepsPerRun    int      `json:"maxStepsPerRun"`
		MaxConcurrentRuns int      `json:"maxConcurrentRuns"`
		StepBudget        int      `json:"stepBudget"`
	}
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.Agent == "" || len(body.Resources) == 0 || len(body.Verbs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "a grant requires an agent, at least one resource, and at least one verb")
		return
	}
	if len(body.Resources) > maxGrantResources || len(body.Verbs) > maxGrantVerbs {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "the grant scope exceeds the maximum size")
		return
	}
	if body.TTLSeconds <= 0 || body.MaxStepsPerRun <= 0 || body.MaxConcurrentRuns <= 0 || body.StepBudget <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "ttlSeconds and all bounds must be positive")
		return
	}
	created, err := a.grants.Create(r.Context(), grant.Input{
		Principal: operator, Agent: body.Agent, Label: body.Label,
		Resources: body.Resources, Verbs: body.Verbs,
		ExpiresAt:         time.Now().Add(time.Duration(body.TTLSeconds) * time.Second),
		MaxStepsPerRun:    body.MaxStepsPerRun,
		MaxConcurrentRuns: body.MaxConcurrentRuns,
		StepBudget:        body.StepBudget,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "issue failed")
		return
	}
	writeJSON(w, http.StatusCreated, toGrantView(created))
}

type grantPage struct {
	Items []grantView `json:"items"`
	Page  pageMeta    `json:"page"`
}

// ListGrants (GET /v1/grants) returns a page of the caller's grants (principal-scoped).
func (a *apiHandlers) ListGrants(w http.ResponseWriter, r *http.Request, params ListGrantsParams) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.grants == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the grant engine is not configured")
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
	grants, last, err := a.grants.ListForOperator(r.Context(), operator, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list grants failed")
		return
	}
	page := grantPage{Items: make([]grantView, 0, len(grants))}
	for _, g := range grants {
		page.Items = append(page.Items, toGrantView(g))
	}
	if len(grants) == limit {
		page.Page.NextCursor = last
	}
	writeJSON(w, http.StatusOK, page)
}

// GetGrant (GET /v1/grants/{id}) returns a grant. Owner-scoped: a cross-operator id answers
// 404 (existence-hiding, the runs/sessions pattern).
func (a *apiHandlers) GetGrant(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.grants == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the grant engine is not configured")
		return
	}
	g, err := a.grants.Get(r.Context(), id)
	if errors.Is(err, grant.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "grant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get grant failed")
		return
	}
	if g.Principal != operator {
		writeError(w, http.StatusNotFound, "not_found", "grant not found") // owner-scoped existence-hiding
		return
	}
	writeJSON(w, http.StatusOK, toGrantView(g))
}

// RevokeGrant (POST /v1/grants/{id}/revoke) revokes an active grant the operator owns
// (ADR-0062 §7): status active→revoked + grant.revoked. The cancel-of-in-flight-runs CASCADE
// (DUR-7) is T-066 — T-063 revoke stops only NEW launches. 404 for an unknown/cross-operator
// id; 409 if the grant is already revoked.
func (a *apiHandlers) RevokeGrant(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.grants == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the grant engine is not configured")
		return
	}
	g, err := a.grants.Get(r.Context(), id)
	if errors.Is(err, grant.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "grant not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "revoke grant failed")
		return
	}
	if g.Principal != operator {
		writeError(w, http.StatusNotFound, "not_found", "grant not found") // owner-scoped existence-hiding
		return
	}
	revoked, err := a.grants.Revoke(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "revoke grant failed")
		return
	}
	if !revoked {
		writeError(w, http.StatusConflict, "conflict", "grant is not active (already revoked)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": grant.StatusRevoked})
}
