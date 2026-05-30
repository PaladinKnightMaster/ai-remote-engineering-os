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

func TestResourceCRUDRoundTrip(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	in := resource.Input{Type: "ssh", Name: "edge-node", SiteID: "site-eu", Labels: map[string]string{"env": "prod"}}
	created, err := s.Create(ctx, in)
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

	upd, err := s.Update(ctx, created.ID, resource.Input{Type: "vnc", Name: "edge-node-2"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if upd.Type != "vnc" || upd.Name != "edge-node-2" {
		t.Fatalf("update mismatch: %+v", upd)
	}
	reread, _ := s.Get(ctx, created.ID)
	if reread.Type != "vnc" || reread.SiteID != "" || len(reread.Labels) != 0 {
		t.Fatalf("update not persisted/cleared: %+v", reread)
	}

	items, _, err := s.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one resource")
	}

	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	if err := s.Delete(ctx, created.ID); err != ErrNotFound {
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
	res, err := s.Create(ctx, resource.Input{Type: "ssh", Name: "ssh-target"})
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}

	const opA, opB = "operator:a", "operator:b"

	// Unknown resourceId → ErrNotFound (FK violation mapped).
	if _, err := s.CreateSession(ctx, opA, session.CreateInput{
		ResourceID: "00000000-0000-0000-0000-000000000000", Mode: "terminal",
	}); err != ErrNotFound {
		t.Fatalf("create on unknown resource: want ErrNotFound, got %v", err)
	}

	created, err := s.CreateSession(ctx, opA, session.CreateInput{ResourceID: res.ID, Mode: "terminal"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
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
