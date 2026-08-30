package store

// evaluate.go is the T-149a policy evaluator (ADR-0074 §D1/§D2): a PURE
// function over an in-memory rule set, with no DB and no context.
// decideHandler (services/policy-service/internal/server/server.go) has
// called this via Store.EvaluateTuple since T-150 wired it into the decide
// path and started chaining the resulting event.
//
// Kept in package store (not split into internal/policy/) because it operates
// directly on the store's own Rule type and constants (Wildcard,
// RuleOutcome*) with no need to export a second copy of that vocabulary into
// a new package boundary — the store package is already where rules.go lives,
// and this is the same concern (what a rule means), not a new one.

// Tuple is the (subject, edge, action, target) an invocation presents to the
// decision point — the exact tuple ADR-0054 §1 already specified.
type Tuple struct {
	Subject string
	Edge    string
	Action  string
	Target  string
}

// severityRank orders rule outcomes for precedence (ADR-0074 §D2): deny >
// requires_approval > allow. Severity beats specificity — there is
// deliberately no tiebreak on how specific a rule's fields are; the ADR
// rejected that as unreasonable to reason about under time pressure.
var severityRank = map[string]int{
	RuleOutcomeDeny:             3,
	RuleOutcomeRequiresApproval: 2,
	RuleOutcomeAllow:            1,
}

// fieldMatches reports whether one rule column matches one tuple value: an
// exact string match, or the rule column holding the literal wildcard.
func fieldMatches(ruleValue, tupleValue string) bool {
	return ruleValue == Wildcard || ruleValue == tupleValue
}

// ruleMatches reports whether a single rule matches a tuple: disabled rules
// never match, and all four fields must match independently (each is
// consulted — a rule that only checked three of the four fields would be
// wrong, which is exactly what evaluate_test.go's per-field non-match cases
// exist to catch).
func ruleMatches(r Rule, t Tuple) bool {
	return r.Enabled &&
		fieldMatches(r.Subject, t.Subject) &&
		fieldMatches(r.Edge, t.Edge) &&
		fieldMatches(r.Action, t.Action) &&
		fieldMatches(r.Target, t.Target)
}

// winnerBeats reports whether candidate should replace current as the
// effective winner. Higher severity always wins, full stop — there is no
// specificity tiebreak (ADR-0074 §D2, CON-3). Within the same severity, the
// earliest-created rule wins, and a CreatedAt tie breaks on id, so the
// returned winner is stable no matter what order the caller's rule slice
// presents matches in.
func winnerBeats(candidate, current Rule) bool {
	cr, kr := severityRank[candidate.Outcome], severityRank[current.Outcome]
	if cr != kr {
		return cr > kr
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.Before(current.CreatedAt)
	}
	return candidate.ID < current.ID
}

// Evaluate returns the effective outcome and the winning rule for a tuple
// against a rule set, or ok=false when no enabled rule matches. ok=false is
// NOT the same signal as a matched allow — callers (T-150) must be able to
// tell "no rule fired, fall back to today's behavior" (ADR-0074 §D3) apart
// from "a rule fired and it said allow".
func Evaluate(rules []Rule, t Tuple) (outcome string, winner Rule, ok bool) {
	for _, r := range rules {
		if !ruleMatches(r, t) {
			continue
		}
		if !ok || winnerBeats(r, winner) {
			winner = r
			ok = true
		}
	}
	if !ok {
		return "", Rule{}, false
	}
	return winner.Outcome, winner, true
}
