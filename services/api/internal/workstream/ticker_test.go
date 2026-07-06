package workstream

import (
	"context"
	"fmt"
	"testing"
	"time"

	"aireos.local/services/api/internal/run"
)

// fakeTickStore satisfies tickerStore; captures self-retires.
type fakeTickStore struct {
	ws      Workstream
	getErr  error
	retired []string
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
