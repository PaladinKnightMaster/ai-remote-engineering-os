package memory

import "testing"

// TestScopeForActor — the ADR-0064 §1/MEM-2 scope-derivation rule: a workstream-fired
// Run (actor = "workstream:<id>") accrues to its charter's scope; every other actor
// (an agent's delegated actor, or "" for an operator-direct Run) accrues to the Run
// itself. Pure — no PG.
func TestScopeForActor(t *testing.T) {
	cases := []struct {
		name         string
		actor, runID string
		wantType     string
		wantScopeID  string
	}{
		{"workstream actor scopes to the charter", "workstream:ws-1", "run-1", ScopeWorkstream, "ws-1"},
		{"agent actor scopes to the run", "agent:bot-1", "run-2", ScopeRun, "run-2"},
		{"empty actor (operator-direct) scopes to the run", "", "run-3", ScopeRun, "run-3"},
		{"an actor merely containing 'workstream:' mid-string does not match the prefix", "agent:workstream:decoy", "run-4", ScopeRun, "run-4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, gotID := scopeForActor(c.actor, c.runID)
			if gotType != c.wantType || gotID != c.wantScopeID {
				t.Fatalf("scopeForActor(%q, %q) = (%q, %q), want (%q, %q)",
					c.actor, c.runID, gotType, gotID, c.wantType, c.wantScopeID)
			}
		})
	}
}

// TestEventActor — mirrors internal/run's eventActor (ADR-0064 §8): the accruing Run's
// actor when set, else the operator.
func TestEventActor(t *testing.T) {
	if got := eventActor("operator:alice", "workstream:ws-1"); got != "workstream:ws-1" {
		t.Fatalf("eventActor with actor set = %q, want the actor", got)
	}
	if got := eventActor("operator:alice", ""); got != "operator:alice" {
		t.Fatalf("eventActor with empty actor = %q, want the operator", got)
	}
}
