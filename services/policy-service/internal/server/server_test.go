package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/policy-service/internal/server"
	"aireos.local/services/policy-service/internal/store"
)

// allowVerifier accepts any bearer as a service-service caller (the audience
// policy-service expects). A request with no bearer is rejected by
// RequireCapability before this is consulted.
type allowVerifier struct{}

func (allowVerifier) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "api", Edge: "service-service"}, nil
}

// fakeStore is a DecisionStore that never touches a database — T-150 made
// decideHandler consult the evaluator on every call, so a real *store.Store
// with a nil pool is no longer usable in a unit test (it would panic on the
// first query); this fake is the injection seam server.Config.Store exists
// for, same shape as session-broker's SessionStore fakes.
type fakeStore struct {
	evalOutcome string
	evalOk      bool
	evalErr     error
	recordErr   error
	createErr   error

	recordedOutcomes []string // outcome values passed to RecordDecision, in order
	createCalled     bool
}

func (f *fakeStore) EvaluateTuple(context.Context, store.Tuple) (string, store.Rule, bool, error) {
	return f.evalOutcome, store.Rule{}, f.evalOk, f.evalErr
}

func (f *fakeStore) RecordDecision(_ context.Context, _, _, _, _, outcome string) error {
	f.recordedOutcomes = append(f.recordedOutcomes, outcome)
	return f.recordErr
}

func (f *fakeStore) CreatePending(_ context.Context, in store.CreatePendingInput, ttl time.Duration) (store.PendingApproval, error) {
	f.createCalled = true
	if f.createErr != nil {
		return store.PendingApproval{}, f.createErr
	}
	return store.PendingApproval{
		ID: "pa-1", SessionID: in.SessionID, Status: "pending",
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

func (f *fakeStore) Resolve(context.Context, string, string, string) (store.PendingApproval, error) {
	return store.PendingApproval{}, nil
}

func newHandler() http.Handler {
	return server.New(server.Config{Verifier: allowVerifier{}})
}

// newHandlerWithStore builds a handler over an injected fake, so decide/resolve
// can be unit-tested without Postgres.
func newHandlerWithStore(st server.DecisionStore) http.Handler {
	return server.New(server.Config{Verifier: allowVerifier{}, Store: st})
}

func decideBody(requiresApproval bool) []byte {
	body, _ := json.Marshal(map[string]any{
		"session_id": "s1", "subject": "operator:admin", "edge": "agent-remote-session",
		"target": "resource:r1", "action": "session.start", "requires_approval": requiresApproval,
	})
	return body
}

func doDecide(t *testing.T, h http.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthzOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: got %d %q", rec.Code, rec.Body.String())
	}
}

// TestDecideRequiresCapability — the gated RPC rejects a request with no bearer
// (401) before any handler/store work.
func TestDecideRequiresCapability(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader([]byte(`{}`)))
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: want 401, got %d", rec.Code)
	}
}

// TestDecideAllowUnflagged — CON-4 fallback: no rule matches (ok=false) and the
// resource is unflagged (requires_approval=false) → allow. This now DOES chain
// policy.decision:allow (ADR-0074 §D4 clause 4 — allow decisions join the
// ledger); it no longer skips the store the way the pre-T-150 handler did.
func TestDecideAllowUnflagged(t *testing.T) {
	fs := &fakeStore{evalOk: false}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(false))
	if rec.Code != http.StatusOK {
		t.Fatalf("decide allow: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("want allow, got %q", resp.Decision)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("approval")) {
		t.Fatalf("allow decision must not carry an approval ref: %s", rec.Body.String())
	}
	if len(fs.recordedOutcomes) != 1 || fs.recordedOutcomes[0] != store.OutcomeDecisionAllow {
		t.Fatalf("want exactly one policy.decision:allow recorded, got %v", fs.recordedOutcomes)
	}
	if fs.createCalled {
		t.Fatalf("an allow decision must never call CreatePending")
	}
}

// TestDecideRequiresApprovalFallback — CON-4 fallback for a flagged resource: no
// rule matches (ok=false) and requires_approval=true → the resource's label
// still parks a pending approval, exactly as v0.14.0 did. Named firing
// assertion for the mutation proof: createCalled must be true and the response
// decision must be requires_approval — a mutant that lets ok=false fall through
// to "allow" for a flagged resource fails THIS assertion.
func TestDecideRequiresApprovalFallback(t *testing.T) {
	fs := &fakeStore{evalOk: false}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(true))
	if rec.Code != http.StatusOK {
		t.Fatalf("decide requires_approval: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != store.DecisionRequiresApproval {
		t.Fatalf("CON-4 fallback for a flagged resource with no matching rule: want requires_approval, got %q", resp.Decision)
	}
	if !fs.createCalled {
		t.Fatalf("CON-4 fallback for a flagged resource with no matching rule: CreatePending was never called")
	}
	if len(fs.recordedOutcomes) != 0 {
		t.Fatalf("requires_approval must not also call RecordDecision (CreatePending chains its own event), got %v", fs.recordedOutcomes)
	}
}

// TestDecideDenyRule — a matched deny rule (ok=true, outcome=deny) returns
// decision:deny and chains policy.decision:deny, never touching CreatePending.
func TestDecideDenyRule(t *testing.T) {
	fs := &fakeStore{evalOk: true, evalOutcome: store.DecisionDeny}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(false))
	if rec.Code != http.StatusOK {
		t.Fatalf("decide deny: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != store.DecisionDeny {
		t.Fatalf("want deny, got %q", resp.Decision)
	}
	if len(fs.recordedOutcomes) != 1 || fs.recordedOutcomes[0] != store.OutcomeDecisionDeny {
		t.Fatalf("want exactly one policy.decision:deny recorded, got %v", fs.recordedOutcomes)
	}
	if fs.createCalled {
		t.Fatalf("a deny decision must never call CreatePending")
	}
}

// TestDecideAllowRuleMatched — a matched allow rule (ok=true, outcome=allow)
// behaves the same as the CON-4 unflagged-fallback case: allow, chained.
func TestDecideAllowRuleMatched(t *testing.T) {
	fs := &fakeStore{evalOk: true, evalOutcome: store.DecisionAllow}
	// requires_approval=true on the request — the matched allow rule must WIN
	// over the resource's own label (a matched rule always takes precedence
	// over the fallback, which only applies when ok=false).
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(true))
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != store.DecisionAllow {
		t.Fatalf("a matched allow rule must win over the flagged resource's label, got %q", resp.Decision)
	}
	if len(fs.recordedOutcomes) != 1 || fs.recordedOutcomes[0] != store.OutcomeDecisionAllow {
		t.Fatalf("want exactly one policy.decision:allow recorded, got %v", fs.recordedOutcomes)
	}
}

// TestDecideEvaluatorErrorFailsClosed — an evaluator error (e.g. DB down) is a
// 500, never a fallthrough to allow.
func TestDecideEvaluatorErrorFailsClosed(t *testing.T) {
	fs := &fakeStore{evalErr: errors.New("boom")}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("evaluator error: want 500, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(fs.recordedOutcomes) != 0 || fs.createCalled {
		t.Fatalf("an evaluator error must not record or park anything: outcomes=%v createCalled=%v", fs.recordedOutcomes, fs.createCalled)
	}
}

// TestDecideUnrecognisedOutcomeFailsClosed — CEO ruling 2026-08-28: a matched
// rule whose outcome is outside {allow, requires_approval, deny} is an
// operational fault (500), never synthesized into a deny.
func TestDecideUnrecognisedOutcomeFailsClosed(t *testing.T) {
	fs := &fakeStore{evalOk: true, evalOutcome: "not-a-real-outcome"}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unrecognized outcome: want 500, got %d (%s)", rec.Code, rec.Body.String())
	}
	if len(fs.recordedOutcomes) != 0 || fs.createCalled {
		t.Fatalf("an unrecognized outcome must not record or park anything: outcomes=%v createCalled=%v", fs.recordedOutcomes, fs.createCalled)
	}
}

// TestDecideRecordDecisionErrorFailsClosed — a RecordDecision write failure on
// the allow path is a 500, not a silently-unrecorded allow.
func TestDecideRecordDecisionErrorFailsClosed(t *testing.T) {
	fs := &fakeStore{evalOk: false, recordErr: errors.New("db down")}
	rec := doDecide(t, newHandlerWithStore(fs), decideBody(false))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("RecordDecision error: want 500, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestDecideBadBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Authorization", "Bearer t")
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: want 400, got %d", rec.Code)
	}
}

func TestResolveBadDecision(t *testing.T) {
	body, _ := json.Marshal(map[string]string{"decision": "maybe", "resolved_by": "operator:admin"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/approvals/s1/resolve", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision: want 400, got %d", rec.Code)
	}
}
