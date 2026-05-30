//go:build integration

// Integration tests for the outbox relay against a real Postgres.
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export OUTBOX_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go test -tags integration ./packages/go/outbox/...
//
// The test owns the canonical `outbox` table (drops + recreates it from the
// README template), so it must run against a disposable test database.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const ddl = `
DROP TRIGGER IF EXISTS outbox_notify_trg ON outbox;
DROP FUNCTION IF EXISTS outbox_notify();
DROP TABLE IF EXISTS outbox;
CREATE TABLE outbox (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key UUID        NOT NULL UNIQUE,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ
);
CREATE INDEX outbox_undelivered_idx ON outbox (id) WHERE delivered_at IS NULL;
CREATE FUNCTION outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER outbox_notify_trg
    AFTER INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION outbox_notify();
`

type fakeClient struct {
	mu   sync.Mutex
	got  [][]byte
	fail bool
}

func (c *fakeClient) Append(_ context.Context, p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("boom")
	}
	c.got = append(c.got, append([]byte(nil), p...))
	return nil
}

func (c *fakeClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.got)
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("OUTBOX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OUTBOX_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		t.Fatalf("ddl: %v", err)
	}
	return pool
}

func enqueueN(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i := 0; i < n; i++ {
		if _, err := (Writer{}).Enqueue(ctx, tx, Event{
			Timestamp: time.Now(), Actor: "operator:admin",
			Edge: "agent-remote-session", Target: "session:test", Outcome: "opened",
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func undelivered(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox WHERE delivered_at IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestRelayDrainDeliversAndMarks — a successful client drains all rows and stamps
// delivered_at; the drain loop clears a backlog larger than one batch.
func TestRelayDrainDeliversAndMarks(t *testing.T) {
	pool := testPool(t)
	enqueueN(t, pool, 5)

	client := &fakeClient{}
	r := &Relay{Pool: pool, Client: client, BatchSize: 2} // 5 rows / batch 2 => 3 batches
	n, err := r.drain(context.Background())
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if n != 5 || client.count() != 5 {
		t.Fatalf("delivered=%d clientGot=%d, want 5/5", n, client.count())
	}
	if u := undelivered(t, pool); u != 0 {
		t.Fatalf("undelivered after drain: %d, want 0", u)
	}
	// Each delivered payload is valid audit-ingest JSON.
	for _, p := range client.got {
		var got ingestShape
		if err := json.Unmarshal(p, &got); err != nil || got.IdempotencyKey == "" {
			t.Fatalf("bad payload: %s (err=%v)", p, err)
		}
	}
}

// TestRelayDrainLeavesRowsOnDeliveryFailure — a failing client rolls the batch
// back; rows stay pending for the next attempt (at-least-once).
func TestRelayDrainLeavesRowsOnDeliveryFailure(t *testing.T) {
	pool := testPool(t)
	enqueueN(t, pool, 3)

	r := &Relay{Pool: pool, Client: &fakeClient{fail: true}, BatchSize: 10}
	if _, err := r.drain(context.Background()); err == nil {
		t.Fatal("drain: want error from failing client, got nil")
	}
	if u := undelivered(t, pool); u != 3 {
		t.Fatalf("undelivered after failed drain: %d, want 3 (rolled back)", u)
	}
}
