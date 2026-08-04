//go:build integration

// Integration tests for the pairing store against a real Postgres (T-113 G1 / VER-6).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/pairing/...
package pairing

import (
	"context"
	"crypto/rand"
	"os"
	"sync"
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
	return NewStore(pool, "api_outbox")
}

func mustCreate(t *testing.T, s *Store, operator, label string) (Request, []byte) {
	t.Helper()
	req, secret, err := s.Create(context.Background(), CreateInput{Operator: operator, Label: label})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return req, secret
}

// TestRedeemSingleUseUnderConcurrency is the ticket's named G1 / VER-6 live test
// (GOV-12): two goroutines race Redeem against the SAME request id, with the SAME
// correct secret, against real Postgres. The SELECT ... FOR UPDATE inside
// RedeemInTx must serialize them so exactly one succeeds and the other is refused —
// without it the race is real and both could succeed.
func TestRedeemSingleUseUnderConcurrency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:racer", "phone")

	const attempts = 2
	var wg sync.WaitGroup
	oks := make([]bool, attempts)
	errs := make([]error, attempts)
	sessIDs := make([]string, attempts)
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func(i int) {
			defer wg.Done()
			sess, ok, err := s.Redeem(ctx, req.ID, secret)
			oks[i] = ok
			errs[i] = err
			sessIDs[i] = sess.ID
		}(i)
	}
	wg.Wait()

	successes := 0
	for i := 0; i < attempts; i++ {
		if errs[i] != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i, errs[i])
		}
		if oks[i] {
			successes++
			if sessIDs[i] == "" {
				t.Fatalf("attempt %d: ok but no session id", i)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent redeem: got %d successes, want exactly 1", successes)
	}

	// A third, sequential redeem attempt (now certainly consumed) is also refused.
	if _, ok, err := s.Redeem(ctx, req.ID, secret); err != nil || ok {
		t.Fatalf("post-race redeem: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestRedeemWrongSecret — a wrong secret is refused and increments attempts,
// without consuming the request (a subsequent correct secret still works).
func TestRedeemWrongSecret(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:alice", "phone")

	wrong := make([]byte, 32)
	if _, err := rand.Read(wrong); err != nil {
		t.Fatalf("rand: %v", err)
	}

	if _, ok, err := s.Redeem(ctx, req.ID, wrong); err != nil || ok {
		t.Fatalf("wrong secret: ok=%v err=%v, want ok=false err=nil", ok, err)
	}

	updated, err := getRequest(t, s, req.ID)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if updated.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", updated.Attempts)
	}
	if updated.ConsumedAt != nil {
		t.Fatal("a single wrong secret must not consume the request")
	}

	// The correct secret still redeems successfully afterward.
	sess, ok, err := s.Redeem(ctx, req.ID, secret)
	if err != nil || !ok {
		t.Fatalf("correct secret after one wrong attempt: ok=%v err=%v", ok, err)
	}
	if sess.Operator != "operator:alice" || sess.Scope != ApprovalsScopeV1 {
		t.Fatalf("unexpected session: %+v", sess)
	}
}

// TestRedeemAttemptCapBurnsRequest — three wrong secrets consume the request (the
// failed-attempt cap, §6); a subsequent CORRECT secret is then refused too.
func TestRedeemAttemptCapBurnsRequest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:bob", "phone")

	wrong := make([]byte, 32)
	if _, err := rand.Read(wrong); err != nil {
		t.Fatalf("rand: %v", err)
	}

	for i := 0; i < maxFailedAttempts; i++ {
		if _, ok, err := s.Redeem(ctx, req.ID, wrong); err != nil || ok {
			t.Fatalf("wrong attempt %d: ok=%v err=%v", i, ok, err)
		}
	}

	updated, err := getRequest(t, s, req.ID)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if updated.Attempts != maxFailedAttempts {
		t.Fatalf("attempts = %d, want %d", updated.Attempts, maxFailedAttempts)
	}
	if updated.ConsumedAt == nil {
		t.Fatal("the maxFailedAttempts-th wrong secret must burn (consume) the request")
	}

	// Even the CORRECT secret is refused now — the request is burned.
	if _, ok, err := s.Redeem(ctx, req.ID, secret); err != nil || ok {
		t.Fatalf("correct secret after burn: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestRedeemExpired — a request past its expires_at is refused.
func TestRedeemExpired(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:carol", "phone")

	if _, err := s.pool.Exec(ctx, `UPDATE pairing_requests SET expires_at = now() - interval '1 second' WHERE id = $1`, req.ID); err != nil {
		t.Fatalf("force expiry: %v", err)
	}

	if _, ok, err := s.Redeem(ctx, req.ID, secret); err != nil || ok {
		t.Fatalf("expired redeem: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestRedeemAlreadyConsumed — a second redeem of an already-consumed request is
// refused (single-use, GOV-12).
func TestRedeemAlreadyConsumed(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:dave", "phone")

	if _, ok, err := s.Redeem(ctx, req.ID, secret); err != nil || !ok {
		t.Fatalf("first redeem: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.Redeem(ctx, req.ID, secret); err != nil || ok {
		t.Fatalf("second redeem: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestGetLiveLifecycle — GetLive returns the row when live; refuses a revoked row
// and an expired row alike (SEC-34's substrate).
func TestGetLiveLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	req1, sec1 := mustCreate(t, s, "operator:erin", "tablet")
	sess, ok, err := s.Redeem(ctx, req1.ID, sec1)
	if err != nil || !ok {
		t.Fatalf("redeem: ok=%v err=%v", ok, err)
	}

	live, err := s.GetLive(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get live: %v", err)
	}
	if live.ID != sess.ID || live.Operator != "operator:erin" {
		t.Fatalf("unexpected live session: %+v", live)
	}

	// Revoked → refused.
	if ok, err := s.Revoke(ctx, sess.ID); err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	if _, err := s.GetLive(ctx, sess.ID); err != ErrNotFound {
		t.Fatalf("get live after revoke: want ErrNotFound, got %v", err)
	}

	// Expired (never revoked) → refused.
	req3, sec3 := mustCreate(t, s, "operator:erin", "watch")
	sess2, ok, err := s.Redeem(ctx, req3.ID, sec3)
	if err != nil || !ok {
		t.Fatalf("redeem 2: ok=%v err=%v", ok, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE paired_sessions SET expires_at = now() - interval '1 second' WHERE id = $1`, sess2.ID); err != nil {
		t.Fatalf("force expiry: %v", err)
	}
	if _, err := s.GetLive(ctx, sess2.ID); err != ErrNotFound {
		t.Fatalf("get live after expiry: want ErrNotFound, got %v", err)
	}

	// Unknown id → refused.
	if _, err := s.GetLive(ctx, "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("get live unknown: want ErrNotFound, got %v", err)
	}
}

// TestRevokeIdempotent — Revoke flips revoked_at once; a second call is a no-op
// (ok=false, err=nil), mirroring grant.Revoke.
func TestRevokeIdempotent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	req, secret := mustCreate(t, s, "operator:frank", "phone")
	sess, ok, err := s.Redeem(ctx, req.ID, secret)
	if err != nil || !ok {
		t.Fatalf("redeem: ok=%v err=%v", ok, err)
	}

	if ok, err := s.Revoke(ctx, sess.ID); err != nil || !ok {
		t.Fatalf("first revoke: ok=%v err=%v", ok, err)
	}
	if ok, err := s.Revoke(ctx, sess.ID); err != nil || ok {
		t.Fatalf("second revoke: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
	if ok, err := s.Revoke(ctx, "00000000-0000-0000-0000-000000000000"); err != nil || ok {
		t.Fatalf("revoke unknown: ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

// TestListForOperatorScoped — ListForOperator returns only that operator's
// sessions, mirroring grant.ListForOperator's owner scoping.
func TestListForOperatorScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	reqA, secA := mustCreate(t, s, "operator:grace", "phone-a")
	sessA, ok, err := s.Redeem(ctx, reqA.ID, secA)
	if err != nil || !ok {
		t.Fatalf("redeem a: ok=%v err=%v", ok, err)
	}
	reqB, secB := mustCreate(t, s, "operator:heidi", "phone-b")
	if _, ok, err := s.Redeem(ctx, reqB.ID, secB); err != nil || !ok {
		t.Fatalf("redeem b: ok=%v err=%v", ok, err)
	}

	graceSessions, _, err := s.ListForOperator(ctx, "operator:grace", "", 100)
	if err != nil {
		t.Fatalf("list grace: %v", err)
	}
	if !containsSession(graceSessions, sessA.ID) {
		t.Fatalf("grace's session %s not in her own list", sessA.ID)
	}
	for _, sess := range graceSessions {
		if sess.Operator != "operator:grace" {
			t.Fatalf("heidi's session %s leaked into grace's list", sess.ID)
		}
	}
}

// TestCleanupRequests — CleanupRequests removes consumed and expired rows and
// leaves live ones (PERF-13).
func TestCleanupRequests(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	consumedReq, consumedSecret := mustCreate(t, s, "operator:ivan", "consumed")
	if _, ok, err := s.Redeem(ctx, consumedReq.ID, consumedSecret); err != nil || !ok {
		t.Fatalf("redeem consumed: ok=%v err=%v", ok, err)
	}

	expiredReq, _ := mustCreate(t, s, "operator:ivan", "expired")
	if _, err := s.pool.Exec(ctx, `UPDATE pairing_requests SET expires_at = now() - interval '1 second' WHERE id = $1`, expiredReq.ID); err != nil {
		t.Fatalf("force expiry: %v", err)
	}

	liveReq, _ := mustCreate(t, s, "operator:ivan", "live")

	n, err := s.CleanupRequests(ctx, time.Now())
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n < 2 {
		t.Fatalf("cleanup removed %d rows, want >= 2 (consumed + expired)", n)
	}

	if _, err := getRequest(t, s, consumedReq.ID); err == nil {
		t.Fatal("consumed request survived cleanup")
	}
	if _, err := getRequest(t, s, expiredReq.ID); err == nil {
		t.Fatal("expired request survived cleanup")
	}
	if _, err := getRequest(t, s, liveReq.ID); err != nil {
		t.Fatalf("live request was removed by cleanup: %v", err)
	}
}

// getRequest is a test-only direct read of pairing_requests (the package exposes
// no public Get for requests — only the redeem path touches them by design).
func getRequest(t *testing.T, s *Store, id string) (Request, error) {
	t.Helper()
	var r Request
	err := s.pool.QueryRow(context.Background(), `
		SELECT id, operator, label, expires_at, consumed_at, attempts, created_at
		  FROM pairing_requests WHERE id = $1`, id).
		Scan(&r.ID, &r.Operator, &r.Label, &r.ExpiresAt, &r.ConsumedAt, &r.Attempts, &r.CreatedAt)
	return r, err
}

func containsSession(sessions []Session, id string) bool {
	for _, sess := range sessions {
		if sess.ID == id {
			return true
		}
	}
	return false
}
