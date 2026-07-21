package server

import (
	"testing"

	"aireos.local/services/api/internal/run"
)

// TestToRunViewCommandGatedToProposed — D2 (ADR-0065 §6, T-091): a step's command
// rides the wire ONLY when the parent Run's status is 'proposed' (the plan-preview
// review surface). Every other status stays byte-unchanged from the pre-v0.9 shape —
// the operator-authored command is not echoed back once the operator has already
// seen/submitted it (template/operator-direct Runs), and a proposed Run that flips to
// pending/canceled after approve/deny no longer carries it either.
func TestToRunViewCommandGatedToProposed(t *testing.T) {
	step := run.Step{Index: 0, ResourceID: "res-a", Command: "echo hi", Status: run.StepPending}

	proposed := run.Run{ID: "run-1", Status: run.StatusProposed, Steps: []run.Step{step}}
	v := toRunView(proposed)
	if len(v.Steps) != 1 || v.Steps[0].Command != "echo hi" {
		t.Fatalf("a 'proposed' Run must echo the command, got %+v", v.Steps)
	}

	for _, status := range []string{run.StatusPending, run.StatusRunning, run.StatusCompleted, run.StatusFailed, run.StatusCanceled} {
		other := run.Run{ID: "run-1", Status: status, Steps: []run.Step{step}}
		v := toRunView(other)
		if len(v.Steps) != 1 || v.Steps[0].Command != "" {
			t.Fatalf("status %q must NOT echo the command, got %+v", status, v.Steps)
		}
	}
}
