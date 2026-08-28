package store

// rules_match.go is the T-149a rule-match query: the DB half of the
// evaluator in evaluate.go. Still called by nothing (unwired, same as
// evaluate.go — see its header comment).

import (
	"context"
	"fmt"
)

// MatchingRules returns only the enabled rules whose four columns each equal
// the tuple's value or the wildcard. The matching is pushed into SQL — four
// column comparisons, not a SELECT * filtered in Go — so the work stays
// bounded and can use policy_rules_enabled_idx (edge, action) WHERE enabled
// (0002_policy_rules.up.sql), which PERF-9 requires.
func (s *Store) MatchingRules(ctx context.Context, t Tuple) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+ruleCols+` FROM policy_rules
		 WHERE enabled
		   AND (subject = $1 OR subject = $5)
		   AND (edge    = $2 OR edge    = $5)
		   AND (action  = $3 OR action  = $5)
		   AND (target  = $4 OR target  = $5)`,
		t.Subject, t.Edge, t.Action, t.Target, Wildcard)
	if err != nil {
		return nil, fmt.Errorf("match rules: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := scanRule(rows, &r); err != nil {
			return nil, fmt.Errorf("scan matched rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// EvaluateTuple composes MatchingRules and Evaluate: the query bounds the
// candidate set to what could possibly match, then the pure function decides
// which one wins. Still called by nothing (T-150 is the ticket that wires
// this into decideHandler).
func (s *Store) EvaluateTuple(ctx context.Context, t Tuple) (outcome string, winner Rule, ok bool, err error) {
	rules, err := s.MatchingRules(ctx, t)
	if err != nil {
		return "", Rule{}, false, err
	}
	outcome, winner, ok = Evaluate(rules, t)
	return outcome, winner, ok, nil
}
