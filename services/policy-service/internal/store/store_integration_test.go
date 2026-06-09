//go:build integration

// Integration tests for the policy-service store against a real Postgres with the
// policy-service migration applied:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export POLICY_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/policy-service migrate up
//	go test -tags integration ./services/policy-service/internal/store/...
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
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
	return New(pool), pool
}

// A fresh session id (the api session UUID) per test keeps runs independent
// (session_id is UNIQUE).
func newSession(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u.String()
}

func input(sessionID string) CreatePendingInput {
	return CreatePendingInput{
		SessionID: sessionID, Subject: "operator:admin", Edge: "agent-remote-session",
		Target: "resource:r-" + sessionID[len(sessionID)-8:], Action: "session.start",
	}
}

// outcomesFor returns the policy_outbox outcomes for a session, in order.
func outcomesFor(t *testing.T, pool *pgxpool.Pool, sessionID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT payload->>'outcome' FROM policy_outbox
		 WHERE payload->>'evidencePacketId' = $1 ORDER BY id`, "session:"+sessionID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, o)
	}
	return out
}

// TestCreatePendingEmitsDecisionAndIsIdempotent — a decide parks a pending row +
// emits exactly one policy.decision:requires_approval; a duplicate decide returns
// the same row and emits NO second event.
func TestCreatePendingEmitsDecisionAndIsIdempotent(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	sid := newSession(t)

	pa, err := s.CreatePending(ctx, input(sid), 120*time.Second)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if pa.Status != "pending" || pa.SessionID != sid {
		t.Fatalf("unexpected row: %+v", pa)
	}
	if pa.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expires_at not in the future: %v", pa.ExpiresAt)
	}

	again, err := s.CreatePending(ctx, input(sid), 120*time.Second)
	if err != nil {
		t.Fatalf("idempotent create: %v", err)
	}
	if again.ID != pa.ID {
		t.Fatalf("idempotent create returned a different row: %s != %s", again.ID, pa.ID)
	}
	if got := outcomesFor(t, pool, sid); len(got) != 1 || got[0] != "policy.decision:requires_approval" {
		t.Fatalf("want exactly one decision event, got %v", got)
	}
}

// TestResolveGrantedEmitsApproval — resolve(granted) flips the row + emits
// approval.granted; a second resolve is ErrNotPending (cannot re-flip).
func TestResolveGrantedEmitsApproval(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	sid := newSession(t)

	if _, err := s.CreatePending(ctx, input(sid), 120*time.Second); err != nil {
		t.Fatalf("create: %v", err)
	}
	pa, err := s.Resolve(ctx, sid, "granted", "operator:admin")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if pa.Status != "granted" || pa.ResolvedBy != "operator:admin" || pa.ResolvedAt == nil {
		t.Fatalf("unexpected resolved row: %+v", pa)
	}
	if _, err := s.Resolve(ctx, sid, "denied", "operator:admin"); err != ErrNotPending {
		t.Fatalf("double resolve: want ErrNotPending, got %v", err)
	}
	got := outcomesFor(t, pool, sid)
	if len(got) != 2 || got[0] != "policy.decision:requires_approval" || got[1] != "approval.granted" {
		t.Fatalf("want decision then approval.granted, got %v", got)
	}
}

// TestExpireTimedOutFailsClosed — an un-actioned approval past its expiry is
// auto-denied with reason=timeout and emits approval.denied:timeout attributed to
// the system sweeper actor.
func TestExpireTimedOutFailsClosed(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	sid := newSession(t)

	if _, err := s.CreatePending(ctx, input(sid), 120*time.Second); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Sweep with a clock well past the TTL — no need to wait.
	n, err := s.ExpireTimedOut(ctx, time.Now().Add(10*time.Minute))
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n < 1 {
		t.Fatalf("want >=1 expired, got %d", n)
	}
	pa, err := s.Get(ctx, sid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if pa.Status != "denied" || pa.Reason != "timeout" || pa.ResolvedBy != "system:policy-timeout" {
		t.Fatalf("unexpected expired row: %+v", pa)
	}
	got := outcomesFor(t, pool, sid)
	if len(got) != 2 || got[1] != "approval.denied:timeout" {
		t.Fatalf("want decision then approval.denied:timeout, got %v", got)
	}
	// Already-resolved rows are not re-expired.
	if again, _ := s.ExpireTimedOut(ctx, time.Now().Add(10*time.Minute)); again != 0 {
		t.Fatalf("second sweep should be a no-op, expired %d", again)
	}
}
