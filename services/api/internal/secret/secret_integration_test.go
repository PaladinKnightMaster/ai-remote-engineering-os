//go:build integration

// Integration tests for the pgcrypto resource-secret helper against a real
// Postgres (pgcrypto ships with postgres:16).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/secret/...
package secret

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

// seedResource inserts a minimal ssh resource and returns its id.
func seedResource(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint id: %v", err)
	}
	_, err = pool.Exec(context.Background(),
		`INSERT INTO resources (id, type, name) VALUES ($1,'ssh','secret-target')`, id.String())
	if err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM resources WHERE id=$1`, id.String())
	})
	return id.String()
}

func TestSecretRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := seedResource(t, pool)
	const key = "round-trip-key-0123456789abcdef"

	in := Material{PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n", Passphrase: "hunter2"}
	if err := Set(ctx, pool, id, key, in); err != nil {
		t.Fatalf("set: %v", err)
	}

	out, err := Get(ctx, pool, id, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.PrivateKey != in.PrivateKey || out.Passphrase != in.Passphrase {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

func TestSecretWrongKeyFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := seedResource(t, pool)

	if err := Set(ctx, pool, id, "the-right-key", Material{PrivateKey: "k"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := Get(ctx, pool, id, "the-wrong-key"); err == nil {
		t.Fatal("expected decrypt error with wrong key, got nil")
	}
}

func TestSecretStoredCiphertextNotPlaintext(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := seedResource(t, pool)
	const marker = "PLAINTEXT-MARKER-SHOULD-NOT-APPEAR"

	if err := Set(ctx, pool, id, "k", Material{PrivateKey: marker}); err != nil {
		t.Fatalf("set: %v", err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx,
		`SELECT connection_secret FROM resources WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw column: %v", err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatal("plaintext marker found in stored ciphertext — column is not encrypted")
	}
}

func TestSecretSetUnknownResource(t *testing.T) {
	pool := testPool(t)
	missing, _ := uuid.NewV7()
	if err := Set(context.Background(), pool, missing.String(), "k", Material{PrivateKey: "k"}); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for unknown resource, got %v", err)
	}
}

func TestSecretGetWhenAbsent(t *testing.T) {
	pool := testPool(t)
	id := seedResource(t, pool) // seeded with no secret set
	if _, err := Get(context.Background(), pool, id, "k"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound when no secret stored, got %v", err)
	}
}
