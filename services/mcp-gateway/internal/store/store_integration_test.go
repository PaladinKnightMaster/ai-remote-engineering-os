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

	"aireos.local/services/mcp-gateway/internal/cmdcrypt"
)

// testKeyHex is a fixed 32-byte AES-256 key so the integration tests exercise the
// F3 command-at-rest path (encrypt on Create, decrypt on every read).
const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

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
	cipher, err := cmdcrypt.NewFromHex(testKeyHex)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	return New(pool, cipher), pool
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

// Get returns the persisted command (ADR-0059); ClaimForExec atomically moves a
// granted parked invocation to running exactly once (the PERF-2 single-winner gate).
func TestGetAndClaimForExec(t *testing.T) {
	st, pool := testStore(t)
	ctx := context.Background()
	inv, _, err := st.Create(ctx, CreateInput{
		AgentSubject: "agent:" + randSuffix(t), ResourceID: "r-claim", Tool: "remote.exec", Command: "uname -a",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// F3 (ADR-0059): the command is encrypted at rest — the raw column is NOT the
	// plaintext, but reads decrypt it transparently.
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT command FROM invocations WHERE id = $1`, inv.ID).Scan(&raw); err != nil {
		t.Fatalf("read raw command: %v", err)
	}
	if string(raw) == "uname -a" {
		t.Fatal("command stored in plaintext — F3 encryption not applied")
	}

	got, err := st.Get(ctx, inv.ID)
	if err != nil || got.Command != "uname -a" || got.Status != "pending_approval" {
		t.Fatalf("Get = %+v err=%v, want command persisted + pending_approval", got, err)
	}

	// Not yet granted → the claim must not win.
	if _, won, _ := st.ClaimForExec(ctx, inv.ID); won {
		t.Fatal("claim won before grant")
	}
	// Operator grant (decision → allow); the resolve route does this in T-040a2.
	if err := st.SetStatus(ctx, inv.ID, "pending_approval", "allow"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	claimed, won, err := st.ClaimForExec(ctx, inv.ID)
	if err != nil || !won || claimed.Command != "uname -a" || claimed.Status != "running" {
		t.Fatalf("claim = %+v won=%v err=%v, want win → running with the persisted command", claimed, won, err)
	}
	// Single-winner: a second claim must not win (no double-exec, PERF-2).
	if _, won, _ := st.ClaimForExec(ctx, inv.ID); won {
		t.Fatal("second claim won — the double-exec gate failed")
	}
}

// ReapStale finalizes a never-resolved parked invocation → failed, emits
// tool.result:error, and frees the concurrency slot (ADR-0059).
func TestReapStaleFinalizesParked(t *testing.T) {
	st, pool := testStore(t)
	ctx := context.Background()
	agent := "agent:" + randSuffix(t)
	inv, _, err := st.Create(ctx, CreateInput{AgentSubject: agent, ResourceID: "r-reap", Tool: "remote.exec", Command: "sleep 1"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// olderThan=0 → the just-created parked row qualifies as stale.
	n, err := st.ReapStale(ctx, 0)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n < 1 {
		t.Fatalf("reaped %d, want >= 1", n)
	}
	if got, _ := st.Get(ctx, inv.ID); got.Status != "failed" {
		t.Fatalf("status after reap = %q, want failed", got.Status)
	}
	if c, _ := st.CountInFlight(ctx, agent); c != 0 {
		t.Fatalf("in-flight after reap = %d, want 0", c)
	}
	if countOutcome(t, pool, "invocation:"+inv.ID, "tool.result:error") != 1 {
		t.Fatal("reap must emit exactly one tool.result:error")
	}
}

// A granted invocation (decision=allow, still pending_approval so the agent can claim
// it) must drop OUT of the operator pending list immediately — otherwise the cockpit
// re-prompts in the window between approve and the agent's claim (T-044).
func TestListPendingExcludesGranted(t *testing.T) {
	st, _ := testStore(t)
	ctx := context.Background()
	inv, _, err := st.Create(ctx, CreateInput{
		AgentSubject: "agent:" + randSuffix(t), ResourceID: "r-tp", Tool: "remote.exec", Command: "echo hi",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Park it with the policy decision, as the orchestrator does.
	if err := st.SetStatus(ctx, inv.ID, "pending_approval", "requires_approval"); err != nil {
		t.Fatalf("park: %v", err)
	}
	if !listHasInvocation(t, st, inv.ID) {
		t.Fatal("a parked invocation must appear in ListPending")
	}

	// Operator approves -> decision flips to allow (status stays pending_approval).
	if ok, err := st.Grant(ctx, inv.ID); err != nil || !ok {
		t.Fatalf("grant: ok=%v err=%v", ok, err)
	}
	if listHasInvocation(t, st, inv.ID) {
		t.Fatal("a granted invocation must NOT appear in ListPending (T-044)")
	}
	// It is still directly retrievable — the agent claims it via Get/ClaimForExec.
	if got, _ := st.Get(ctx, inv.ID); got.Status != "pending_approval" || got.Decision != "allow" {
		t.Fatalf("granted invocation state = %+v, want pending_approval/allow", got)
	}
}

func listHasInvocation(t *testing.T, st *Store, id string) bool {
	t.Helper()
	list, err := st.ListPending(context.Background())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	for _, inv := range list {
		if inv.ID == id {
			return true
		}
	}
	return false
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
