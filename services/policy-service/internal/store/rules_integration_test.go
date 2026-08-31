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
	"strings"
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
		Actor: "operator:test-lifecycle",
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

	disabled, err := s.SetRuleEnabled(ctx, created.ID, false, "operator:test-lifecycle")
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

	if err := s.DeleteRule(ctx, created.ID, "operator:test-lifecycle"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetRule(ctx, created.ID); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("get after delete: want ErrRuleNotFound, got %v", err)
	}
	if err := s.DeleteRule(ctx, created.ID, "operator:test-lifecycle"); !errors.Is(err, ErrRuleNotFound) {
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
	_ = s.DeleteRule(ctx, r.ID, "operator:test-wildcard")
}

// TestMalformedIDMapsToNotFoundNotError — CON-7 (T-151b): a malformed
// (non-UUID) id raises Postgres SQLSTATE 22P02 ("invalid text
// representation") against the uuid-typed id column — this is the ONLY
// existing test in this repo that can actually produce a real 22P02 (a fake
// pool never would), which is why it lives in the integration file rather
// than rules_test.go. Before the fix this fell through GetRule/SetRuleEnabled
// /DeleteRule's error mapping as an opaque wrapped error (a 500 through the
// whole chain, per the criteria doc); a malformed id definitionally matches
// no row, so it now maps to the same ErrRuleNotFound as a well-formed-but-
// absent id.
func TestMalformedIDMapsToNotFoundNotError(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	if _, err := s.GetRule(ctx, "not-a-uuid"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("get malformed id: want ErrRuleNotFound, got %v", err)
	}
	if _, err := s.SetRuleEnabled(ctx, "not-a-uuid", false, "operator:test-malformed"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("set-enabled malformed id: want ErrRuleNotFound, got %v", err)
	}
	if err := s.DeleteRule(ctx, "not-a-uuid", "operator:test-malformed"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("delete malformed id: want ErrRuleNotFound, got %v", err)
	}
}

// ruleOutboxEvent mirrors server package's outboxEvent — this file has no
// access to that unexported test type, and a rule mutation's evidencePacketId
// is a rule:<id> anchor (not session:<id>), so it needs its own query anyway.
type ruleOutboxEvent struct{ Outcome, Actor, Edge, Target, EvidencePacketID string }

func ruleOutboxEventsFor(t *testing.T, s *Store, ruleID string) []ruleOutboxEvent {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `
		SELECT payload->>'outcome', payload->>'actor', payload->>'edge',
		       payload->>'target', payload->>'evidencePacketId'
		  FROM policy_outbox
		 WHERE payload->>'evidencePacketId' = $1 ORDER BY id`, ruleAnchor(ruleID))
	if err != nil {
		t.Fatalf("query rule outbox: %v", err)
	}
	defer rows.Close()
	var out []ruleOutboxEvent
	for rows.Next() {
		var e ruleOutboxEvent
		if err := rows.Scan(&e.Outcome, &e.Actor, &e.Edge, &e.Target, &e.EvidencePacketID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, e)
	}
	return out
}

// TestRuleMutationsChainAUD40Events — AUD-40: create/enable/disable/delete
// each chain exactly one policy.rule:* event, in order, with the right actor,
// the rule's own edge on Event.Edge, and rule:<id> as the anchor. The delete
// case is the sharpest: policy_rules no longer has the row afterward, so this
// is the ONLY place left to see what the deleted rule actually allowed.
func TestRuleMutationsChainAUD40Events(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	subject := distinctSubject(t)
	const actor = "operator:test-aud40"

	r, err := s.CreateRule(ctx, CreateRuleInput{
		Label: "aud-40 proof", Subject: subject, Edge: "agent-remote-session",
		Action: "remote.exec", Target: "resource:aud40", Outcome: RuleOutcomeDeny, Actor: actor,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.SetRuleEnabled(ctx, r.ID, false, actor); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := s.SetRuleEnabled(ctx, r.ID, true, actor); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := s.DeleteRule(ctx, r.ID, actor); err != nil {
		t.Fatalf("delete: %v", err)
	}

	events := ruleOutboxEventsFor(t, s, r.ID)
	wantOutcomes := []string{OutcomeRuleCreated, OutcomeRuleDisabled, OutcomeRuleEnabled, OutcomeRuleDeleted}
	if len(events) != len(wantOutcomes) {
		t.Fatalf("want %d chained rule events, got %d: %+v", len(wantOutcomes), len(events), events)
	}
	for i, want := range wantOutcomes {
		ev := events[i]
		if ev.Outcome != want {
			t.Fatalf("event %d: outcome = %q, want %q", i, ev.Outcome, want)
		}
		if ev.Actor != actor {
			t.Fatalf("event %d: actor = %q, want %q", i, ev.Actor, actor)
		}
		// AUD-41 (T-152b): Event.Edge is the edge the MUTATION traversed
		// (authoring/enabling/disabling/deleting a rule is an operator action
		// over human-agent — SEC-53), not the edge the rule governs. This
		// used to assert "agent-remote-session" (the rule's own edge) — that
		// was AUD-41's bug: it let a filter on edge=agent-remote-session
		// (reviewing egress activity) also return rule-authorship events.
		if ev.Edge != "human-agent" {
			t.Fatalf("event %d: edge = %q, want the traversed edge %q", i, ev.Edge, "human-agent")
		}
		// The rule's own governed edge must still survive somewhere in the
		// ledger — AUD-41 requires BOTH halves: Edge becomes the traversed
		// edge AND the rule's governed edge moves into Target. Losing this
		// half would delete the governed edge from the ledger entirely.
		if !strings.Contains(ev.Target, "edge=agent-remote-session") {
			t.Fatalf("event %d: target = %q, want it to contain the rule's governed edge %q", i, ev.Target, "edge=agent-remote-session")
		}
		if ev.EvidencePacketID != "rule:"+r.ID {
			t.Fatalf("event %d: evidencePacketId = %q, want anchor %q", i, ev.EvidencePacketID, "rule:"+r.ID)
		}
	}
	// AUD-40's sharpest requirement: the DELETE event alone (the row is gone
	// from policy_rules by now) still says what the rule allowed/denied.
	if !strings.Contains(events[3].Target, "outcome="+RuleOutcomeDeny) {
		t.Fatalf("deleted rule's own outcome did not survive on the delete event: %+v", events[3])
	}
	if !strings.Contains(events[3].Target, "subject="+subject) || !strings.Contains(events[3].Target, "action=remote.exec") {
		t.Fatalf("deleted rule's tuple did not survive on the delete event: %+v", events[3])
	}

	// Same-transaction proof: a rejected mutation (unknown id) enqueues
	// NOTHING — create a second, throwaway rule and delete it TWICE; the
	// second (already-gone) delete must add no new row to the anchor's event
	// list — reusing an existing id means "no such row" is exercised via the
	// real store path, not a synthetic uuid.
	r2, err := s.CreateRule(ctx, CreateRuleInput{
		Label: "aud-40 no-op-mutation proof", Subject: distinctSubject(t), Edge: "agent-remote-session",
		Action: "remote.exec", Target: "resource:aud40b", Outcome: RuleOutcomeAllow, Actor: actor,
	})
	if err != nil {
		t.Fatalf("create r2: %v", err)
	}
	if err := s.DeleteRule(ctx, r2.ID, actor); err != nil {
		t.Fatalf("delete r2: %v", err)
	}
	before := ruleOutboxEventsFor(t, s, r2.ID)
	if err := s.DeleteRule(ctx, r2.ID, actor); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("re-delete r2: want ErrRuleNotFound, got %v", err)
	}
	after := ruleOutboxEventsFor(t, s, r2.ID)
	if len(after) != len(before) {
		t.Fatalf("a failed mutation (already deleted) must enqueue NOTHING: before=%d after=%d", len(before), len(after))
	}
}
