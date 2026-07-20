package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/run"
)

// The plan-vs-grant validator itself moved to internal/run at T-072 (SEC-30a) and is
// unit-tested there (launch_test.go, typed kinds). This file tests the handler's HTTP
// adaptation of the shared core: the typed Refusal → wire-status mapping is the
// behavior-preserving contract (out-of-grant + grant-dead → 403, bounds → 429,
// re-attach → 200, created → 201) with the refusal reason carried verbatim.

// gwOnlyVerifier authenticates every request as the gateway's service token (the
// route-scoped verifier's happy path).
type gwOnlyVerifier struct{}

func (gwOnlyVerifier) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "mcp-gateway", Edge: "service-service"}, nil
}

// fakeLaunchRunStore satisfies runStore; LaunchBounded is exercised here, and
// CancelRunsForGrant records the grant ids swept (the workstream retire-cascade
// assertion, T-074).
type fakeLaunchRunStore struct {
	launched       run.Run
	reattached     bool
	refusal        *run.Refusal
	canceledGrants []string
}

func (f *fakeLaunchRunStore) LaunchBounded(_ context.Context, _ run.GrantSource, _, _, _ string, _ []run.StepInput) (run.Run, bool, *run.Refusal, error) {
	return f.launched, f.reattached, f.refusal, nil
}
func (f *fakeLaunchRunStore) Create(_ context.Context, _ run.Input) (run.Run, error) {
	return run.Run{}, nil
}
func (f *fakeLaunchRunStore) Get(_ context.Context, _ string) (run.Run, error) {
	return run.Run{}, run.ErrNotFound
}
func (f *fakeLaunchRunStore) CountActiveForOperator(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (f *fakeLaunchRunStore) ListForOperator(_ context.Context, _, _ string, _ int) ([]run.Run, string, error) {
	return nil, "", nil
}
func (f *fakeLaunchRunStore) CancelRun(_ context.Context, _ string) (bool, error) { return false, nil }
func (f *fakeLaunchRunStore) CancelRunsForGrant(_ context.Context, grantID string) (int, error) {
	f.canceledGrants = append(f.canceledGrants, grantID)
	return 1, nil
}
func (f *fakeLaunchRunStore) ApproveProposed(_ context.Context, _ run.GrantSource, _ string) (run.Run, bool, *run.Refusal, error) {
	return run.Run{}, false, nil, nil
}
func (f *fakeLaunchRunStore) DenyProposed(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// LaunchBoundedProposed (F-c, T-090) shares the same fixture fields as LaunchBounded —
// SubmitPlan's tests (adaptive_internal_test.go) drive it identically.
func (f *fakeLaunchRunStore) LaunchBoundedProposed(_ context.Context, _ run.GrantSource, _, _, _ string, _ []run.StepInput) (run.Run, bool, *run.Refusal, error) {
	return f.launched, f.reattached, f.refusal, nil
}

// fakeGrantStore satisfies grantStore; the handler passes it through to the core as the
// GrantSource (unused by the fake core).
type fakeGrantStore struct{}

func (fakeGrantStore) Create(_ context.Context, _ grant.Input) (grant.Grant, error) {
	return grant.Grant{}, nil
}
func (fakeGrantStore) Get(_ context.Context, _ string) (grant.Grant, error) {
	return grant.Grant{}, grant.ErrNotFound
}
func (fakeGrantStore) ListForOperator(_ context.Context, _, _ string, _ int) ([]grant.Grant, string, error) {
	return nil, "", nil
}
func (fakeGrantStore) Revoke(_ context.Context, _ string) (bool, error) { return false, nil }
func (fakeGrantStore) AuditLaunchDenied(_ context.Context, _, _ string) error {
	return nil
}

func launchThrough(t *testing.T, rs *fakeLaunchRunStore, body string) *httptest.ResponseRecorder {
	t.Helper()
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{},
		"", "", WithGrantStore(fakeGrantStore{}), WithRunStore(rs), WithGatewayVerifier(gwOnlyVerifier{}))
	r := httptest.NewRequest(http.MethodPost, "/v1/internal/delegated-runs", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer x")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

const validLaunchBody = `{"agentSubject":"agent:x","grantId":"g-1","idempotencyKey":"k-1",
	"steps":[{"resourceId":"res-a","command":"uptime"}]}`

// TestLaunchDelegatedRunRefusalMapping — the typed Refusal maps to the UNCHANGED wire
// statuses, with the reason (the e2e's asserted contract string) carried verbatim.
func TestLaunchDelegatedRunRefusalMapping(t *testing.T) {
	cases := []struct {
		name       string
		refusal    *run.Refusal
		wantStatus int
	}{
		{"out-of-grant → 403", &run.Refusal{Kind: run.RefusalOutOfGrant, Reason: "a step targets a resource outside the grant"}, http.StatusForbidden},
		{"grant-dead → 403", &run.Refusal{Kind: run.RefusalGrantDead, Reason: "the grant has expired"}, http.StatusForbidden},
		{"bounds → 429", &run.Refusal{Kind: run.RefusalBounds, Reason: "the grant's step budget is exhausted"}, http.StatusTooManyRequests},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := launchThrough(t, &fakeLaunchRunStore{refusal: c.refusal}, validLaunchBody)
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), c.refusal.Reason) {
				t.Fatalf("refusal reason %q not carried in body %s", c.refusal.Reason, rec.Body.String())
			}
		})
	}
}

// TestLaunchDelegatedRunCreatedAndReattach — a fresh launch answers 201; an idempotent
// re-attach answers 200 with the original Run.
func TestLaunchDelegatedRunCreatedAndReattach(t *testing.T) {
	created := run.Run{ID: "run-1", Status: run.StatusPending}
	if rec := launchThrough(t, &fakeLaunchRunStore{launched: created}, validLaunchBody); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "run-1") {
		t.Fatalf("created: status=%d body=%s, want 201 + run-1", rec.Code, rec.Body.String())
	}
	if rec := launchThrough(t, &fakeLaunchRunStore{launched: created, reattached: true}, validLaunchBody); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "run-1") {
		t.Fatalf("re-attach: status=%d body=%s, want 200 + run-1", rec.Code, rec.Body.String())
	}
}

// TestLaunchDelegatedRun400 — structural 400s stay handler-side, before the core.
func TestLaunchDelegatedRun400(t *testing.T) {
	for name, body := range map[string]string{
		"no agent":        `{"grantId":"g-1","steps":[{"resourceId":"r","command":"c"}]}`,
		"no steps":        `{"agentSubject":"a","grantId":"g-1","steps":[]}`,
		"incomplete step": `{"agentSubject":"a","grantId":"g-1","steps":[{"resourceId":"r"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := launchThrough(t, &fakeLaunchRunStore{}, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}
