//go:build integration

// Integration tests for the store against a real Postgres. They are the
// ADR-0045 §Verification concurrent-append integrity test and the dedup test.
//
// Run them where the Go toolchain and a Postgres are available:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export AUDIT_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
//	go run ./services/audit-service migrate up   # apply 0001_init
//	go test -tags integration ./services/audit-service/internal/store/...
package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("AUDIT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AUDIT_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool)
}

func ingest(key string) audit.Ingest {
	return audit.Ingest{
		Timestamp: "2026-05-20T00:00:00Z", Actor: "svc:test",
		Edge: "service-service", Target: "t:" + key, IdempotencyKey: key,
	}
}

// Concurrent appends serialize into a single linear chain with no forks/gaps.
func TestConcurrentAppendIntegrity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, _, err := s.Append(ctx, ingest(fmt.Sprintf("conc-%d", i))); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	events, _, err := s.List(ctx, 0, 1000)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) < n {
		t.Fatalf("expected >= %d events, got %d", n, len(events))
	}
	if idx := chain.Verify(events); idx != -1 {
		t.Fatalf("chain broken at index %d under concurrent append", idx)
	}
}

// A replayed idempotencyKey returns the stored event and does not extend the chain.
func TestDedupReplayIntegrity(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := ingest("dedup-key")

	first, deduped1, err := s.Append(ctx, in)
	if err != nil || deduped1 {
		t.Fatalf("first append: deduped=%v err=%v", deduped1, err)
	}
	second, deduped2, err := s.Append(ctx, in)
	if err != nil {
		t.Fatalf("replay append: %v", err)
	}
	if !deduped2 {
		t.Fatal("replay should be flagged deduped")
	}
	if first.ID != second.ID || first.ChainHash != second.ChainHash {
		t.Fatalf("replay returned a different event: %+v vs %+v", first, second)
	}
}
