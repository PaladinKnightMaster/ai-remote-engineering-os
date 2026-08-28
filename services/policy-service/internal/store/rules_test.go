// Unit tests for policy_rules Go-side validation — no DB (ADR-0074, T-148).
// Covers the invariants the store must reject before ever issuing a query:
// empty fields (wildcard must be the literal "*", not "") and an outcome
// outside allow/requires_approval/deny.
package store

import "testing"

func TestValidateRuleInputAccepts(t *testing.T) {
	cases := []struct {
		name                                   string
		subject, edge, action, target, outcome string
	}{
		{"exact tuple, allow", "operator:admin", "agent-remote-session", "session.start", "resource:r-1", RuleOutcomeAllow},
		{"exact tuple, requires_approval", "agent:composer", "agent-remote-session", "remote.exec", "resource:r-2", RuleOutcomeRequiresApproval},
		{"exact tuple, deny", "agent:composer", "agent-remote-session", "remote.exec", "resource:r-3", RuleOutcomeDeny},
		{"all wildcards", Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny},
		{"mixed wildcard", "agent:composer", Wildcard, "remote.file.write", Wildcard, RuleOutcomeRequiresApproval},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateRuleInput(tc.subject, tc.edge, tc.action, tc.target, tc.outcome); err != nil {
				t.Fatalf("want valid, got %v", err)
			}
		})
	}
}

func TestValidateRuleInputRejectsEmptyFields(t *testing.T) {
	base := []string{"operator:admin", "agent-remote-session", "session.start", "resource:r-1"}
	for i, label := range []string{"subject", "edge", "action", "target"} {
		fields := append([]string(nil), base...)
		fields[i] = ""
		t.Run("empty "+label, func(t *testing.T) {
			err := validateRuleInput(fields[0], fields[1], fields[2], fields[3], RuleOutcomeAllow)
			var invalid ErrInvalidRule
			if err == nil {
				t.Fatalf("want ErrInvalidRule for empty %s, got nil", label)
			}
			if !isErrInvalidRule(err, &invalid) {
				t.Fatalf("want ErrInvalidRule for empty %s, got %v (%T)", label, err, err)
			}
		})
	}
}

func TestValidateRuleInputRejectsBadOutcome(t *testing.T) {
	cases := []string{"", "ALLOW", "allowed", "requiresApproval", "block"}
	for _, outcome := range cases {
		t.Run("outcome="+outcome, func(t *testing.T) {
			err := validateRuleInput("operator:admin", "agent-remote-session", "session.start", "resource:r-1", outcome)
			var invalid ErrInvalidRule
			if !isErrInvalidRule(err, &invalid) {
				t.Fatalf("want ErrInvalidRule for outcome %q, got %v (%T)", outcome, err, err)
			}
		})
	}
}

// isErrInvalidRule reports whether err is an ErrInvalidRule, copying it into
// target when so (a type switch is enough — ErrInvalidRule is a plain struct,
// never wrapped).
func isErrInvalidRule(err error, target *ErrInvalidRule) bool {
	if e, ok := err.(ErrInvalidRule); ok {
		*target = e
		return true
	}
	return false
}
