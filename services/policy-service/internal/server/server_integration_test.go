//go:build integration

// Integration tests + benchmarks for T-150's switch-on against a real Postgres
// with the policy-service migrations applied:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export POLICY_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/policy-service migrate up
//	go test -tags integration -p 1 -v ./services/policy-service/internal/server/...
//
// The unit tests in server_test.go exercise decideHandler's dispatch against a
// fake DecisionStore (fast, no DB). These prove the same dispatch end to end
// through decideHandler → *store.Store → real Postgres → policy_outbox:
// CON-3 (severity beats specificity through the WHOLE handler, not just the
// pure Evaluate function) and AUD-35/AUD-36 (the chained event's fields).
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/policy-service/internal/server"
	"aireos.local/services/policy-service/internal/store"
)

func testPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POLICY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("POLICY_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// uniqueEdge mints a per-test-run edge value so a wildcard-except-edge rule
// cannot collide with, or be matched by, any other test's fixtures or
// concurrent-suite noise — the same isolation trick evaluate_integration_test.go
// uses via distinctSubject, applied to the field this suite varies.
func uniqueEdge(t testing.TB, label string) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return "test-t150-" + label + "-" + u.String()
}

func newSessionID(t testing.TB) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u.String()
}

type outboxEvent struct{ Outcome, Actor, Edge, Target, EvidencePacketID string }

func outboxEventsFor(t testing.TB, pool *pgxpool.Pool, sessionID string) []outboxEvent {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT payload->>'outcome', payload->>'actor', payload->>'edge',
		       payload->>'target', payload->>'evidencePacketId'
		  FROM policy_outbox
		 WHERE payload->>'evidencePacketId' = $1 ORDER BY id`, "session:"+sessionID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []outboxEvent
	for rows.Next() {
		var e outboxEvent
		if err := rows.Scan(&e.Outcome, &e.Actor, &e.Edge, &e.Target, &e.EvidencePacketID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, e)
	}
	return out
}

func decideReq(sessionID, subject, edge, action, target string, requiresApproval bool) []byte {
	body, _ := json.Marshal(map[string]any{
		"session_id": sessionID, "subject": subject, "edge": edge,
		"target": target, "action": action, "requires_approval": requiresApproval,
	})
	return body
}

// TestDecideWildcardDenyBeatsSpecificAllowEndToEnd — CON-3 through the whole
// handler: a fully-specific allow rule AND a wildcard deny rule both match the
// same tuple; deny must win (severity beats specificity, ADR-0074 §D2), and the
// win must be observable in the actual HTTP response and the actual chained
// event, not just the pure Evaluate function evaluate_test.go already covers.
// Also proves AUD-35 (chained) and AUD-36 (positively recorded, exact fields).
func TestDecideWildcardDenyBeatsSpecificAllowEndToEnd(t *testing.T) {
	pool := testPool(t)
	s := store.New(pool)
	ctx := context.Background()

	subject := "operator:test-" + newSessionID(t)
	edge := uniqueEdge(t, "deny-beats-allow") // unique so the wildcard rule below cannot leak onto any other test's tuple
	const action = "session.start"
	target := "resource:" + newSessionID(t)

	specificAllow, err := s.CreateRule(ctx, store.CreateRuleInput{
		Label: "specific allow", Subject: subject, Edge: edge, Action: action, Target: target, Outcome: store.RuleOutcomeAllow,
	})
	if err != nil {
		t.Fatalf("create specific allow: %v", err)
	}
	wildcardDeny, err := s.CreateRule(ctx, store.CreateRuleInput{
		Label: "wildcard deny", Subject: store.Wildcard, Edge: edge, Action: store.Wildcard, Target: store.Wildcard, Outcome: store.RuleOutcomeDeny,
	})
	if err != nil {
		t.Fatalf("create wildcard deny: %v", err)
	}
	t.Cleanup(func() {
		_ = s.DeleteRule(ctx, specificAllow.ID, "operator:test-cleanup")
		_ = s.DeleteRule(ctx, wildcardDeny.ID, "operator:test-cleanup")
	})

	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})
	sid := newSessionID(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions",
		bytes.NewReader(decideReq(sid, subject, edge, action, target, false)))
	req.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("decide: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != store.DecisionDeny {
		t.Fatalf("CON-3: wildcard deny must beat the fully-specific allow through the handler, got %q", resp.Decision)
	}

	events := outboxEventsFor(t, pool, sid)
	if len(events) != 1 {
		t.Fatalf("AUD-36: want exactly one positively-recorded event, got %d: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Outcome != store.OutcomeDecisionDeny {
		t.Fatalf("AUD-35: outcome = %q, want %q", ev.Outcome, store.OutcomeDecisionDeny)
	}
	if ev.Actor != subject || ev.Edge != edge || ev.Target != target || ev.EvidencePacketID != "session:"+sid {
		t.Fatalf("AUD-35: event fields wrong: %+v (want actor=%s edge=%s target=%s evidence=session:%s)", ev, subject, edge, target, sid)
	}
}

// TestDecideAllowRuleChainsEventEndToEnd — a matched allow rule wins over a
// flagged resource's own label (a matched rule beats the CON-4 fallback) and
// chains policy.decision:allow with the exact same field shape as the deny
// case above (AUD-35).
func TestDecideAllowRuleChainsEventEndToEnd(t *testing.T) {
	pool := testPool(t)
	s := store.New(pool)
	ctx := context.Background()

	subject := "operator:test-" + newSessionID(t)
	edge := uniqueEdge(t, "allow-chains")
	const action = "session.start"
	target := "resource:" + newSessionID(t)

	rule, err := s.CreateRule(ctx, store.CreateRuleInput{
		Label: "exact allow", Subject: subject, Edge: edge, Action: action, Target: target, Outcome: store.RuleOutcomeAllow,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	t.Cleanup(func() { _ = s.DeleteRule(ctx, rule.ID, "operator:test-cleanup") })

	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})
	sid := newSessionID(t)
	rec := httptest.NewRecorder()
	// requires_approval=true on the request: the matched allow rule must still
	// win — a matched rule always outranks the resource's own label.
	req := httptest.NewRequest(http.MethodPost, "/v1/decisions",
		bytes.NewReader(decideReq(sid, subject, edge, action, target, true)))
	req.Header.Set("Authorization", "Bearer t")
	h.ServeHTTP(rec, req)

	var resp struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Decision != store.DecisionAllow {
		t.Fatalf("a matched allow rule must win over the flagged resource's own label, got %q", resp.Decision)
	}
	events := outboxEventsFor(t, pool, sid)
	if len(events) != 1 || events[0].Outcome != store.OutcomeDecisionAllow {
		t.Fatalf("want exactly one policy.decision:allow, got %+v", events)
	}
}

// --- PERF-9: a recorded measurement, not shipped instrumentation. ----------
//
// CEO scope ruling 2026-08-28: measure decide-call latency and write the
// number down; do not build a metrics surface. `go test -bench` is a
// standard Go testing artifact, not application instrumentation — nothing
// here ships in the service binary.
//
//	go test -tags integration -p 1 -run '^$' -bench . -benchtime 200x \
//	  ./services/policy-service/internal/server/...

// BenchmarkDecideEmptyRuleTable measures a decide call that always falls back
// (CON-4) on an empty policy_rules table — the v0.14.0 baseline shape.
func BenchmarkDecideEmptyRuleTable(b *testing.B) {
	pool := testPool(b)
	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})
	subject, edge, action, target := "operator:bench", uniqueEdge(b, "empty-table"), "session.start", "resource:bench"
	body := decideReq("bench-session", subject, edge, action, target, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer t")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("decide: want 200, got %d", rec.Code)
		}
	}
}

// BenchmarkDecideSmallRuleSet measures the same call shape with 20 enabled
// rules present, none matching the benchmarked tuple — so every iteration
// still takes the CON-4 fallback, but now pays for the indexed MatchingRules
// query (policy_rules_enabled_idx) instead of querying an empty table.
func BenchmarkDecideSmallRuleSet(b *testing.B) {
	pool := testPool(b)
	s := store.New(pool)
	ctx := context.Background()
	edge := uniqueEdge(b, "small-set")
	var created []store.Rule
	for i := 0; i < 20; i++ {
		r, err := s.CreateRule(ctx, store.CreateRuleInput{
			Label: "bench filler", Subject: "operator:no-match", Edge: edge,
			Action: fmt.Sprintf("no.match.%d", i), Target: "resource:no-match", Outcome: store.RuleOutcomeAllow,
		})
		if err != nil {
			b.Fatalf("create filler rule: %v", err)
		}
		created = append(created, r)
	}
	b.Cleanup(func() {
		for _, r := range created {
			_ = s.DeleteRule(ctx, r.ID, "operator:test-cleanup")
		}
	})

	h := server.New(server.Config{Verifier: allowVerifier{}, Pool: pool, TTL: 5 * time.Minute})
	subject, action, target := "operator:bench", "session.start", "resource:bench"
	body := decideReq("bench-session", subject, edge, action, target, false)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/decisions", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer t")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("decide: want 200, got %d", rec.Code)
		}
	}
}
