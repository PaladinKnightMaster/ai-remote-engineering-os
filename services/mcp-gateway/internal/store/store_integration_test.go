//go:build integration

// Integration tests for the mcp-gateway store against a real Postgres with the
// gateway migration applied:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export MCP_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/mcp-gateway migrate up
//	go test -tags integration ./services/mcp-gateway/internal/store/...
package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("MCP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MCP_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

// randSuffix returns a unique token. NOTE: use the FULL UUID — UUID v7 begins with
// a millisecond timestamp, so a prefix is identical for ids minted in the same run
// (an early bug here: agents collided and CountInFlight summed across tests).
func randSuffix(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u.String()
}

func countOutcome(t *testing.T, pool *pgxpool.Pool, evidence, outcome string) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM gateway_outbox
		 WHERE payload->>'evidencePacketId' = $1 AND payload->>'outcome' = $2`,
		evidence, outcome).Scan(&n)
	if err != nil {
		t.Fatalf("count outcome: %v", err)
	}
	return n
}

// A retry carrying the same idempotency key reconciles to the original invocation
// and emits tool.invoked exactly once (PERF-2).
func TestCreateIdempotency(t *testing.T) {
	st, pool := testStore(t)
	ctx := context.Background()
	in := CreateInput{
		AgentSubject:   "agent:" + randSuffix(t),
		ResourceID:     "r-1",
		Tool:           "remote.exec",
		IdempotencyKey: "idem-" + randSuffix(t),
	}

	inv1, created1, err := st.Create(ctx, in)
	if err != nil || !created1 {
		t.Fatalf("first create: created=%v err=%v", created1, err)
	}
	inv2, created2, err := st.Create(ctx, in)
	if err != nil {
		t.Fatalf("retry create: %v", err)
	}
	if created2 {
		t.Fatal("retry with same idempotency key must not create a new invocation")
	}
	if inv2.ID != inv1.ID {
		t.Fatalf("retry id %s != original %s", inv2.ID, inv1.ID)
	}
	if n := countOutcome(t, pool, "invocation:"+inv1.ID, "tool.invoked"); n != 1 {
		t.Fatalf("tool.invoked count = %d, want exactly 1 (no double-emit on retry)", n)
	}
}

// Keyless calls always create distinct invocations (identical commands may run more
// than once legitimately).
func TestKeylessCreatesAreDistinct(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	agent := "agent:" + randSuffix(t)
	a, _, err := st.Create(ctx, CreateInput{AgentSubject: agent, ResourceID: "r", Tool: "remote.exec"})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, _, err := st.Create(ctx, CreateInput{AgentSubject: agent, ResourceID: "r", Tool: "remote.exec"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if a.ID == b.ID {
		t.Fatal("keyless invocations must be distinct")
	}
}

// CountInFlight tracks the concurrency cap input; Finish makes it terminal and
// enqueues tool.result.
func TestConcurrencyCountAndFinish(t *testing.T) {
	st, pool := testStore(t)
	ctx := context.Background()
	agent := "agent:" + randSuffix(t)
	inv, _, err := st.Create(ctx, CreateInput{AgentSubject: agent, ResourceID: "r", Tool: "remote.exec"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n, _ := st.CountInFlight(ctx, agent); n != 1 {
		t.Fatalf("in-flight = %d, want 1", n)
	}
	if err := st.Finish(ctx, inv.ID, "completed", "tool.result"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if n, _ := st.CountInFlight(ctx, agent); n != 0 {
		t.Fatalf("in-flight after finish = %d, want 0", n)
	}
	if n := countOutcome(t, pool, "invocation:"+inv.ID, "tool.result"); n != 1 {
		t.Fatalf("tool.result count = %d, want 1", n)
	}
}

func TestSetStatusAndNotFound(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	inv, _, err := st.Create(ctx, CreateInput{AgentSubject: "agent:" + randSuffix(t), ResourceID: "r", Tool: "remote.exec"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetStatus(ctx, inv.ID, "running", "allow"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if err := st.Finish(ctx, uuid.Must(uuid.NewV7()).String(), "completed", "tool.result"); err != ErrNotFound {
		t.Fatalf("finish unknown id: err = %v, want ErrNotFound", err)
	}
}
