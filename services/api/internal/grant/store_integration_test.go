//go:build integration

// Integration tests for the delegation-grant store against a real Postgres (T-063 G1).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/grant/...
package grant

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) *Store {
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
	return NewStore(pool, "api_outbox") // Create/Revoke enqueue grant.* into api_outbox
}

func sampleInput(principal, agent string) Input {
	return Input{
		Principal: principal, Agent: agent, Label: "ci grant",
		Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"},
		ExpiresAt: time.Now().Add(time.Hour), MaxStepsPerRun: 10, MaxConcurrentRuns: 2, StepBudget: 50,
	}
}

// TestGrantStoreLifecycle — the T-063 G1 live-PG proof: Create persists the grant + emits
// grant.issued in-tx (AUD-16); Get round-trips the arrays + bounds; ListForOperator is
// principal-owner-scoped; Revoke flips status + emits grant.revoked, is idempotent (a
// second revoke is a no-op), and an unknown id is not found.
func TestGrantStoreLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleInput("operator:alice", "agent:bot-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != StatusActive {
		t.Fatalf("unexpected created: %+v", created)
	}
	if n := countOutcome(t, s, "grant:"+created.ID, "grant.issued"); n != 1 {
		t.Fatalf("grant.issued count = %d, want 1", n)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Agent != "agent:bot-1" || got.Principal != "operator:alice" || got.Status != StatusActive {
		t.Fatalf("get mismatch: %+v", got)
	}
	if len(got.Resources) != 2 || got.Resources[0] != "res-a" || got.Verbs[0] != "exec" {
		t.Fatalf("array round-trip wrong: %+v", got)
	}
	if got.MaxStepsPerRun != 10 || got.MaxConcurrentRuns != 2 || got.StepBudget != 50 {
		t.Fatalf("bounds round-trip wrong: %+v", got)
	}

	// ListForOperator is owner-scoped: alice sees her grant, bob does not.
	grants, _, err := s.ListForOperator(ctx, "operator:alice", "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !containsGrant(grants, created.ID) {
		t.Fatalf("created grant %s not in alice's list", created.ID)
	}
	others, _, err := s.ListForOperator(ctx, "operator:bob", "", 100)
	if err != nil {
		t.Fatalf("list bob: %v", err)
	}
	if containsGrant(others, created.ID) {
		t.Fatal("alice's grant leaked into bob's owner-scoped list")
	}

	// Revoke → status flips + grant.revoked emitted; a second revoke is a no-op.
	if ok, err := s.Revoke(ctx, created.ID); err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	if g, _ := s.Get(ctx, created.ID); g.Status != StatusRevoked {
		t.Fatalf("status after revoke = %q, want revoked", g.Status)
	}
	if n := countOutcome(t, s, "grant:"+created.ID, "grant.revoked"); n != 1 {
		t.Fatalf("grant.revoked count = %d, want 1", n)
	}
	if ok, _ := s.Revoke(ctx, created.ID); ok {
		t.Fatal("revoking an already-revoked grant must return ok=false")
	}

	// Unknown id → ErrNotFound (Get) and a no-op (Revoke).
	if _, err := s.Get(ctx, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}
	if ok, _ := s.Revoke(ctx, "00000000-0000-0000-0000-000000000000"); ok {
		t.Fatal("revoking an unknown grant must return ok=false")
	}
}

// TestGrantStoreCreateValidation — Create rejects a malformed grant (no agent, no resource,
// no verb, a past expiry, a non-positive bound), fail-loud, no partial row.
func TestGrantStoreCreateValidation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	base := func() Input { return sampleInput("operator:alice", "agent:bot-1") }

	bad := func(name string, mut func(*Input)) {
		in := base()
		mut(&in)
		if _, err := s.Create(ctx, in); err == nil {
			t.Fatalf("%s: want error, got nil", name)
		}
	}
	bad("no agent", func(in *Input) { in.Agent = "" })
	bad("no resources", func(in *Input) { in.Resources = nil })
	bad("no verbs", func(in *Input) { in.Verbs = nil })
	bad("past expiry", func(in *Input) { in.ExpiresAt = time.Now().Add(-time.Minute) })
	bad("zero budget", func(in *Input) { in.StepBudget = 0 })
	bad("zero max steps", func(in *Input) { in.MaxStepsPerRun = 0 })
}

func countOutcome(t *testing.T, s *Store, evidence, outcome string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM api_outbox WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
		evidence, outcome).Scan(&n); err != nil {
		t.Fatalf("count outcome: %v", err)
	}
	return n
}

func containsGrant(grants []Grant, id string) bool {
	for _, g := range grants {
		if g.ID == id {
			return true
		}
	}
	return false
}
