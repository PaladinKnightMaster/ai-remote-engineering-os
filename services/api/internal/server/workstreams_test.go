package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/workstream"
)

// fakeWorkstreamStore captures the charter Input so tests can assert what the handler
// binds (the window default, the grant scope) without Postgres. The lifecycle verbs
// return the scripted verbOK (ok=false → the handler's 409).
type fakeWorkstreamStore struct {
	lastCreate    *workstream.Input
	byID          map[string]workstream.Workstream
	verbOK        bool
	verbCalls     []string
	closedGrants  []string // ClosePlanRequestsForGrant calls (T-089, the RevokeGrant cascade)
	closedGrantsN int      // the scripted return count

	// T-090 fixtures: workstream.poll / plan.submit's store-side needs.
	pollResults      []workstream.PlanRequestComposition
	pollErr          error
	planRequests     map[string]workstream.PlanRequest
	markSubmittedOK  bool
	markSubmittedErr error
	lastMarkRunID    string
	lastMarkActor    string

	// T-091 fixtures: the workstream-detail plan-request history + Create's
	// validation-error simulation (the store's kind-matrix, mapped to 422).
	planRequestHistory     []workstream.PlanRequest
	lastListPlanRequestsID string
	lastListPlanRequestsN  int
	createErr              error
}

func (f *fakeWorkstreamStore) ClosePlanRequestsForGrant(_ context.Context, grantID string) (int, error) {
	f.closedGrants = append(f.closedGrants, grantID)
	return f.closedGrantsN, nil
}

func (f *fakeWorkstreamStore) PollOpenPlanRequests(_ context.Context, _, _ string) ([]workstream.PlanRequestComposition, error) {
	return f.pollResults, f.pollErr
}

func (f *fakeWorkstreamStore) GetPlanRequest(_ context.Context, id string) (workstream.PlanRequest, error) {
	if pr, ok := f.planRequests[id]; ok {
		return pr, nil
	}
	return workstream.PlanRequest{}, workstream.ErrPlanRequestNotFound
}

func (f *fakeWorkstreamStore) MarkPlanRequestSubmitted(_ context.Context, _, runID, actor string) (bool, error) {
	f.lastMarkRunID, f.lastMarkActor = runID, actor
	return f.markSubmittedOK, f.markSubmittedErr
}

func (f *fakeWorkstreamStore) Pause(_ context.Context, id string) (bool, error) {
	f.verbCalls = append(f.verbCalls, "pause:"+id)
	return f.verbOK, nil
}

func (f *fakeWorkstreamStore) Resume(_ context.Context, id string) (bool, error) {
	f.verbCalls = append(f.verbCalls, "resume:"+id)
	return f.verbOK, nil
}

func (f *fakeWorkstreamStore) Retire(_ context.Context, id string) (bool, error) {
	f.verbCalls = append(f.verbCalls, "retire:"+id)
	return f.verbOK, nil
}

func (f *fakeWorkstreamStore) Create(_ context.Context, in workstream.Input) (workstream.Workstream, error) {
	f.lastCreate = &in
	if f.createErr != nil {
		return workstream.Workstream{}, f.createErr
	}
	steps := make([]workstream.Step, len(in.Steps))
	for i, st := range in.Steps {
		steps[i] = workstream.Step{Index: i, ResourceID: st.ResourceID, Command: st.Command}
	}
	kind := in.Kind
	if kind == "" {
		kind = workstream.KindTemplate
	}
	return workstream.Workstream{
		ID: "ws-00001", Principal: in.Principal, Label: in.Label, Objective: in.Objective,
		GrantID: "grant-00001", IntervalSeconds: in.IntervalSeconds,
		NextFireAt: time.Now().Add(time.Duration(in.IntervalSeconds) * time.Second),
		Status:     workstream.StatusActive,
		Steps:      steps, Kind: kind, Composer: in.Composer,
	}, nil
}

func (f *fakeWorkstreamStore) Get(_ context.Context, id string) (workstream.Workstream, error) {
	if w, ok := f.byID[id]; ok {
		return w, nil
	}
	return workstream.Workstream{}, workstream.ErrNotFound
}

// ListPlanRequestsForWorkstream mirrors the store's bounded, newest-first history
// (D3, T-091) — the scripted planRequestHistory, regardless of workstreamID/limit
// (tests assert the args via lastListPlanRequestsID/N instead).
func (f *fakeWorkstreamStore) ListPlanRequestsForWorkstream(_ context.Context, workstreamID string, limit int) ([]workstream.PlanRequest, error) {
	f.lastListPlanRequestsID, f.lastListPlanRequestsN = workstreamID, limit
	return f.planRequestHistory, nil
}

func (f *fakeWorkstreamStore) ListForOperator(_ context.Context, operator, _ string, _ int) ([]workstream.Workstream, string, error) {
	var out []workstream.Workstream
	for _, w := range f.byID {
		if w.Principal == operator {
			out = append(out, w)
		}
	}
	return out, "", nil
}

func newWorkstreamHandler(fake *fakeWorkstreamStore, minInterval int) http.Handler {
	key, _ := auth.GenerateKey()
	return New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "",
		WithWorkstreamStore(fake, minInterval))
}

func charterBody(mut func(map[string]any)) string {
	m := map[string]any{
		"label": "nightly", "objective": "keep hosts healthy",
		"resources": []string{"res-a", "res-b"}, "verbs": []string{"exec"},
		"ttlSeconds": 86400, "intervalSeconds": 300,
		"maxStepsPerRun": 5, "maxConcurrentRuns": 2, "stepBudget": 10,
		"steps": []map[string]string{
			{"resourceId": "res-a", "command": "uptime"},
		},
	}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func postWorkstream(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/workstreams", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestCreateWorkstreamHappy — a valid charter answers 201 with the template echoed; the
// handler binds the grant scope from the charter and defaults the budget window to the
// interval (WS-4). Grantee/principal are NOT handler-bound (the store binds them — the
// Input carries an empty Agent).
func TestCreateWorkstreamHappy(t *testing.T) {
	fake := &fakeWorkstreamStore{}
	h := newWorkstreamHandler(fake, 60)
	rec := postWorkstream(t, h, charterBody(nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	in := fake.lastCreate
	if in == nil {
		t.Fatal("store never called")
	}
	if in.Principal != "operator" || in.Objective != "keep hosts healthy" {
		t.Fatalf("input binding wrong: %+v", in)
	}
	if in.Grant.Agent != "" {
		t.Fatalf("handler must not bind the grantee (store's job), got %q", in.Grant.Agent)
	}
	if in.Grant.BudgetWindowSeconds != 300 {
		t.Fatalf("budget window default = %d, want the interval 300", in.Grant.BudgetWindowSeconds)
	}
	if len(in.Grant.Resources) != 2 || in.Grant.Verbs[0] != "exec" || in.Grant.StepBudget != 10 {
		t.Fatalf("grant scope binding wrong: %+v", in.Grant)
	}
	var got workstreamView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != workstream.StatusActive || len(got.Steps) != 1 || got.GrantID == "" {
		t.Fatalf("view wrong: %+v", got)
	}
}

// TestCreateWorkstream422Matrix — the charter-time gate (WS-2): sub-floor interval,
// template outside its own scope, template exceeding its own bounds (F2: a charter that
// could never fire is rejected at consent), a negative window, and the SHARED issuance
// bounds (SEC-30b — the same validator CreateGrant runs, proven by the ceiling case).
func TestCreateWorkstream422Matrix(t *testing.T) {
	cases := []struct {
		name string
		mut  func(map[string]any)
	}{
		{"interval below floor", func(m map[string]any) { m["intervalSeconds"] = 30 }},
		{"template outside scope", func(m map[string]any) {
			m["steps"] = []map[string]string{{"resourceId": "res-x", "command": "id"}}
		}},
		{"template exceeds maxStepsPerRun", func(m map[string]any) {
			m["maxStepsPerRun"] = 1
			m["steps"] = []map[string]string{
				{"resourceId": "res-a", "command": "a"}, {"resourceId": "res-b", "command": "b"},
			}
		}},
		{"template exceeds stepBudget", func(m map[string]any) {
			m["stepBudget"] = 1
			m["maxStepsPerRun"] = 5
			m["steps"] = []map[string]string{
				{"resourceId": "res-a", "command": "a"}, {"resourceId": "res-b", "command": "b"},
			}
		}},
		{"negative window", func(m map[string]any) { m["budgetWindowSeconds"] = -5 }},
		{"shared bounds: concurrency ceiling", func(m map[string]any) { m["maxConcurrentRuns"] = 5 }},
		{"shared bounds: zero ttl", func(m map[string]any) { m["ttlSeconds"] = 0 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWorkstreamStore{}
			h := newWorkstreamHandler(fake, 60)
			rec := postWorkstream(t, h, charterBody(c.mut))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
			}
			if fake.lastCreate != nil {
				t.Fatal("store must not be called on a rejected charter")
			}
		})
	}
}

// TestCreateWorkstream400 — structurally invalid charters answer 400 before any store
// call (no objective / an incomplete step / an unknown field). "No steps" is NOT here
// (D4, T-091): steps is now optional on the wire — an empty/absent array reaches the
// store, whose kind matrix is the SINGLE validator (see
// TestCreateWorkstream422FromStoreValidation below).
func TestCreateWorkstream400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no objective", charterBody(func(m map[string]any) { m["objective"] = "" })},
		{"incomplete step", charterBody(func(m map[string]any) {
			m["steps"] = []map[string]string{{"resourceId": "res-a"}}
		})},
		{"unknown field", `{"objective":"x","nope":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeWorkstreamStore{}
			h := newWorkstreamHandler(fake, 60)
			rec := postWorkstream(t, h, c.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestCreateWorkstreamAdaptiveHappy — an adaptive charter (kind + composer, NO steps
// key) answers 201 and binds Kind/Composer through to the store (D4, ADR-0065 §4);
// the view echoes kind=adaptive + the composer.
func TestCreateWorkstreamAdaptiveHappy(t *testing.T) {
	fake := &fakeWorkstreamStore{}
	h := newWorkstreamHandler(fake, 60)
	body := charterBody(func(m map[string]any) {
		delete(m, "steps")
		m["kind"] = "adaptive"
		m["composer"] = "agent:composer-1"
	})
	rec := postWorkstream(t, h, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastCreate == nil || fake.lastCreate.Kind != "adaptive" || fake.lastCreate.Composer != "agent:composer-1" {
		t.Fatalf("input binding wrong: %+v", fake.lastCreate)
	}
	if len(fake.lastCreate.Steps) != 0 {
		t.Fatalf("an adaptive charter must bind zero steps, got %d", len(fake.lastCreate.Steps))
	}
	var got workstreamView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "adaptive" || got.Composer != "agent:composer-1" {
		t.Fatalf("view wrong: %+v", got)
	}
}

// TestCreateWorkstream422FromStoreValidation — the store's kind-matrix validation
// (e.g. a template charter with no steps, or an adaptive charter with no composer)
// surfaces as 422 via errors.Is(err, workstream.ErrValidation) — the store, not the
// handler, is the single validator for this matrix (D4).
func TestCreateWorkstream422FromStoreValidation(t *testing.T) {
	fake := &fakeWorkstreamStore{createErr: fmt.Errorf("%w: workstream requires at least one template step", workstream.ErrValidation)}
	h := newWorkstreamHandler(fake, 60)
	body := charterBody(func(m map[string]any) { delete(m, "steps") })
	rec := postWorkstream(t, h, body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "at least one template step") {
		t.Fatalf("body must surface the store's validation message, got %s", rec.Body.String())
	}
}

// TestGetWorkstreamOwnerScoped — a foreign charter answers 404 (existence-hiding); the
// owner's answers 200 with the template.
func TestGetWorkstreamOwnerScoped(t *testing.T) {
	fake := &fakeWorkstreamStore{byID: map[string]workstream.Workstream{
		"ws-mine":   {ID: "ws-mine", Principal: "operator", Objective: "o", Status: workstream.StatusActive, Steps: []workstream.Step{{ResourceID: "res-a", Command: "uptime"}}},
		"ws-theirs": {ID: "ws-theirs", Principal: "operator:other", Objective: "o", Status: workstream.StatusActive},
	}}
	h := newWorkstreamHandler(fake, 60)

	get := func(id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/workstreams/"+id, nil)
		r.Header.Set("Authorization", "Bearer x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if rec := get("ws-mine"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "uptime") {
		t.Fatalf("own charter: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := get("ws-theirs"); rec.Code != http.StatusNotFound {
		t.Fatalf("foreign charter: status=%d, want 404", rec.Code)
	}
	if rec := get("ws-unknown"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown charter: status=%d, want 404", rec.Code)
	}
}

// TestGetWorkstreamPlanRequests — the detail view carries the charter's bounded
// plan-request history (D3, ADR-0065 §5, T-091): the handler calls
// ListPlanRequestsForWorkstream(id, 10) and renders whatever comes back.
func TestGetWorkstreamPlanRequests(t *testing.T) {
	expiresAt := time.Now().Add(90 * time.Second)
	fake := &fakeWorkstreamStore{
		byID: map[string]workstream.Workstream{
			"ws-mine": {ID: "ws-mine", Principal: "operator", Objective: "o", Kind: workstream.KindAdaptive, Composer: "agent:composer-1", Status: workstream.StatusActive},
		},
		planRequestHistory: []workstream.PlanRequest{
			{ID: "pr-1", WorkstreamID: "ws-mine", Status: workstream.PlanRequestOpen, ExpiresAt: expiresAt},
		},
	}
	h := newWorkstreamHandler(fake, 60)
	r := httptest.NewRequest(http.MethodGet, "/v1/workstreams/ws-mine", nil)
	r.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastListPlanRequestsID != "ws-mine" || fake.lastListPlanRequestsN != planRequestHistoryLimit {
		t.Fatalf("ListPlanRequestsForWorkstream bound wrong: id=%q limit=%d", fake.lastListPlanRequestsID, fake.lastListPlanRequestsN)
	}
	var got workstreamView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Kind != "adaptive" || got.Composer != "agent:composer-1" {
		t.Fatalf("kind/composer wrong: %+v", got)
	}
	if len(got.PlanRequests) != 1 || got.PlanRequests[0].ID != "pr-1" || got.PlanRequests[0].Status != "open" {
		t.Fatalf("planRequests wrong: %+v", got.PlanRequests)
	}
}

// TestWorkstreamLifecycleVerbHandlers — the verb routes (WS-5, T-074): owner-scoped 404,
// 409 on a refused flip, 200 + the right status on success — and retire triggers the
// in-flight cascade on the charter's grant (the RevokeGrant mirror).
func TestWorkstreamLifecycleVerbHandlers(t *testing.T) {
	own := map[string]workstream.Workstream{
		"ws-mine":   {ID: "ws-mine", Principal: "operator", Objective: "o", GrantID: "grant-9", Status: workstream.StatusActive},
		"ws-theirs": {ID: "ws-theirs", Principal: "operator:other", Objective: "o", Status: workstream.StatusActive},
	}
	post := func(t *testing.T, fake *fakeWorkstreamStore, rs *fakeLaunchRunStore, path string) *httptest.ResponseRecorder {
		t.Helper()
		key, _ := auth.GenerateKey()
		opts := []Option{WithWorkstreamStore(fake, 60)}
		if rs != nil {
			opts = append(opts, WithRunStore(rs))
		}
		h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", opts...)
		r := httptest.NewRequest(http.MethodPost, path, nil)
		r.Header.Set("Authorization", "Bearer x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	for _, verb := range []string{"pause", "resume", "retire"} {
		t.Run(verb+" owner-scoped 404", func(t *testing.T) {
			fake := &fakeWorkstreamStore{byID: own, verbOK: true}
			if rec := post(t, fake, nil, "/v1/workstreams/ws-theirs/"+verb); rec.Code != http.StatusNotFound {
				t.Fatalf("foreign: status=%d, want 404", rec.Code)
			}
			if rec := post(t, fake, nil, "/v1/workstreams/ws-unknown/"+verb); rec.Code != http.StatusNotFound {
				t.Fatalf("unknown: status=%d, want 404", rec.Code)
			}
			if len(fake.verbCalls) != 0 {
				t.Fatalf("verb reached the store on a 404: %v", fake.verbCalls)
			}
		})
		t.Run(verb+" refused flip → 409", func(t *testing.T) {
			fake := &fakeWorkstreamStore{byID: own, verbOK: false}
			if rec := post(t, fake, nil, "/v1/workstreams/ws-mine/"+verb); rec.Code != http.StatusConflict {
				t.Fatalf("status=%d, want 409", rec.Code)
			}
		})
	}

	t.Run("pause/resume happy", func(t *testing.T) {
		fake := &fakeWorkstreamStore{byID: own, verbOK: true}
		if rec := post(t, fake, nil, "/v1/workstreams/ws-mine/pause"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), workstream.StatusPaused) {
			t.Fatalf("pause: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if rec := post(t, fake, nil, "/v1/workstreams/ws-mine/resume"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), workstream.StatusActive) {
			t.Fatalf("resume: status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("retire happy + cascade", func(t *testing.T) {
		fake := &fakeWorkstreamStore{byID: own, verbOK: true}
		rs := &fakeLaunchRunStore{}
		if rec := post(t, fake, rs, "/v1/workstreams/ws-mine/retire"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), workstream.StatusRetired) {
			t.Fatalf("retire: status=%d body=%s", rec.Code, rec.Body.String())
		}
		if len(rs.canceledGrants) != 1 || rs.canceledGrants[0] != "grant-9" {
			t.Fatalf("cascade not run on the charter's grant: %v", rs.canceledGrants)
		}
	})

	t.Run("retire without a run store skips the cascade", func(t *testing.T) {
		fake := &fakeWorkstreamStore{byID: own, verbOK: true}
		if rec := post(t, fake, nil, "/v1/workstreams/ws-mine/retire"); rec.Code != http.StatusOK {
			t.Fatalf("retire sans runs: status=%d", rec.Code)
		}
	})
}

// fakeRevokableGrantStore is a minimal grantStore whose Get/Revoke succeed for one
// fixed grant id — the RevokeGrant cascade-wiring test below (T-089 R3 point 1) needs
// a HAPPY-PATH revoke, unlike the shared fakeGrantStore (delegated_test.go), which is
// deliberately scripted to always fail (its own caller, TestLaunchDelegatedRun, needs
// the opposite: an out-of-grant refusal).
type fakeRevokableGrantStore struct{ id, principal string }

func (f fakeRevokableGrantStore) Create(context.Context, grant.Input) (grant.Grant, error) {
	return grant.Grant{}, nil
}
func (f fakeRevokableGrantStore) Get(_ context.Context, id string) (grant.Grant, error) {
	if id != f.id {
		return grant.Grant{}, grant.ErrNotFound
	}
	return grant.Grant{ID: f.id, Principal: f.principal, Status: grant.StatusActive}, nil
}
func (f fakeRevokableGrantStore) ListForOperator(context.Context, string, string, int) ([]grant.Grant, string, error) {
	return nil, "", nil
}
func (f fakeRevokableGrantStore) Revoke(context.Context, string) (bool, error) { return true, nil }
func (f fakeRevokableGrantStore) AuditLaunchDenied(context.Context, string, string) error {
	return nil
}

// TestRevokeGrantCascadesToPlanRequests — DUR-10 §7/R3 point 1 (T-089): RevokeGrant's
// handler reaches BOTH cascade legs on a successful revoke — runStore.CancelRunsForGrant
// (DUR-7, unchanged) AND workstreamStore.ClosePlanRequestsForGrant (the new leg this
// ticket wires) — each called exactly once with the revoked grant's id.
func TestRevokeGrantCascadesToPlanRequests(t *testing.T) {
	key, _ := auth.GenerateKey()
	gs := fakeRevokableGrantStore{id: "grant-9", principal: "operator"}
	rs := &fakeLaunchRunStore{}
	ws := &fakeWorkstreamStore{closedGrantsN: 1}
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "",
		WithGrantStore(gs), WithRunStore(rs), WithWorkstreamStore(ws, 60))
	r := httptest.NewRequest(http.MethodPost, "/v1/grants/grant-9/revoke", nil)
	r.Header.Set("Authorization", "Bearer x")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(rs.canceledGrants) != 1 || rs.canceledGrants[0] != "grant-9" {
		t.Fatalf("CancelRunsForGrant cascade: %v, want exactly [grant-9]", rs.canceledGrants)
	}
	if len(ws.closedGrants) != 1 || ws.closedGrants[0] != "grant-9" {
		t.Fatalf("ClosePlanRequestsForGrant cascade: %v, want exactly [grant-9]", ws.closedGrants)
	}
}

// TestWorkstreamRoutesUnconfigured — no WithWorkstreamStore → the routes exist but
// answer 503 (dependency absent), not 404.
func TestWorkstreamRoutesUnconfigured(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/workstreams", charterBody(nil)},
		{http.MethodGet, "/v1/workstreams", ""},
		{http.MethodGet, "/v1/workstreams/x", ""},
		{http.MethodPost, "/v1/workstreams/x/pause", ""},
		{http.MethodPost, "/v1/workstreams/x/resume", ""},
		{http.MethodPost, "/v1/workstreams/x/retire", ""},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Authorization", "Bearer x")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s: status=%d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}
