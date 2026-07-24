package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/ctxindex"
	"aireos.local/services/api/internal/memory"
	"aireos.local/services/api/internal/run"
	"aireos.local/services/api/internal/workstream"
)

// notGatewayVerifier authenticates every request as SOME OTHER service-service caller
// (not the gateway) — the subject-pin negative (SEC-27): a valid service-service token
// that is not the gateway's own must still be refused.
type notGatewayVerifier struct{}

func (notGatewayVerifier) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "svc:someone-else", Edge: "service-service"}, nil
}

func adaptiveHandler(t *testing.T, ws *fakeWorkstreamStore, gs grantStore, rs runStore, ms *fakeMemoryStore, verifier mw.Verifier) http.Handler {
	t.Helper()
	key, _ := auth.GenerateKey()
	opts := []Option{WithGatewayVerifier(verifier)}
	if ws != nil {
		opts = append(opts, WithWorkstreamStore(ws, 0))
	}
	if gs != nil {
		opts = append(opts, WithGrantStore(gs))
	}
	if rs != nil {
		opts = append(opts, WithRunStore(rs))
	}
	if ms != nil {
		opts = append(opts, WithMemoryStore(ms))
	}
	return New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", opts...)
}

func postInternal(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// --- workstream.poll (D1) -----------------------------------------------------------

func TestPollWorkstreamHappyPath(t *testing.T) {
	ws := &fakeWorkstreamStore{pollResults: []workstream.PlanRequestComposition{
		{RequestID: "req-1", WorkstreamID: "ws-1", Objective: "keep it healthy",
			Resources: []string{"res-a"}, Verbs: []string{"exec"}, MaxStepsPerRun: 5,
			ExpiresAt: time.Now().Add(time.Hour)},
	}}
	h := adaptiveHandler(t, ws, nil, nil, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-polls", `{"agentSubject":"agent:x"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "req-1") || !strings.Contains(rec.Body.String(), "keep it healthy") {
		t.Fatalf("body missing composition context: %s", rec.Body.String())
	}
}

func TestPollWorkstreamGatewayOnlyPin(t *testing.T) {
	ws := &fakeWorkstreamStore{}
	h := adaptiveHandler(t, ws, nil, nil, nil, notGatewayVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-polls", `{"agentSubject":"agent:x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-gateway service-service caller: status = %d, want 403", rec.Code)
	}
}

func TestPollWorkstreamRequiresAgentSubject(t *testing.T) {
	h := adaptiveHandler(t, &fakeWorkstreamStore{}, nil, nil, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-polls", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestPollWorkstreamUnconfigured(t *testing.T) {
	h := adaptiveHandler(t, nil, nil, nil, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-polls", `{"agentSubject":"agent:x"}`)
	if rec.Code != http.StatusNotFound {
		// The route is not mounted at all when h.workstreams is nil (no group entry).
		t.Fatalf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}

// --- plan.submit (D3) ----------------------------------------------------------------

const validSubmitBody = `{"agentSubject":"agent:composer","requestId":"req-1",
	"steps":[{"resourceId":"res-a","command":"uptime"}]}`

func TestSubmitPlanNoOracleUnknownRequest(t *testing.T) {
	ws := &fakeWorkstreamStore{planRequests: map[string]workstream.PlanRequest{}}
	rs := &fakeLaunchRunStore{}
	h := adaptiveHandler(t, ws, fakeGrantStore{}, rs, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-submissions", validSubmitBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unknown request: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), submitPlanRefusalReason) {
		t.Fatalf("unknown request body = %s, want the canonical no-oracle refusal reason", rec.Body.String())
	}
}

// TestSubmitPlanNoOracleForeignAgent — a request that EXISTS (open) but whose grant
// belongs to a different agent answers the IDENTICAL refusal as the unknown-request
// case above (§6's impersonation-pin no-oracle) — driven through the shared core's own
// grant check (validatePlanAgainstGrant), not a handler-side shortcut.
func TestSubmitPlanNoOracleForeignAgent(t *testing.T) {
	ws := &fakeWorkstreamStore{
		planRequests: map[string]workstream.PlanRequest{
			"req-1": {ID: "req-1", WorkstreamID: "ws-1", Status: workstream.PlanRequestOpen, LaunchKey: "k-1"},
		},
		byID: map[string]workstream.Workstream{
			"ws-1": {ID: "ws-1", GrantID: "grant-1"},
		},
	}
	// A launch-core refusal simulating the impersonation pin (validatePlanAgainstGrant's
	// exact reason string) — this test proves the WIRING (handler → core → the SAME
	// message), not the core's own logic (unit-tested in internal/run).
	rs := &fakeLaunchRunStore{refusal: &run.Refusal{Kind: run.RefusalOutOfGrant, Reason: submitPlanRefusalReason}}
	h := adaptiveHandler(t, ws, fakeGrantStore{}, rs, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-submissions", validSubmitBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign agent: status = %d, want 403 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), submitPlanRefusalReason) {
		t.Fatalf("foreign agent body = %s, want the canonical no-oracle refusal reason", rec.Body.String())
	}
}

func TestSubmitPlanBoundsRefusalIs429(t *testing.T) {
	ws := &fakeWorkstreamStore{
		planRequests: map[string]workstream.PlanRequest{
			"req-1": {ID: "req-1", WorkstreamID: "ws-1", Status: workstream.PlanRequestOpen, LaunchKey: "k-1"},
		},
		byID: map[string]workstream.Workstream{"ws-1": {ID: "ws-1", GrantID: "grant-1"}},
	}
	rs := &fakeLaunchRunStore{refusal: &run.Refusal{Kind: run.RefusalBounds, Reason: "the grant's step budget is exhausted"}}
	h := adaptiveHandler(t, ws, fakeGrantStore{}, rs, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-submissions", validSubmitBody)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("bounds refusal: status = %d, want 429 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestSubmitPlanHappyPathMarksSubmitted(t *testing.T) {
	ws := &fakeWorkstreamStore{
		planRequests: map[string]workstream.PlanRequest{
			"req-1": {ID: "req-1", WorkstreamID: "ws-1", Status: workstream.PlanRequestOpen, LaunchKey: "k-1"},
		},
		byID:            map[string]workstream.Workstream{"ws-1": {ID: "ws-1", GrantID: "grant-1"}},
		markSubmittedOK: true,
	}
	rs := &fakeLaunchRunStore{launched: run.Run{ID: "run-9", Status: run.StatusProposed}}
	h := adaptiveHandler(t, ws, fakeGrantStore{}, rs, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/plan-submissions", validSubmitBody)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "run-9") || !strings.Contains(rec.Body.String(), "proposed") {
		t.Fatalf("happy path: status=%d body=%s, want 200 + run-9 + proposed", rec.Code, rec.Body.String())
	}
	if ws.lastMarkRunID != "run-9" || ws.lastMarkActor != "agent:composer" {
		t.Fatalf("MarkPlanRequestSubmitted called with runID=%q actor=%q, want run-9 / agent:composer", ws.lastMarkRunID, ws.lastMarkActor)
	}
}

func TestSubmitPlan400(t *testing.T) {
	ws := &fakeWorkstreamStore{}
	rs := &fakeLaunchRunStore{}
	h := adaptiveHandler(t, ws, fakeGrantStore{}, rs, nil, gwOnlyVerifier{})
	for name, body := range map[string]string{
		"no agent":     `{"requestId":"r","steps":[{"resourceId":"r","command":"c"}]}`,
		"no requestId": `{"agentSubject":"a","steps":[{"resourceId":"r","command":"c"}]}`,
		"no steps":     `{"agentSubject":"a","requestId":"r","steps":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := postInternal(t, h, "/v1/internal/plan-submissions", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// --- memory.read (D2) -----------------------------------------------------------------

func TestReadMemoryHappyPath(t *testing.T) {
	ms := &fakeMemoryStore{readItems: []memory.Memory{{ID: "mem-1", ScopeType: "run", ScopeID: "run-1"}}}
	h := adaptiveHandler(t, nil, nil, nil, ms, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/memory-reads", `{"agentSubject":"agent:x","workstreamId":"ws-1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "mem-1") {
		t.Fatalf("status=%d body=%s, want 200 + mem-1", rec.Code, rec.Body.String())
	}
	if ms.lastReadAgent != "agent:x" || ms.lastReadWorkstream != "ws-1" {
		t.Fatalf("ReadForAgent called with agent=%q ws=%q, want agent:x / ws-1", ms.lastReadAgent, ms.lastReadWorkstream)
	}
}

func TestReadMemoryGatewayOnlyPin(t *testing.T) {
	ms := &fakeMemoryStore{}
	h := adaptiveHandler(t, nil, nil, nil, ms, notGatewayVerifier{})
	rec := postInternal(t, h, "/v1/internal/memory-reads", `{"agentSubject":"agent:x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-gateway caller: status = %d, want 403", rec.Code)
	}
}

func TestReadMemoryRequiresAgentSubject(t *testing.T) {
	h := adaptiveHandler(t, nil, nil, nil, &fakeMemoryStore{}, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/memory-reads", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --- context.search (D4, ADR-0066 §4, T-099) ----------------------------------------

// fakeContextStore captures the last SearchForAgent call (the fakeMemoryStore
// readItems/lastReadAgent shape, applied to the context-index forward).
type fakeContextStore struct {
	results   []ctxindex.ChunkResult
	err       error
	lastAgent string
	lastWS    string
	lastQuery string
	lastLimit int
}

func (f *fakeContextStore) SearchForAgent(_ context.Context, agentSubject, workstreamID, query string, limit int) ([]ctxindex.ChunkResult, error) {
	f.lastAgent, f.lastWS, f.lastQuery, f.lastLimit = agentSubject, workstreamID, query, limit
	return f.results, f.err
}

// contextHandler builds a handler wired ONLY with the context-index store: context.search
// needs no workstream/grant/run/memory store, so a dedicated minimal builder avoids
// threading a sixth always-nil param through every adaptiveHandler call site above.
func contextHandler(t *testing.T, cs contextSearchStore, verifier mw.Verifier) http.Handler {
	t.Helper()
	key, _ := auth.GenerateKey()
	opts := []Option{WithGatewayVerifier(verifier)}
	if cs != nil {
		opts = append(opts, WithContextStore(cs))
	}
	return New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", opts...)
}

func TestSearchContextHappyPath(t *testing.T) {
	cs := &fakeContextStore{results: []ctxindex.ChunkResult{{ChunkID: "chunk-1", FilePath: "docs/runbook.md"}}}
	h := contextHandler(t, cs, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/context-searches", `{"agentSubject":"agent:x","query":"ops runbook","workstreamId":"ws-1"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "chunk-1") {
		t.Fatalf("status=%d body=%s, want 200 + chunk-1", rec.Code, rec.Body.String())
	}
	if cs.lastAgent != "agent:x" || cs.lastWS != "ws-1" || cs.lastQuery != "ops runbook" {
		t.Fatalf("SearchForAgent called with agent=%q ws=%q query=%q, want agent:x / ws-1 / ops runbook",
			cs.lastAgent, cs.lastWS, cs.lastQuery)
	}
}

func TestSearchContextGatewayOnlyPin(t *testing.T) {
	cs := &fakeContextStore{}
	h := contextHandler(t, cs, notGatewayVerifier{})
	rec := postInternal(t, h, "/v1/internal/context-searches", `{"agentSubject":"agent:x","query":"ops"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-gateway caller: status = %d, want 403", rec.Code)
	}
}

func TestSearchContextRequiresAgentSubject(t *testing.T) {
	h := contextHandler(t, &fakeContextStore{}, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/context-searches", `{"query":"ops"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSearchContextUnconfigured(t *testing.T) {
	h := contextHandler(t, nil, gwOnlyVerifier{})
	rec := postInternal(t, h, "/v1/internal/context-searches", `{"agentSubject":"agent:x","query":"ops"}`)
	if rec.Code != http.StatusNotFound {
		// The route is not mounted at all when h.contextIndex is nil (no group entry).
		t.Fatalf("status = %d, want 404 (route not mounted)", rec.Code)
	}
}
