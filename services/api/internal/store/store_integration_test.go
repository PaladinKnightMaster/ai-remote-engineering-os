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
