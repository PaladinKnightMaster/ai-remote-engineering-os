//go:build integration

// Integration tests for T-149a's rule-match query against a real Postgres
// with 0002_policy_rules applied. Reuses testStore(t) (store_integration_
// test.go) and distinctSubject(t) (rules_integration_test.go) — same harness:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export POLICY_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/policy-service migrate up
//	go test -tags integration -v ./services/policy-service/internal/store/...
//
// The point of this file: prove MatchingRules (SQL) and Evaluate (pure Go)
// agree on the same fixture set. They must not drift from each other.
package store

import (
	"context"
	"testing"
)

// TestMatchingRulesAgreesWithEvaluateOnFixtureSet creates a known fixture set
// covering an exact match, a wildcard-subject match, an all-wildcard match,
// and two rules that must NOT match (different subject, different action),
// then checks that MatchingRules' SQL-computed set is exactly the set the
// pure Go ruleMatches would pick out of the same fixtures, and that Evaluate
// gives the identical effective decision whichever set it is fed.
func TestMatchingRulesAgreesWithEvaluateOnFixtureSet(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)
	const edge, action, target = "agent-remote-session", "remote.exec", "resource:t149a-fixture"

	// The wildcard-subject and all-wildcard fixtures below are not keyed by
	// distinctSubject, so a stale row from a previously-interrupted run of
	// this exact test could collide with the UNIQUE tuple constraint —
	// pre-clean both exact tuples first, the same idempotency precaution
	// rules_integration_test.go's wildcard round-trip test takes.
	if _, err := pool.Exec(ctx, `
		DELETE FROM policy_rules WHERE
		    (subject = $1 AND edge = $2 AND action = $3 AND target = $4)
		 OR (subject = '*' AND edge = '*' AND action = '*' AND target = '*')`,
		Wildcard, edge, action, target); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}

	tuple := Tuple{Subject: subject, Edge: edge, Action: action, Target: target}

	fixtures := []CreateRuleInput{
		{Label: "exact allow", Subject: subject, Edge: edge, Action: action, Target: target, Outcome: RuleOutcomeAllow},
		{Label: "wildcard subject requires_approval", Subject: Wildcard, Edge: edge, Action: action, Target: target, Outcome: RuleOutcomeRequiresApproval},
		{Label: "all wildcard deny", Subject: Wildcard, Edge: Wildcard, Action: Wildcard, Target: Wildcard, Outcome: RuleOutcomeDeny},
		{Label: "non-matching subject", Subject: distinctSubject(t), Edge: edge, Action: action, Target: target, Outcome: RuleOutcomeAllow},
		{Label: "non-matching action", Subject: subject, Edge: edge, Action: "remote.file.read", Target: target, Outcome: RuleOutcomeAllow},
	}
	var created []Rule
	for _, f := range fixtures {
		r, err := s.CreateRule(ctx, f)
		if err != nil {
			t.Fatalf("create fixture %q: %v", f.Label, err)
		}
		created = append(created, r)
	}
	t.Cleanup(func() {
		for _, r := range created {
			_ = s.DeleteRule(ctx, r.ID)
		}
	})

	matched, err := s.MatchingRules(ctx, tuple)
	if err != nil {
		t.Fatalf("MatchingRules: %v", err)
	}

	// Ground truth computed by the pure Go matcher over the exact fixture set
	// we created — not the whole table, which would couple this test to rows
	// left by unrelated tests.
	expected := map[string]bool{}
	for _, r := range created {
		if ruleMatches(r, tuple) {
			expected[r.ID] = true
		}
	}
	if len(expected) != 3 {
		t.Fatalf("fixture sanity: want 3 of 5 fixtures to match in Go, got %d", len(expected))
	}

	got := map[string]bool{}
	for _, r := range matched {
		got[r.ID] = true
	}
	for id := range expected {
		if !got[id] {
			t.Fatalf("MatchingRules (SQL) missed a rule the Go matcher accepts: %s — drift", id)
		}
	}
	for _, r := range matched {
		if !ruleMatches(r, tuple) {
			t.Fatalf("MatchingRules (SQL) returned a rule the Go matcher rejects: %+v — drift", r)
		}
	}
	if len(matched) != len(expected) {
		t.Fatalf("MatchingRules returned %d rows, Go matcher expected %d", len(matched), len(expected))
	}

	// Evaluate over the SQL-matched set must reach the identical effective
	// decision as Evaluate over the full known-fixture set.
	wantOutcome, wantWinner, wantOK := Evaluate(created, tuple)
	gotOutcome, gotWinner, gotOK := Evaluate(matched, tuple)
	if wantOK != gotOK || wantOutcome != gotOutcome || wantWinner.ID != gotWinner.ID {
		t.Fatalf("Evaluate(MatchingRules(...)) disagrees with Evaluate(all fixtures, ...): got outcome=%q winner=%s ok=%v, want outcome=%q winner=%s ok=%v",
			gotOutcome, gotWinner.ID, gotOK, wantOutcome, wantWinner.ID, wantOK)
	}
	if wantOutcome != RuleOutcomeDeny || wantWinner.Label != "all wildcard deny" {
		t.Fatalf("sanity: want the all-wildcard deny fixture to win this tuple (CON-3), got outcome=%q winner label=%q", wantOutcome, wantWinner.Label)
	}
}

// TestEvaluateTupleComposesMatchAndEvaluate exercises the thin composed
// method end to end against Postgres: a matched allow, and a tuple with no
// matching rule at all (ok=false, no error — never a matched allow).
func TestEvaluateTupleComposesMatchAndEvaluate(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)
	const edge, action, target = "agent-remote-session", "remote.file.read", "resource:t149a-composed"

	r, err := s.CreateRule(ctx, CreateRuleInput{
		Label: "composed allow", Subject: subject, Edge: edge, Action: action, Target: target, Outcome: RuleOutcomeAllow,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteRule(ctx, r.ID) })

	outcome, winner, ok, err := s.EvaluateTuple(ctx, Tuple{Subject: subject, Edge: edge, Action: action, Target: target})
	if err != nil {
		t.Fatalf("EvaluateTuple: %v", err)
	}
	if !ok || outcome != RuleOutcomeAllow || winner.ID != r.ID {
		t.Fatalf("want allow/%s, got outcome=%q winner=%s ok=%v", r.ID, outcome, winner.ID, ok)
	}

	noMatch := Tuple{Subject: distinctSubject(t), Edge: "no-such-edge", Action: "no-such-action", Target: "no-such-target"}
	_, _, ok2, err2 := s.EvaluateTuple(ctx, noMatch)
	if err2 != nil {
		t.Fatalf("EvaluateTuple (no match): %v", err2)
	}
	if ok2 {
		t.Fatalf("want ok=false for a tuple with no matching rule — never conflate with a matched allow")
	}
}
