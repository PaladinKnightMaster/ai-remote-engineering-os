//go:build integration

// Integration tests for the policy_rules store (ADR-0074, T-148) against a real
// Postgres with 0002_policy_rules applied. Reuses testStore(t) from
// store_integration_test.go — same harness, same env var:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export POLICY_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/policy-service migrate up
//	go test -tags integration ./services/policy-service/internal/store/...
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// distinctSubject keeps rows from colliding across test runs sharing one DB —
// every rule tuple in these tests carries a fresh subject so the UNIQUE
// (subject, edge, action, target) constraint never fires except where the test
// deliberately provokes it.
func distinctSubject(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return "operator:test-" + u.String()
}

// TestRuleLifecycleCreateGetListDisableDelete — create -> get -> list ->
// disable -> delete, each step observed against the store's own reads.
func TestRuleLifecycleCreateGetListDisableDelete(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)

	created, err := s.CreateRule(ctx, CreateRuleInput{
		Label: "deny composer exec", Subject: subject, Edge: "agent-remote-session",
		Action: "remote.exec", Target: "resource:r-1", Outcome: RuleOutcomeDeny,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || !created.Enabled || created.Outcome != RuleOutcomeDeny {
		t.Fatalf("unexpected created row: %+v", created)
	}

	got, err := s.GetRule(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != created {
		t.Fatalf("get mismatch: got %+v, want %+v", got, created)
	}

	all, err := s.ListRules(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, r := range all {
		if r.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("created rule %s not present in list of %d rules", created.ID, len(all))
	}

	disabled, err := s.SetRuleEnabled(ctx, created.ID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if disabled.Enabled {
		t.Fatalf("want disabled, got enabled row: %+v", disabled)
	}
	if !disabled.UpdatedAt.After(created.UpdatedAt) && disabled.UpdatedAt != created.UpdatedAt {
		// UpdatedAt must not go backwards; equal is only possible if the clock
		// didn't advance, which we don't assert against — just no regression.
		t.Fatalf("updated_at regressed: %v -> %v", created.UpdatedAt, disabled.UpdatedAt)
	}

	if err := s.DeleteRule(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetRule(ctx, created.ID); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("get after delete: want ErrRuleNotFound, got %v", err)
	}
	if err := s.DeleteRule(ctx, created.ID); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("double delete: want ErrRuleNotFound, got %v", err)
	}
}

// TestCreateRuleDuplicateTupleReturnsTypedError — a second rule over the exact
// same (subject, edge, action, target) tuple is rejected with ErrDuplicateRule,
// never a raw pg error string.
func TestCreateRuleDuplicateTupleReturnsTypedError(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)
	in := CreateRuleInput{
		Label: "first", Subject: subject, Edge: "agent-remote-session",
		Action: "remote.file.write", Target: "resource:r-2", Outcome: RuleOutcomeRequiresApproval,
	}

	if _, err := s.CreateRule(ctx, in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	in.Label = "second — different label, same tuple"
	in.Outcome = RuleOutcomeDeny
	if _, err := s.CreateRule(ctx, in); !errors.Is(err, ErrDuplicateRule) {
		t.Fatalf("duplicate create: want ErrDuplicateRule, got %v", err)
	}
}

// TestCreateRuleInvalidOutcomeRejectedBeforeQuery — an outcome outside the
// three decision values never reaches the DB (Go-side validation, T-148 spec).
func TestCreateRuleInvalidOutcomeRejectedBeforeQuery(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)

	_, err := s.CreateRule(ctx, CreateRuleInput{
		Subject: subject, Edge: "agent-remote-session", Action: "remote.exec",
		Target: "resource:r-3", Outcome: "block",
	})
	var invalid ErrInvalidRule
	if !errors.As(err, &invalid) {
		t.Fatalf("want ErrInvalidRule, got %v (%T)", err, err)
	}

	all, err := s.ListRules(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range all {
		if r.Subject == subject {
			t.Fatalf("invalid-outcome create must not have reached the DB, found row: %+v", r)
		}
	}
}

// TestWildcardRulesRoundTripAsLiteralAsterisk — every column can hold the
// literal "*" and it comes back byte-identical, never NULL.
func TestWildcardRulesRoundTripAsLiteralAsterisk(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	// Idempotency: the all-wildcard tuple is fixed (wildcards aren't unique-
	// able per run like distinctSubject rows are), so clear any leftover row
	// from a prior interrupted run before asserting a fresh create.
	if _, err := pool.Exec(ctx, `DELETE FROM policy_rules WHERE subject='*' AND edge='*' AND action='*' AND target='*'`); err != nil {
		t.Fatalf("pre-clean: %v", err)
	}

	r, err := s.CreateRule(ctx, CreateRuleInput{
		Label: "default deny-nothing wildcard", Subject: Wildcard, Edge: Wildcard,
		Action: Wildcard, Target: Wildcard, Outcome: RuleOutcomeAllow,
	})
	if err != nil {
		t.Fatalf("create wildcard rule: %v", err)
	}
	if r.Subject != "*" || r.Edge != "*" || r.Action != "*" || r.Target != "*" {
		t.Fatalf("wildcard did not round-trip as literal '*': %+v", r)
	}

	got, err := s.GetRule(ctx, r.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Subject != "*" || got.Edge != "*" || got.Action != "*" || got.Target != "*" {
		t.Fatalf("wildcard did not survive a re-read: %+v", got)
	}
	_ = s.DeleteRule(ctx, r.ID)
}
