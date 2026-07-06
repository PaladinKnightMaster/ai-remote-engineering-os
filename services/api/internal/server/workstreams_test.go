package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/workstream"
)

// fakeWorkstreamStore captures the charter Input so tests can assert what the handler
// binds (the window default, the grant scope) without Postgres.
type fakeWorkstreamStore struct {
	lastCreate *workstream.Input
	byID       map[string]workstream.Workstream
}

func (f *fakeWorkstreamStore) Create(_ context.Context, in workstream.Input) (workstream.Workstream, error) {
	f.lastCreate = &in
	return workstream.Workstream{
		ID: "ws-00001", Principal: in.Principal, Label: in.Label, Objective: in.Objective,
		GrantID: "grant-00001", IntervalSeconds: in.IntervalSeconds,
		NextFireAt: time.Now().Add(time.Duration(in.IntervalSeconds) * time.Second),
		Status:     workstream.StatusActive,
		Steps:      []workstream.Step{{Index: 0, ResourceID: in.Steps[0].ResourceID, Command: in.Steps[0].Command}},
	}, nil
}

func (f *fakeWorkstreamStore) Get(_ context.Context, id string) (workstream.Workstream, error) {
	if w, ok := f.byID[id]; ok {
		return w, nil
	}
	return workstream.Workstream{}, workstream.ErrNotFound
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
// call (no objective / no steps / an incomplete step / an unknown field).
func TestCreateWorkstream400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no objective", charterBody(func(m map[string]any) { m["objective"] = "" })},
		{"no steps", charterBody(func(m map[string]any) { m["steps"] = []map[string]string{} })},
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

// TestWorkstreamRoutesUnconfigured — no WithWorkstreamStore → the routes exist but
// answer 503 (dependency absent), not 404.
func TestWorkstreamRoutesUnconfigured(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "")
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/workstreams", charterBody(nil)},
		{http.MethodGet, "/v1/workstreams", ""},
		{http.MethodGet, "/v1/workstreams/x", ""},
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
