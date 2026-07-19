package workstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"aireos.local/services/api/internal/grant"
	"aireos.local/services/api/internal/run"
)

// fakeTickStore satisfies tickerStore; captures self-retires + adaptive plan-request
// opens (T-089: openCalls/opened* capture what fireAdaptive hands OpenPlanRequest).
type fakeTickStore struct {
	ws      Workstream
	getErr  error
	retired []string

	openCalls          int
	openedWorkstreamID string
	openedLaunchKey    string
	openedExpiresAt    time.Time
	openReattached     bool
	openErr            error
}

func (f *fakeTickStore) DueWorkstreams(_ context.Context, _ int) ([]DueRef, error) {
	return nil, nil
}
func (f *fakeTickStore) ClaimTick(_ context.Context, _ string, _ time.Time) (bool, error) {
	return true, nil
}
func (f *fakeTickStore) Get(_ context.Context, _ string) (Workstream, error) {
	return f.ws, f.getErr
}
func (f *fakeTickStore) SelfRetire(_ context.Context, id string) (bool, error) {
	f.retired = append(f.retired, id)
	return true, nil
}
func (f *fakeTickStore) OpenPlanRequest(_ context.Context, workstreamID, launchKey string, expiresAt time.Time) (PlanRequest, bool, error) {
	f.openCalls++
	f.openedWorkstreamID, f.openedLaunchKey, f.openedExpiresAt = workstreamID, launchKey, expiresAt
	if f.openErr != nil {
		return PlanRequest{}, false, f.openErr
	}
	return PlanRequest{ID: "pr-1", WorkstreamID: workstreamID, LaunchKey: launchKey, Status: PlanRequestOpen, ExpiresAt: expiresAt}, f.openReattached, nil
}
func (f *fakeTickStore) ExpirePlanRequestsDue(_ context.Context, _ int) (int, error) {
	return 0, nil
}

// fakeGrantSource satisfies run.GrantSource for the ticker's adaptive-branch direct
// grant-liveness check (grantDead, T-089) — independent of fakeLauncher (the template
// branch's own dependency, which never touches this).
type fakeGrantSource struct {
	grant grant.Grant
	err   error
}

func (f fakeGrantSource) Get(_ context.Context, _ string) (grant.Grant, error) {
	return f.grant, f.err
}
func (f fakeGrantSource) AuditLaunchDenied(_ context.Context, _, _ string) error { return nil }

// fakeLauncher captures what the ticker hands the shared core and returns a scripted
// outcome.
type fakeLauncher struct {
	gotSubject, gotGrant, gotKey string
	gotSteps                     []run.StepInput
	refusal                      *run.Refusal
	err                          error
	calls                        int
}

func (f *fakeLauncher) LaunchBounded(_ context.Context, _ run.GrantSource, agentSubject, grantID, launchKey string, steps []run.StepInput) (run.Run, bool, *run.Refusal, error) {
	f.calls++
	f.gotSubject, f.gotGrant, f.gotKey, f.gotSteps = agentSubject, grantID, launchKey, steps
	if f.err != nil || f.refusal != nil {
		return run.Run{}, false, f.refusal, f.err
	}
	return run.Run{ID: "run-fired", Status: run.StatusPending}, false, nil, nil
}

func tickWorkstream() Workstream {
	return Workstream{
		ID: "ws-1", Principal: "operator:o", GrantID: "grant-1", IntervalSeconds: 300,
		Status: StatusActive,
		Steps: []Step{
			{Index: 0, ResourceID: "res-a", Command: "uptime"},
			{Index: 1, ResourceID: "res-b", Command: "df -h"},
		},
	}
}

// adaptiveTickWorkstream is tickWorkstream's KindAdaptive twin (D4, T-089): zero
// template steps, a named composer, otherwise identical identity/schedule.
func adaptiveTickWorkstream() Workstream {
	return Workstream{
		ID: "ws-adapt", Principal: "operator:o", GrantID: "grant-1", IntervalSeconds: 300,
		Status: StatusActive, Kind: KindAdaptive, Composer: "agent:composer-1",
	}
}

// TestTickerFireBindsTheTick — a fired tick hands the shared core the workstream actor,
// the charter's grant, the fire-time-deterministic launch key, and the instantiated
// template, in order (WS-3: the ticker enforces nothing itself).
func TestTickerFireBindsTheTick(t *testing.T) {
	fs := &fakeTickStore{ws: tickWorkstream()}
	fl := &fakeLauncher{}
	tk := &Ticker{store: fs, launcher: fl}
	fireAt := time.Unix(1770000000, 0)

	tk.fire(context.Background(), DueRef{ID: "ws-1", NextFireAt: fireAt})

	if fl.calls != 1 {
		t.Fatalf("launcher calls = %d, want 1", fl.calls)
	}
	if fl.gotSubject != "workstream:ws-1" || fl.gotGrant != "grant-1" {
		t.Fatalf("identity binding wrong: subject=%q grant=%q", fl.gotSubject, fl.gotGrant)
	}
	if want := fmt.Sprintf("workstream:ws-1:tick:%d", fireAt.Unix()); fl.gotKey != want {
		t.Fatalf("launch key = %q, want %q", fl.gotKey, want)
	}
	if len(fl.gotSteps) != 2 || fl.gotSteps[0].Command != "uptime" || fl.gotSteps[1].ResourceID != "res-b" {
		t.Fatalf("template instantiation wrong: %+v", fl.gotSteps)
	}
	if len(fs.retired) != 0 {
		t.Fatal("a successful fire must not retire")
	}
}

// TestTickerRefusalMapping — the typed kinds drive the ticker's reaction (the T-072
// contract): GrantDead self-retires (F1); Bounds and OutOfGrant and infra errors do NOT
// (never wedge, never retire on transient).
func TestTickerRefusalMapping(t *testing.T) {
	cases := []struct {
		name       string
		refusal    *run.Refusal
		err        error
		wantRetire bool
	}{
		{"grant dead → self-retire", &run.Refusal{Kind: run.RefusalGrantDead, Reason: "the grant has expired"}, nil, true},
		{"bounds → re-fire only", &run.Refusal{Kind: run.RefusalBounds, Reason: "the grant's step budget is exhausted"}, nil, false},
		{"out-of-grant → log only", &run.Refusal{Kind: run.RefusalOutOfGrant, Reason: "a step targets a resource outside the grant"}, nil, false},
		{"infra error → re-fire only", nil, fmt.Errorf("pg down"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &fakeTickStore{ws: tickWorkstream()}
			fl := &fakeLauncher{refusal: c.refusal, err: c.err}
			tk := &Ticker{store: fs, launcher: fl}
			tk.fire(context.Background(), DueRef{ID: "ws-1", NextFireAt: time.Now()})
			if got := len(fs.retired) == 1; got != c.wantRetire {
				t.Fatalf("retired=%v, want %v (retired list: %v)", got, c.wantRetire, fs.retired)
			}
		})
	}
}

// TestTickerFireAdaptiveOpensPlanRequest — D4/R1 (T-089): an adaptive tick with a live
// grant never touches the shared launch core; it opens a plan request instead, with
// expiresAt = the CLAIMED fire time (d.NextFireAt) + the charter's interval (R1) — NOT
// wallclock now+interval.
func TestTickerFireAdaptiveOpensPlanRequest(t *testing.T) {
	fs := &fakeTickStore{ws: adaptiveTickWorkstream()}
	fl := &fakeLauncher{}
	fg := fakeGrantSource{grant: grant.Grant{Status: grant.StatusActive, ExpiresAt: time.Now().Add(time.Hour)}}
	tk := &Ticker{store: fs, launcher: fl, grants: fg}
	fireAt := time.Unix(1770000000, 0)

	tk.fire(context.Background(), DueRef{ID: "ws-adapt", NextFireAt: fireAt})

	if fl.calls != 0 {
		t.Fatalf("adaptive fire must never touch the shared launch core, calls=%d", fl.calls)
	}
	if fs.openCalls != 1 {
		t.Fatalf("OpenPlanRequest calls = %d, want 1", fs.openCalls)
	}
	if fs.openedWorkstreamID != "ws-adapt" {
		t.Fatalf("workstream id = %q, want ws-adapt", fs.openedWorkstreamID)
	}
	if want := fmt.Sprintf("workstream:ws-adapt:tick:%d", fireAt.Unix()); fs.openedLaunchKey != want {
		t.Fatalf("launch key = %q, want %q", fs.openedLaunchKey, want)
	}
	wantExpiry := fireAt.Add(300 * time.Second)
	if !fs.openedExpiresAt.Equal(wantExpiry) {
		t.Fatalf("expiresAt = %v, want %v (R1: claimed fire time + interval)", fs.openedExpiresAt, wantExpiry)
	}
	if len(fs.retired) != 0 {
		t.Fatal("a live-grant adaptive fire must not retire")
	}
}

// TestTickerFireAdaptiveDeadGrantSelfRetires — D4/F1 (T-089): a dead grant (missing,
// inactive, or expired) self-retires the charter WITHOUT ever opening a plan request —
// the same F1 trigger the template branch's RefusalGrantDead case uses, reached here by
// a DIRECT liveness check (grantDead, the same 3-case vocabulary run.ApproveProposed
// uses) since the adaptive branch never calls the shared launch core.
func TestTickerFireAdaptiveDeadGrantSelfRetires(t *testing.T) {
	cases := []struct {
		name string
		fg   fakeGrantSource
	}{
		{"grant missing", fakeGrantSource{err: grant.ErrNotFound}},
		{"grant not active", fakeGrantSource{grant: grant.Grant{Status: grant.StatusRevoked, ExpiresAt: time.Now().Add(time.Hour)}}},
		{"grant expired", fakeGrantSource{grant: grant.Grant{Status: grant.StatusActive, ExpiresAt: time.Now().Add(-time.Minute)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &fakeTickStore{ws: adaptiveTickWorkstream()}
			fl := &fakeLauncher{}
			tk := &Ticker{store: fs, launcher: fl, grants: c.fg}
			tk.fire(context.Background(), DueRef{ID: "ws-adapt", NextFireAt: time.Now()})

			if len(fs.retired) != 1 || fs.retired[0] != "ws-adapt" {
				t.Fatalf("retired = %v, want exactly [ws-adapt]", fs.retired)
			}
			if fs.openCalls != 0 {
				t.Fatalf("OpenPlanRequest calls = %d, want 0 (a dead grant must never open)", fs.openCalls)
			}
			if fl.calls != 0 {
				t.Fatal("adaptive fire must never touch the shared launch core")
			}
		})
	}
}
