//go:build integration

// Integration tests for the resource store against a real Postgres.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/store/...
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/api/internal/resource"
	"aireos.local/services/api/internal/session"
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
	return New(pool)
}

// newPendingSession lands a session in 'pending' status exactly the way
// production now does (createGoverned, server.go) for an allow decision:
// ParkForApproval then ApproveSession. SEC-59 (T-151c) removed the store's
// old direct-to-'pending' CreateSession — it was an ungoverned second path
// to the same status these two calls reach properly — so every fixture in
// this file that needs a 'pending' (not 'pending_approval') row goes through
// here now.
func newPendingSession(t *testing.T, s *Store, ctx context.Context, operatorID string, in session.CreateInput) session.Session {
	t.Helper()
	parked, err := s.ParkForApproval(ctx, operatorID, in)
	if err != nil {
		t.Fatalf("park for approval: %v", err)
	}
	approved, err := s.ApproveSession(ctx, operatorID, parked.ID)
	if err != nil {
		t.Fatalf("approve session: %v", err)
	}
	return approved
}

func TestResourceCRUDRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	in := resource.Input{Type: "ssh", Name: "edge-node", SiteID: "site-eu", Labels: map[string]string{"env": "prod"}}
	created, err := s.Create(ctx, "operator:crud-test", in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Type != "ssh" {
		t.Fatalf("unexpected created: %+v", created)
	}

	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "edge-node" || got.Labels["env"] != "prod" || got.SiteID != "site-eu" {
		t.Fatalf("get mismatch: %+v", got)
	}

	items, _, err := s.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one resource")
	}

	if err := s.Delete(ctx, "operator:crud-test", created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := s.Delete(ctx, "operator:crud-test", created.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound deleting twice, got %v", err)
	}
}

// TestSessionCRUDRoundTrip exercises the cockpit-session store against a real
// Postgres: FK 404 on unknown resource, pending create, operator-scoped get/list,
// the end transition (closed/operator/ended_at) and its idempotency, plus the
// cross-operator isolation that is the v0.1.0 security boundary.
func TestSessionCRUDRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	// A real resource to satisfy the FK.
	res, err := s.Create(ctx, "operator:seed-test", resource.Input{Type: "ssh", Name: "ssh-target"})
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}

	const opA, opB = "operator:a", "operator:b"

	// Unknown resourceId → ErrNotFound (FK violation mapped). ParkForApproval
	// shares insertSession with the removed CreateSession, so the same
	// FK-violation mapping applies regardless of which status it inserts.
	if _, err := s.ParkForApproval(ctx, opA, session.CreateInput{
		ResourceID: "00000000-0000-0000-0000-000000000000", Mode: "terminal",
	}); err != ErrNotFound {
		t.Fatalf("create on unknown resource: want ErrNotFound, got %v", err)
	}

	created := newPendingSession(t, s, ctx, opA, session.CreateInput{ResourceID: res.ID, Mode: "terminal"})
	if created.ID == "" || created.Status != session.StatusPending || created.OperatorID != opA {
		t.Fatalf("unexpected created session: %+v", created)
	}
	if created.EndedAt != nil || created.ClosedReason != "" {
		t.Fatalf("pending session should have no end fields: %+v", created)
	}

	// Operator scope: owner sees it, a different operator does not.
	if got, err := s.GetSession(ctx, opA, created.ID); err != nil || got.ID != created.ID {
		t.Fatalf("owner get: %+v err=%v", got, err)
	}
	if _, err := s.GetSession(ctx, opB, created.ID); err != ErrNotFound {
		t.Fatalf("cross-operator get: want ErrNotFound, got %v", err)
	}

	items, _, err := s.ListSessions(ctx, opA, "", 100)
	if err != nil || len(items) == 0 {
		t.Fatalf("owner list: len=%d err=%v", len(items), err)
	}
	if other, _, _ := s.ListSessions(ctx, opB, "", 100); len(other) != 0 {
		t.Fatalf("cross-operator list: want empty, got %d", len(other))
	}

	// End transitions to closed/operator with ended_at stamped.
	ended, err := s.EndSession(ctx, opA, created.ID)
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if ended.Status != session.StatusClosed || ended.ClosedReason != session.ReasonOperator || ended.EndedAt == nil {
		t.Fatalf("end transition wrong: %+v", ended)
	}
	firstEnd := *ended.EndedAt

	// Idempotent re-end: still closed, ended_at unchanged (COALESCE keeps it).
	again, err := s.EndSession(ctx, opA, created.ID)
	if err != nil {
		t.Fatalf("re-end: %v", err)
	}
	if again.Status != session.StatusClosed || !again.EndedAt.Equal(firstEnd) {
		t.Fatalf("re-end not idempotent: %+v (first end %v)", again, firstEnd)
	}

	// Cross-operator end is a no-op → ErrNotFound.
	if _, err := s.EndSession(ctx, opB, created.ID); err != ErrNotFound {
		t.Fatalf("cross-operator end: want ErrNotFound, got %v", err)
	}
}

// TestResourceDeleteReferentialGuard proves T-118c's guard end to end against
// a real Postgres — a mocked FK/JOIN is not proof: a resource referenced by a
// non-terminal session, run, or workstream is refused (ErrInUse), never the
// raw FK-violation sessions would otherwise surface, and never the silent
// Run/Workstream orphan runs/workstreams would otherwise leave. Once a Run or
// a Workstream reaches its terminal state (verified against the CHECK
// constraints — completed/failed/canceled, retired), deletion succeeds — but
// NOT a session: this test caught a real gap in the brief's own framing
// ("non-terminal blocks, terminal allows" uniformly across all three) that a
// mocked test would have missed. sessions.resource_id has NO `ON DELETE`
// clause (migration 0002), so even a TERMINAL (closed) session row still
// physically blocks the DELETE at the database level — see the session
// sub-test below and Delete's doc comment for why that residual is accepted
// rather than "fixed" by deleting session history. Runs and workstreams carry
// resource_id on their STEP tables (run_steps/workstream_steps), not on the
// parent row, so those two legs are seeded with raw SQL directly against the
// tables rather than through the run/workstream packages (avoiding a
// resources->run/workstream package dependency for a test fixture only).
func TestResourceDeleteReferentialGuard(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const actor = "operator:guard-test"

	t.Run("session", func(t *testing.T) {
		res, err := s.Create(ctx, actor, resource.Input{Type: "ssh", Name: "guard-session"})
		if err != nil {
			t.Fatalf("seed resource: %v", err)
		}
		sess := newPendingSession(t, s, ctx, actor, session.CreateInput{ResourceID: res.ID, Mode: "terminal"})

		if err := s.Delete(ctx, actor, res.ID); err != ErrInUse {
			t.Fatalf("delete referenced by a pending session: want ErrInUse, got %v", err)
		}

		if _, err := s.EndSession(ctx, actor, sess.ID); err != nil {
			t.Fatalf("end session: %v", err)
		}
		// STILL ErrInUse, not success: the closed session row still holds the
		// hard FK. The pre-check no longer flags it (it only screens
		// non-terminal rows), so this proves the DELETE-attempt fallback
		// (23503 -> ErrInUse) actually engages, not just the pre-check.
		if err := s.Delete(ctx, actor, res.ID); err != ErrInUse {
			t.Fatalf("delete referenced by a TERMINAL session (still FK-blocked): want ErrInUse, got %v", err)
		}
	})

	t.Run("run", func(t *testing.T) {
		res, err := s.Create(ctx, actor, resource.Input{Type: "ssh", Name: "guard-run"})
		if err != nil {
			t.Fatalf("seed resource: %v", err)
		}
		runID := seedRun(t, s, "running", res.ID)

		if err := s.Delete(ctx, actor, res.ID); err != ErrInUse {
			t.Fatalf("delete referenced by a running Run: want ErrInUse, got %v", err)
		}

		if _, err := s.pool.Exec(ctx, `UPDATE runs SET status='completed' WHERE id=$1`, runID); err != nil {
			t.Fatalf("terminalize run: %v", err)
		}
		if err := s.Delete(ctx, actor, res.ID); err != nil {
			t.Fatalf("delete after the Run reached a terminal state: %v", err)
		}
	})

	t.Run("workstream", func(t *testing.T) {
		res, err := s.Create(ctx, actor, resource.Input{Type: "ssh", Name: "guard-workstream"})
		if err != nil {
			t.Fatalf("seed resource: %v", err)
		}
		wsID := seedWorkstream(t, s, "active", res.ID)

		if err := s.Delete(ctx, actor, res.ID); err != ErrInUse {
			t.Fatalf("delete referenced by an active Workstream: want ErrInUse, got %v", err)
		}

		if _, err := s.pool.Exec(ctx, `UPDATE workstreams SET status='paused' WHERE id=$1`, wsID); err != nil {
			t.Fatalf("pause workstream: %v", err)
		}
		if err := s.Delete(ctx, actor, res.ID); err != ErrInUse {
			t.Fatalf("delete referenced by a paused Workstream: want ErrInUse, got %v", err)
		}

		if _, err := s.pool.Exec(ctx, `UPDATE workstreams SET status='retired' WHERE id=$1`, wsID); err != nil {
			t.Fatalf("retire workstream: %v", err)
		}
		if err := s.Delete(ctx, actor, res.ID); err != nil {
			t.Fatalf("delete after the Workstream reached a terminal state: %v", err)
		}
	})
}

// TestResourceCreateChainsAuditEvent proves resource.created lands in the SAME
// transaction as the row insert (AUD-40 / T-131) — the symmetric case of
// TestResourceDeleteChainsAuditEvent below (T-118c). Update no longer exists
// (its sole route was removed at T-131 / B-035), so Create/Delete are now the
// whole of the resource lifecycle and both chain an event.
func TestResourceCreateChainsAuditEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, "operator:create-audit-test", resource.Input{Type: "ssh", Name: "create-audit-target"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'target' = $1 AND payload->>'outcome' = $2 AND payload->>'actor' = $3 AND payload->>'edge' = $4`,
		"resource:"+res.ID, "resource.created", "operator:create-audit-test", "human-agent").Scan(&n); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 resource.created event, got %d", n)
	}
}

// TestResourceDeleteChainsAuditEvent proves resource.deleted lands in the SAME
// transaction as the row delete (T-118c) — Create now chains resource.created
// too (AUD-40 / T-131, above).
func TestResourceDeleteChainsAuditEvent(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, "operator:audit-test", resource.Input{Type: "ssh", Name: "audit-target"})
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	if err := s.Delete(ctx, "operator:audit-test", res.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM api_outbox
		 WHERE payload->>'target' = $1 AND payload->>'outcome' = $2 AND payload->>'actor' = $3 AND payload->>'edge' = $4`,
		"resource:"+res.ID, "resource.deleted", "operator:audit-test", "human-agent").Scan(&n); err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 resource.deleted event, got %d", n)
	}
}

// seedRun inserts a minimal runs + run_steps row pair directly (raw SQL, not
// through internal/run) so the resources package need not depend on it just
// for a test fixture. grant_id/other run_steps columns are irrelevant to the
// guard, so only what the guard's query reads is populated.
func seedRun(t *testing.T, s *Store, status, resourceID string) string {
	t.Helper()
	ctx := context.Background()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint run id: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO runs (id, operator, status) VALUES ($1,$2,$3)`,
		id.String(), "operator:guard-test", status); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO run_steps (run_id, step_index, resource_id, command) VALUES ($1,0,$2,$3)`,
		id.String(), resourceID, []byte("noop")); err != nil {
		t.Fatalf("insert run_step: %v", err)
	}
	return id.String()
}

// seedWorkstream inserts a minimal workstreams + workstream_steps row pair
// directly (raw SQL, not through internal/workstream — same rationale as
// seedRun). grant_id carries no FK (migration 0011), so a fresh UUID with no
// backing delegation_grants row is sufficient for the guard's query.
func seedWorkstream(t *testing.T, s *Store, status, resourceID string) string {
	t.Helper()
	ctx := context.Background()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint workstream id: %v", err)
	}
	grantID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint grant id: %v", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO workstreams (id, principal, objective, grant_id, interval_seconds, next_fire_at, status)
		VALUES ($1,$2,$3,$4,$5, now() + interval '1 hour', $6)`,
		id.String(), "operator:guard-test", "guard-test objective", grantID.String(), 3600, status); err != nil {
		t.Fatalf("insert workstream: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO workstream_steps (workstream_id, step_index, resource_id, command) VALUES ($1,0,$2,$3)`,
		id.String(), resourceID, []byte("noop")); err != nil {
		t.Fatalf("insert workstream_step: %v", err)
	}
	return id.String()
}

// TestReapStale — the stuck-session reaper (T-032). A stale 'pending' and a stale
// 'pending_approval' session (past their cutoffs) are closed with ttl /
// approval_timeout; a fresh pending session and an OLD active session are left
// untouched (active is never reaped); a second sweep is a no-op.
func TestReapStale(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	res, err := s.Create(ctx, "operator:reap", resource.Input{Type: "ssh", Name: "reap-target"})
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	const op = "operator:reap"
	in := session.CreateInput{ResourceID: res.ID, Mode: "terminal"}

	backdate := func(id string, age time.Duration) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `UPDATE sessions SET started_at = $2 WHERE id = $1`, id, time.Now().Add(-age)); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}

	stalePending := newPendingSession(t, s, ctx, op, in)
	backdate(stalePending.ID, time.Hour)

	staleApproval, err := s.ParkForApproval(ctx, op, in)
	if err != nil {
		t.Fatalf("seed pending_approval: %v", err)
	}
	backdate(staleApproval.ID, 10*time.Minute)

	freshPending := newPendingSession(t, s, ctx, op, in)

	activeSess := newPendingSession(t, s, ctx, op, in)
	if _, err := s.pool.Exec(ctx, `UPDATE sessions SET status='active', started_at=$2 WHERE id=$1`, activeSess.ID, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("activate: %v", err)
	}

	n, err := s.ReapStale(ctx, time.Now().Add(-30*time.Minute), time.Now().Add(-5*time.Minute))
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n != 2 {
		t.Fatalf("reaped %d, want 2", n)
	}

	assertSession := func(id, wantStatus, wantReason string) {
		t.Helper()
		got, err := s.GetSession(ctx, op, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.Status != wantStatus || got.ClosedReason != wantReason {
			t.Fatalf("session %s: status=%q reason=%q, want %q/%q", id, got.Status, got.ClosedReason, wantStatus, wantReason)
		}
	}
	assertSession(stalePending.ID, session.StatusClosed, "ttl")
	assertSession(staleApproval.ID, session.StatusClosed, session.ReasonApprovalTimeout)
	assertSession(freshPending.ID, session.StatusPending, "")
	assertSession(activeSess.ID, session.StatusActive, "")

	if again, _ := s.ReapStale(ctx, time.Now().Add(-30*time.Minute), time.Now().Add(-5*time.Minute)); again != 0 {
		t.Fatalf("second sweep reaped %d, want 0", again)
	}
}
