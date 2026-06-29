//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	"aireos.local/services/mcp-gateway/internal/catalog"
	"aireos.local/services/mcp-gateway/internal/store"
)

// G1 (T-040a2): the operator resolve route drives the block-and-wait unblock end to
// end on real wires — an agent tools/call parks a flagged invocation, the held call
// blocks, the internal resolve route (service-service) grants, and the gateway claims
// + execs and returns the result to the still-held agent call. This proves the a1
// (waiter) + a2 (resolve plane) seam against a real Postgres. Run:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export MCP_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/mcp-gateway migrate up
//	go test -tags integration ./services/mcp-gateway/internal/server/...
func TestInvocationBlockAndWaitResolve(t *testing.T) {
	dsn := os.Getenv("MCP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MCP_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// RequiresApproval reads resources.labels (the broker pattern). The gateway
	// migration doesn't own that table; seed a minimal one + a flagged resource. The
	// full real-schema cross-service proof is the T-041 compose e2e.
	resourceID := "r-" + uuid.Must(uuid.NewV7()).String()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS resources (id TEXT PRIMARY KEY, labels JSONB)`); err != nil {
		t.Fatalf("ensure resources table: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO resources (id, labels) VALUES ($1, '{"requires_approval":"true"}')`, resourceID); err != nil {
		t.Fatalf("seed resource: %v", err)
	}

	gw := store.New(pool, nil) // plaintext command store — this test exercises the server, not F3
	brk := &fakeBroker{stdout: "APPROVED-OK\n"}
	pol := &fakePolicy{decision: "requires_approval"} // Decide parks; Resolve returns nil (operator authority faked)
	cat := catalog.New(catalog.Config{
		Store: gw, Policy: pol, Broker: brk, MaxInFlight: 4,
		WaitTimeout: 5 * time.Second, PollInterval: 20 * time.Millisecond,
	})
	resolver := catalog.NewResolver(gw, pol)

	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	signer := auth.NewSigner(key, "")
	h := New(Config{Verifier: v, Catalog: cat, Resolver: resolver})

	agentTok, err := signer.Issue(auth.IssueParams{
		Subject: "agent:e2e", Edge: "agent-tool", Audience: "mcp-gateway",
		Scope: auth.Scope{Resource: resourceID}, TTL: auth.AgentTTL,
	})
	if err != nil {
		t.Fatalf("agent token: %v", err)
	}
	svcTok, err := signer.IssueService("api", "mcp-gateway")
	if err != nil {
		t.Fatalf("service token: %v", err)
	}

	// The agent's tools/call blocks on approval — run it in the background (only the
	// goroutine-safe postMCP runs here; decodeRPC stays on the test goroutine).
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.exec",` +
			`"arguments":{"resourceId":"` + resourceID + `","command":"echo hi"}}}`
		done <- postMCP(t, h, body, agentTok)
	}()

	// Wait until the invocation has parked, then take its id from the operator list.
	var id string
	for i := 0; i < 100; i++ {
		rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", svcTok)
		var lst struct {
			Invocations []struct {
				ID             string `json:"id"`
				Command        string `json:"command"`
				IdempotencyKey string `json:"idempotencyKey"`
			} `json:"invocations"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &lst)
		if len(lst.Invocations) > 0 {
			id = lst.Invocations[0].ID
			if lst.Invocations[0].Command != "echo hi" {
				t.Fatalf("operator list command = %q, want echo hi (ADR-0059)", lst.Invocations[0].Command)
			}
			// An agent-tool invocation carries no Run idempotency key — so api derives
			// no FE-16 Run context for it (behavior guard: context is Run-steps only).
			if lst.Invocations[0].IdempotencyKey != "" {
				t.Fatalf("agent invocation idempotencyKey = %q, want empty", lst.Invocations[0].IdempotencyKey)
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("invocation never parked")
	}

	// Operator grants via the internal resolve route (service-service).
	rec := doReq(t, h, http.MethodPost, "/v1/invocations/"+id+"/resolve",
		`{"decision":"granted","resolvedBy":"operator:1"}`, svcTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve granted: status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The held agent call now unblocks with the executed result.
	select {
	case held := <-done:
		isErr, text := toolResult(t, decodeRPC(t, held))
		if isErr || text != "APPROVED-OK\n" {
			t.Fatalf("held call result: isError=%v text=%q", isErr, text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the held agent call did not unblock after the grant")
	}
	if !brk.called {
		t.Fatal("broker exec never ran after the grant")
	}
	if got, _ := gw.Get(ctx, id); got.Status != "completed" {
		t.Fatalf("invocation status = %q, want completed", got.Status)
	}
}
