//go:build integration

// Integration tests for T-151a's /v1/rules routes against a real Postgres
// with the policy-service migrations applied — see server_integration_test.go
// for the run command. These prove two things a fake store cannot: the routes
// are actually wired to *store.Store (not just DecisionStore-shaped), and
// SEC-54 (a disabled rule is inert; a deleted rule leaves its already-chained
// decisions intact — the audit-chain outbox is a separate table from
// policy_rules, so a rule DELETE must not touch it).
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"aireos.local/services/policy-service/internal/server"
	"aireos.local/services/policy-service/internal/store"
)

// TestDisabledRuleIsInertEndToEnd — SEC-54, half 1: a matched deny rule wins
// a decide call while enabled; disabling it via PATCH /v1/rules/{id} makes
// the SAME tuple's decide call stop seeing it — the evaluator (T-149a) only
// consults policy_rules_enabled_idx (WHERE enabled), so a disabled row is
// invisible to matching, not merely "loses ties".
func TestDisabledRuleIsInertEndToEnd(t *testing.T) {
	pool := testPool(t)
	s := store.New(pool)
	ctx := context.Background()
	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})

	subject := "operator:test-" + newSessionID(t)
	edge := uniqueEdge(t, "disabled-inert")
	const action = "session.start"
	target := "resource:" + newSessionID(t)

	rule, err := s.CreateRule(ctx, store.CreateRuleInput{
		Label: "inert check", Subject: subject, Edge: edge, Action: action, Target: target,
		Outcome: store.RuleOutcomeDeny, Actor: "operator:test-cleanup",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteRule(ctx, rule.ID, "operator:test-cleanup") })

	sid1 := newSessionID(t)
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(decideReq(sid1, subject, edge, action, target, false)))
	req1.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec1, req1)
	var resp1 struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(rec1.Body.Bytes(), &resp1)
	if resp1.Decision != store.DecisionDeny {
		t.Fatalf("enabled rule: want deny, got %q", resp1.Decision)
	}

	patchBody, _ := json.Marshal(map[string]any{"enabled": false, "actor": "operator:test-disable"})
	patchRec := httptest.NewRecorder()
	patchReq := httptest.NewRequest(http.MethodPatch, "/v1/rules/"+rule.ID, bytes.NewReader(patchBody))
	patchReq.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("disable rule: want 200, got %d (%s)", patchRec.Code, patchRec.Body.String())
	}

	sid2 := newSessionID(t)
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(decideReq(sid2, subject, edge, action, target, false)))
	req2.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec2, req2)
	var resp2 struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(rec2.Body.Bytes(), &resp2)
	// SEC-54 firing assertion: with the rule disabled, this tuple must fall
	// through to the CON-4 fallback (requires_approval=false → allow) — NOT
	// deny. A disabled rule that still matched would return deny here.
	if resp2.Decision != store.DecisionAllow {
		t.Fatalf("SEC-54: disabled rule must be inert — want allow (CON-4 fallback), got %q", resp2.Decision)
	}
}

// TestDeleteRuleLeavesChainedDecisionsIntactEndToEnd — SEC-54, half 2: a
// decide chained under a rule, then that rule DELETEd via the route, must
// leave the already-chained policy_outbox event byte-for-byte untouched — the
// audit trail is a separate table (ADR-0045 outbox) with no foreign key back
// to policy_rules, so a rule delete has nothing to cascade into.
func TestDeleteRuleLeavesChainedDecisionsIntactEndToEnd(t *testing.T) {
	pool := testPool(t)
	s := store.New(pool)
	ctx := context.Background()
	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})

	subject := "operator:test-" + newSessionID(t)
	edge := uniqueEdge(t, "delete-leaves-chain")
	const action = "session.start"
	target := "resource:" + newSessionID(t)

	rule, err := s.CreateRule(ctx, store.CreateRuleInput{
		Label: "chain then delete", Subject: subject, Edge: edge, Action: action, Target: target,
		Outcome: store.RuleOutcomeDeny, Actor: "operator:test-chain-then-delete",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	sid := newSessionID(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(decideReq(sid, subject, edge, action, target, false)))
	req.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec, req)
	var resp struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Decision != store.DecisionDeny {
		t.Fatalf("setup: want deny before delete, got %q", resp.Decision)
	}
	before := outboxEventsFor(t, pool, sid)
	if len(before) != 1 || before[0].Outcome != store.OutcomeDecisionDeny {
		t.Fatalf("setup: want exactly one chained policy.decision:deny, got %+v", before)
	}

	deleteBody, _ := json.Marshal(map[string]string{"actor": "operator:test-delete"})
	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/v1/rules/"+rule.ID, bytes.NewReader(deleteBody))
	deleteReq.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete rule: want 204, got %d (%s)", deleteRec.Code, deleteRec.Body.String())
	}

	// SEC-54 firing assertion: the event chained BEFORE the rule was deleted
	// must survive, unchanged, after the delete.
	after := outboxEventsFor(t, pool, sid)
	if len(after) != 1 {
		t.Fatalf("SEC-54: chained decision must survive the rule's deletion, got %d events: %+v", len(after), after)
	}
	if after[0] != before[0] {
		t.Fatalf("SEC-54: chained event fields must be byte-for-byte unchanged: before=%+v after=%+v", before[0], after[0])
	}

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/v1/rules/"+rule.ID, nil)
	getReq.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("deleted rule should 404 on get, got %d", getRec.Code)
	}
}
