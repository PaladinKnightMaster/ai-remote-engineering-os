//go:build integration

// Integration tests for the chartered-Workstream store against a real Postgres
// (T-071 G1).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/workstream/...
package workstream

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/grant"
)

// Interface conformance: the real grant store satisfies GrantCreator (compile-time).
var _ GrantCreator = (*grant.Store)(nil)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("API_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	// The real grant store is the GrantCreator (WS-2: one issuance path, SEC-30).
	return NewStore(pool, nil, grant.NewStore(pool, "api_outbox"), "api_outbox"), pool
}

func sampleInput(principal string) Input {
	return Input{
		Principal: principal, Label: "nightly health", Objective: "keep the fleet healthy",
		IntervalSeconds: 3600,
		Steps: []StepInput{
			{ResourceID: "res-a", Command: "uptime"},
			{ResourceID: "res-b", Command: "df -h"},
		},
		Grant: grant.Input{
			Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"},
			ExpiresAt: time.Now().Add(24 * time.Hour), MaxStepsPerRun: 5,
			MaxConcurrentRuns: 2, StepBudget: 10, BudgetWindowSeconds: 3600,
		},
	}
}

// TestWorkstreamCharterLifecycle — the T-071 G1 live-PG proof: Create charters the
// workstream + its sealed template + the auto-issued grant (grantee = workstream:<id>,
// principal bound server-side) in ONE transaction, with workstream.chartered +
// grant.issued both enqueued from it (WS-2/AUD-18); first fire = charter + interval;
// Get round-trips the template; ListForOperator is owner-scoped and template-free.
func TestWorkstreamCharterLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	before := time.Now()
	created, err := s.Create(ctx, sampleInput("operator:alice"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != StatusActive || created.GrantID == "" {
		t.Fatalf("unexpected created: %+v", created)
	}

	// First fire = charter + interval (no immediate fire, ADR-0063 §5).
	wantFire := before.Add(time.Duration(created.IntervalSeconds) * time.Second)
	if created.NextFireAt.Before(wantFire.Add(-5*time.Second)) || created.NextFireAt.After(wantFire.Add(time.Minute)) {
		t.Fatalf("next_fire_at = %v, want ≈ charter+%ds", created.NextFireAt, created.IntervalSeconds)
	}

	// The auto-issued grant: grantee = workstream:<id>, principal = the charterer,
	// window carried (WS-4).
	g, err := grant.NewStore(pool, "api_outbox").Get(ctx, created.GrantID)
	if err != nil {
		t.Fatalf("get charter grant: %v", err)
	}
	if g.Agent != "workstream:"+created.ID || g.Principal != "operator:alice" {
		t.Fatalf("grant binding wrong: agent=%q principal=%q", g.Agent, g.Principal)
	}
	if g.BudgetWindowSeconds != 3600 || g.Status != grant.StatusActive {
		t.Fatalf("grant window/status wrong: %+v", g)
	}

	// Both consent events, from the one tx (WS-2/AUD-18).
	if n := countOutcome(t, pool, "workstream:"+created.ID, "workstream.chartered"); n != 1 {
		t.Fatalf("workstream.chartered count = %d, want 1", n)
	}
	if n := countOutcome(t, pool, "grant:"+created.GrantID, "grant.issued"); n != 1 {
		t.Fatalf("grant.issued count = %d, want 1", n)
	}

	// Get round-trips the (sealed-at-rest) template in order.
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Steps) != 2 || got.Steps[0].Command != "uptime" || got.Steps[1].ResourceID != "res-b" {
		t.Fatalf("template round-trip wrong: %+v", got.Steps)
	}
	if got.Objective != "keep the fleet healthy" || got.GrantID != created.GrantID {
		t.Fatalf("get mismatch: %+v", got)
	}

	// ListForOperator: owner-scoped, template-free.
	mine, _, err := s.ListForOperator(ctx, "operator:alice", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsWorkstream(mine, created.ID) {
		t.Fatalf("created workstream %s not in alice's list", created.ID)
	}
	for _, w := range mine {
		if len(w.Steps) != 0 {
			t.Fatal("list must not carry templates")
		}
	}
	others, _, err := s.ListForOperator(ctx, "operator:bob", "", 100)
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if containsWorkstream(others, created.ID) {
		t.Fatal("alice's workstream leaked into bob's owner-scoped list")
	}

	// Unknown id → ErrNotFound.
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}
}

// TestWorkstreamCharterAtomicity — a failing grant issuance rolls the whole charter back
// (WS-2): no workstream row, no template, no events. The failure is forced through the
// SHARED validator (an expired grant TTL rejected inside CreateInTx).
func TestWorkstreamCharterAtomicity(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	in := sampleInput("operator:atomic")
	in.Grant.ExpiresAt = time.Now().Add(-time.Minute) // CreateInTx rejects → tx must roll back
	if _, err := s.Create(ctx, in); err == nil {
		t.Fatal("want charter failure on a bad grant, got nil")
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workstreams WHERE principal = 'operator:atomic'`).Scan(&n); err != nil {
		t.Fatalf("count workstreams: %v", err)
	}
	if n != 0 {
		t.Fatalf("charter row survived a failed grant issuance: %d rows", n)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'actor' = 'operator:atomic' AND payload->>'outcome' = 'workstream.chartered'`).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if n != 0 {
		t.Fatalf("workstream.chartered leaked from a rolled-back charter: %d events", n)
	}
}

// TestWorkstreamCreateValidation — Create rejects a malformed charter (no objective, no
// steps, a bad interval, an incomplete step), fail-loud, no partial row.
func TestWorkstreamCreateValidation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	bad := func(name string, mut func(*Input)) {
		in := sampleInput("operator:val")
		mut(&in)
		if _, err := s.Create(ctx, in); err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
	}
	bad("no objective", func(in *Input) { in.Objective = "" })
	bad("no steps", func(in *Input) { in.Steps = nil })
	bad("zero interval", func(in *Input) { in.IntervalSeconds = 0 })
	bad("step without command", func(in *Input) { in.Steps[0].Command = "" })
}

func countOutcome(t *testing.T, pool *pgxpool.Pool, evidence, outcome string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
		evidence, outcome).Scan(&n); err != nil {
		t.Fatalf("count outcome: %v", err)
	}
	return n
}

func containsWorkstream(items []Workstream, id string) bool {
	for _, w := range items {
		if w.ID == id {
			return true
		}
	}
	return false
}
