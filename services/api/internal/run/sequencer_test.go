package run

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type fakeSeqStore struct {
	run       Run
	stepCalls []string // "index:status"
	finished  string
}

func (f *fakeSeqStore) NextPending(context.Context, int) ([]string, error) {
	return []string{f.run.ID}, nil
}
func (f *fakeSeqStore) ClaimRun(context.Context, string) (bool, error) { return true, nil }
func (f *fakeSeqStore) Get(context.Context, string) (Run, error)       { return f.run, nil }
func (f *fakeSeqStore) SetStepResult(_ context.Context, _ string, index int, status, _ string) error {
	f.stepCalls = append(f.stepCalls, fmt.Sprintf("%d:%s", index, status))
	return nil
}
func (f *fakeSeqStore) FinishRun(_ context.Context, _, status string) error {
	f.finished = status
	return nil
}

type fakeDriver struct {
	results []StepResult
	errs    []error
	calls   []StepRequest
}

func (f *fakeDriver) RunStep(_ context.Context, req StepRequest) (StepResult, error) {
	i := len(f.calls)
	f.calls = append(f.calls, req)
	var res StepResult
	var err error
	if i < len(f.results) {
		res = f.results[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return res, err
}

func twoStepRun() Run {
	return Run{ID: "run-1", Operator: "operator:admin", Status: StatusRunning, Steps: []Step{
		{Index: 0, ResourceID: "r-a", Command: "echo a", Status: StepPending},
		{Index: 1, ResourceID: "r-b", Command: "echo b", Status: StepPending},
	}}
}

// All steps succeed → the Run completes; each step is dispatched with the operator as
// subject and the deterministic run:<id>:step:<index> idempotency key, in order.
func TestDriveRunAllStepsComplete(t *testing.T) {
	st := &fakeSeqStore{run: twoStepRun()}
	drv := &fakeDriver{results: []StepResult{{}, {}}}
	NewSequencer(st, drv).DriveRun(context.Background(), "run-1")

	if st.finished != StatusCompleted {
		t.Fatalf("run finished %q, want completed", st.finished)
	}
	if len(drv.calls) != 2 {
		t.Fatalf("driver calls = %d, want 2", len(drv.calls))
	}
	for i, c := range drv.calls {
		if c.Subject != "operator:admin" {
			t.Fatalf("step %d subject = %q, want operator:admin", i, c.Subject)
		}
		if want := fmt.Sprintf("run:run-1:step:%d", i); c.IdempotencyKey != want {
			t.Fatalf("step %d idem key = %q, want %q", i, c.IdempotencyKey, want)
		}
	}
}

// A governance refusal (IsError) on step 0 stops the Run (failed); step 1 is never
// dispatched.
func TestDriveRunStopsOnGovernanceRefusal(t *testing.T) {
	st := &fakeSeqStore{run: twoStepRun()}
	drv := &fakeDriver{results: []StepResult{{IsError: true, Text: "the operator denied this invocation"}}}
	NewSequencer(st, drv).DriveRun(context.Background(), "run-1")

	if st.finished != StatusFailed {
		t.Fatalf("run finished %q, want failed", st.finished)
	}
	if len(drv.calls) != 1 {
		t.Fatalf("driver calls = %d, want 1 (stop-on-failure)", len(drv.calls))
	}
}

// An infra/transport error stops the Run (failed) fail-closed; no further dispatch.
func TestDriveRunStopsOnInfraError(t *testing.T) {
	st := &fakeSeqStore{run: twoStepRun()}
	drv := &fakeDriver{errs: []error{errors.New("dial failed")}}
	NewSequencer(st, drv).DriveRun(context.Background(), "run-1")

	if st.finished != StatusFailed {
		t.Fatalf("run finished %q, want failed", st.finished)
	}
	if len(drv.calls) != 1 {
		t.Fatalf("driver calls = %d, want 1 (fail-closed stop)", len(drv.calls))
	}
}

// An already-completed step is skipped on (re)drive — never re-dispatched.
func TestDriveRunSkipsCompletedSteps(t *testing.T) {
	r := twoStepRun()
	r.Steps[0].Status = StepCompleted
	st := &fakeSeqStore{run: r}
	drv := &fakeDriver{results: []StepResult{{}}}
	NewSequencer(st, drv).DriveRun(context.Background(), "run-1")

	if st.finished != StatusCompleted {
		t.Fatalf("run finished %q, want completed", st.finished)
	}
	if len(drv.calls) != 1 || drv.calls[0].ResourceID != "r-b" {
		t.Fatalf("want only the pending step (r-b) dispatched, got %+v", drv.calls)
	}
}
