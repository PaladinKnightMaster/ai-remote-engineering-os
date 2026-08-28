package store

// evaluate_test.go — pure unit tests for the T-149a evaluator (evaluate.go).
// NO DB: every case builds Rule values by hand and calls Evaluate directly.
// The integration leg (evaluate_integration_test.go, //go:build integration)
// separately proves MatchingRules (SQL) agrees with Evaluate (Go) on a live
// fixture set.

import (
	"testing"
	"time"
)

// baseTuple is the tuple every test matches (or deliberately fails to match)
// against.
var baseTuple = Tuple{
	Subject: "operator:alice",
	Edge:    "agent-remote-session",
	Action:  "remote.exec",
	Target:  "resource:r-1",
}

func mkRule(id string, createdAt time.Time, subject, edge, action, target, outcome string, enabled bool) Rule {
	return Rule{
		ID: id, Label: id, Subject: subject, Edge: edge, Action: action, Target: target,
		Outcome: outcome, Enabled: enabled, CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

// t0 gives every test a distinct, ordered CreatedAt without relying on
// wall-clock timing.
func t0(offsetSeconds int) time.Time {
	return time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC).Add(time.Duration(offsetSeconds) * time.Second)
}

func TestEvaluateExactMatchAllFields(t *testing.T) {
	r := mkRule("r1", t0(0), baseTuple.Subject, baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)
	outcome, winner, ok := Evaluate([]Rule{r}, baseTuple)
	if !ok || outcome != RuleOutcomeAllow || winner.ID != "r1" {
		t.Fatalf("want ok=true outcome=allow winner=r1, got outcome=%q winner=%+v ok=%v", outcome, winner, ok)
	}
}

// TestEvaluateEachFieldWildcardedIndependently covers each field wildcarded
// on its own, and all four wildcarded together.
func TestEvaluateEachFieldWildcardedIndependently(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
	}{
		{"subject wildcard", mkRule("r-subj", t0(0), Wildcard, baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)},
		{"edge wildcard", mkRule("r-edge", t0(0), baseTuple.Subject, Wildcard, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)},
		{"action wildcard", mkRule("r-action", t0(0), baseTuple.Subject, baseTuple.Edge, Wildcard, baseTuple.Target, RuleOutcomeAllow, true)},
		{"target wildcard", mkRule("r-target", t0(0), baseTuple.Subject, baseTuple.Edge, baseTuple.Action, Wildcard, RuleOutcomeAllow, true)},
		{"all four wildcard", mkRule("r-all", t0(0), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeAllow, true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcome, winner, ok := Evaluate([]Rule{c.rule}, baseTuple)
			if !ok || outcome != RuleOutcomeAllow || winner.ID != c.rule.ID {
				t.Fatalf("%s: want a match, got outcome=%q ok=%v winner=%+v", c.name, outcome, ok, winner)
			}
		})
	}
}

// TestEvaluateNonMatchOnEachFieldIndependently proves each field is actually
// consulted: a matcher that silently ignored one field (e.g. target, the way
// the pre-ADR-0074 decision point ignored it) would pass a careless test
// suite but must fail here.
func TestEvaluateNonMatchOnEachFieldIndependently(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
	}{
		{"subject differs", mkRule("r1", t0(0), "operator:bob", baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)},
		{"edge differs", mkRule("r2", t0(0), baseTuple.Subject, "agent-workspace", baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)},
		{"action differs", mkRule("r3", t0(0), baseTuple.Subject, baseTuple.Edge, "remote.file.read", baseTuple.Target, RuleOutcomeAllow, true)},
		{"target differs", mkRule("r4", t0(0), baseTuple.Subject, baseTuple.Edge, baseTuple.Action, "resource:r-2", RuleOutcomeAllow, true)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, ok := Evaluate([]Rule{c.rule}, baseTuple)
			if ok {
				t.Fatalf("%s: rule must NOT match — a matcher that ignores this field would wrongly pass", c.name)
			}
		})
	}
}

func TestEvaluateDisabledRuleNeverMatches(t *testing.T) {
	r := mkRule("r1", t0(0), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, false)
	_, _, ok := Evaluate([]Rule{r}, baseTuple)
	if ok {
		t.Fatalf("disabled rule matched — enabled flag not consulted")
	}
}

// TestEvaluateWildcardDenyBeatsSpecificAllow is the CON-3 headline case: a
// wildcard deny beats a fully-specific allow. Severity beats specificity —
// there is no tiebreak on how specific a rule's fields are. Run both input
// orderings so the result cannot depend on slice order.
func TestEvaluateWildcardDenyBeatsSpecificAllow(t *testing.T) {
	specificAllow := mkRule("specific-allow", t0(0), baseTuple.Subject, baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)
	wildcardDeny := mkRule("wildcard-deny", t0(1), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)

	for _, order := range [][]Rule{
		{specificAllow, wildcardDeny},
		{wildcardDeny, specificAllow},
	} {
		outcome, winner, ok := Evaluate(order, baseTuple)
		if !ok {
			t.Fatalf("want a match, got none")
		}
		if outcome != RuleOutcomeDeny {
			t.Fatalf("CON-3: want deny to beat a fully-specific allow, got outcome=%q", outcome)
		}
		if winner.ID != wildcardDeny.ID {
			t.Fatalf("CON-3: want the wildcard deny rule named as winner, got %+v", winner)
		}
	}
}

func TestEvaluateRequiresApprovalBeatsAllow(t *testing.T) {
	allow := mkRule("allow", t0(0), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeAllow, true)
	approval := mkRule("approval", t0(1), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeRequiresApproval, true)
	outcome, winner, ok := Evaluate([]Rule{allow, approval}, baseTuple)
	if !ok || outcome != RuleOutcomeRequiresApproval || winner.ID != "approval" {
		t.Fatalf("want requires_approval to beat allow, got outcome=%q winner=%+v ok=%v", outcome, winner, ok)
	}
}

func TestEvaluateDenyBeatsRequiresApproval(t *testing.T) {
	approval := mkRule("approval", t0(0), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeRequiresApproval, true)
	deny := mkRule("deny", t0(1), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)
	outcome, winner, ok := Evaluate([]Rule{approval, deny}, baseTuple)
	if !ok || outcome != RuleOutcomeDeny || winner.ID != "deny" {
		t.Fatalf("want deny to beat requires_approval, got outcome=%q winner=%+v ok=%v", outcome, winner, ok)
	}
}

// TestEvaluateNoMatchingRuleIsNotOk proves ok=false is distinguishable from a
// matched allow — the T-150 fallback (ADR-0074 §D3) hinges on telling these
// two signals apart; they must never collapse into each other.
func TestEvaluateNoMatchingRuleIsNotOk(t *testing.T) {
	nonMatching := mkRule("r1", t0(0), "operator:someone-else", baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)
	outcome, winner, ok := Evaluate([]Rule{nonMatching}, baseTuple)
	if ok {
		t.Fatalf("want ok=false for no matching rule, got ok=true outcome=%q winner=%+v", outcome, winner)
	}
	if outcome != "" || winner != (Rule{}) {
		t.Fatalf("want zero-value outcome/winner on no match, got outcome=%q winner=%+v", outcome, winner)
	}

	matchingAllow := mkRule("r2", t0(0), baseTuple.Subject, baseTuple.Edge, baseTuple.Action, baseTuple.Target, RuleOutcomeAllow, true)
	outcome2, _, ok2 := Evaluate([]Rule{matchingAllow}, baseTuple)
	if !ok2 || outcome2 != RuleOutcomeAllow {
		t.Fatalf("sanity: a matched allow should be ok=true outcome=allow, got outcome=%q ok=%v", outcome2, ok2)
	}
	if ok == ok2 {
		t.Fatalf("no-match and matched-allow must be distinguishable via ok; both got ok=%v", ok)
	}
}

// TestEvaluateNoRulesAtAllIsNotOk covers the degenerate empty-slice input —
// ADR-0074 §D3's "empty table reproduces v0.14.0 semantics exactly" starts
// here.
func TestEvaluateNoRulesAtAllIsNotOk(t *testing.T) {
	_, _, ok := Evaluate(nil, baseTuple)
	if ok {
		t.Fatalf("want ok=false for an empty rule set")
	}
}

// TestEvaluateDeterministicWinnerAcrossRepeatedCallsAndOrder: two same-
// severity matches must return the same, stable winner every call, and
// regardless of the order the caller's slice presents them in.
func TestEvaluateDeterministicWinnerAcrossRepeatedCallsAndOrder(t *testing.T) {
	earlier := mkRule("rule-a", t0(5), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)
	later := mkRule("rule-b", t0(10), Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)

	for i := 0; i < 5; i++ {
		outcome, winner, ok := Evaluate([]Rule{earlier, later}, baseTuple)
		if !ok || outcome != RuleOutcomeDeny || winner.ID != "rule-a" {
			t.Fatalf("call %d, [earlier,later] order: want rule-a to win, got %+v", i, winner)
		}
	}
	for i := 0; i < 5; i++ {
		outcome, winner, ok := Evaluate([]Rule{later, earlier}, baseTuple)
		if !ok || outcome != RuleOutcomeDeny || winner.ID != "rule-a" {
			t.Fatalf("call %d, [later,earlier] order: want rule-a to win regardless of input order, got %+v", i, winner)
		}
	}
}

// TestEvaluateDeterministicWinnerTiesOnCreatedAtBreakOnID: when CreatedAt
// itself ties, the winner still must be stable — id is the final tiebreak.
func TestEvaluateDeterministicWinnerTiesOnCreatedAtBreakOnID(t *testing.T) {
	same := t0(0)
	a := mkRule("aaa", same, Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)
	b := mkRule("bbb", same, Wildcard, Wildcard, Wildcard, Wildcard, RuleOutcomeDeny, true)

	for i := 0; i < 3; i++ {
		outcome, winner, ok := Evaluate([]Rule{b, a}, baseTuple)
		if !ok || outcome != RuleOutcomeDeny || winner.ID != "aaa" {
			t.Fatalf("call %d: want lexicographically-earlier id 'aaa' to win a CreatedAt tie, got %+v", i, winner)
		}
	}
}
