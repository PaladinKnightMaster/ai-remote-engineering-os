//go:build integration

// Integration tests for connector-service credential custody against a real
// Postgres (pgcrypto ships with postgres:16). Both api's resources table and
// connector's connector_credentials table must exist:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export CONNECTOR_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up                 # creates resources (+ connection_secret)
//	go run ./services/connector-service migrate up    # creates connector_credentials
//	go test -tags integration ./services/connector-service/internal/store/...
package store

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const testKey = "connector-round-trip-key-0123456789ab"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CONNECTOR_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("CONNECTOR_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mintID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint id: %v", err)
	}
	return id.String()
}

func TestSetGetRoundTrip(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id) })

	in := Material{PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----\n", Passphrase: "hunter2"}
	if err := Set(ctx, pool, id, testKey, in); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := Get(ctx, pool, id, testKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.PrivateKey != in.PrivateKey || out.Passphrase != in.Passphrase {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

// TestDeleteRemovesAndIsIdempotent covers store.Delete (T-118b --undo): a set
// credential is unreadable after delete, and a second delete against the same
// (now-absent) resource_id succeeds rather than erroring.
func TestDeleteRemovesAndIsIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id) })

	if err := Set(ctx, pool, id, testKey, Material{PrivateKey: "k"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := Delete(ctx, pool, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := Get(ctx, pool, id, testKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Idempotent: deleting again (nothing to delete) must not error.
	if err := Delete(ctx, pool, id); err != nil {
		t.Fatalf("second delete (idempotent) failed: %v", err)
	}
	// Deleting a resource-id that was never set must also not error.
	if err := Delete(ctx, pool, mintID(t)); err != nil {
		t.Fatalf("delete on never-set resource-id failed: %v", err)
	}
}

func TestSetUpsertReplaces(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id) })

	if err := Set(ctx, pool, id, testKey, Material{PrivateKey: "first"}); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	if err := Set(ctx, pool, id, testKey, Material{PrivateKey: "second"}); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	out, err := Get(ctx, pool, id, testKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.PrivateKey != "second" {
		t.Fatalf("expected upsert to replace, got %q", out.PrivateKey)
	}
}

func TestWrongKeyFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id) })

	if err := Set(ctx, pool, id, "the-right-key", Material{PrivateKey: "k"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := Get(ctx, pool, id, "the-wrong-key"); err == nil {
		t.Fatal("expected decrypt error with wrong key, got nil")
	}
}

func TestStoredCiphertextNotPlaintext(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id) })

	const marker = "PLAINTEXT-MARKER-SHOULD-NOT-APPEAR"
	if err := Set(ctx, pool, id, testKey, Material{PrivateKey: marker}); err != nil {
		t.Fatalf("set: %v", err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT connection_secret FROM connector_credentials WHERE resource_id=$1`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw column: %v", err)
	}
	if strings.Contains(string(raw), marker) {
		t.Fatal("plaintext marker found in stored ciphertext — column is not encrypted")
	}
}

func TestGetWhenAbsent(t *testing.T) {
	pool := testPool(t)
	if _, err := Get(context.Background(), pool, mintID(t), testKey); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound when no credential stored, got %v", err)
	}
}

// TestBackfillVerbatimAndIdempotent proves the "expand" leg of the ADR-0053
// relocation: existing ciphertext on the legacy api-owned
// resources.connection_secret column is copied byte-for-byte (no
// decrypt/re-encrypt) into connector custody, and a re-run is a no-op.
func TestBackfillVerbatimAndIdempotent(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM resources WHERE id=$1`, id)
	})

	// Seed a legacy resources row with an encrypted secret (as api would have).
	const legacyJSON = `{"private_key":"LEGACY-KEY","passphrase":"pp"}`
	if _, err := pool.Exec(ctx,
		`INSERT INTO resources (id, type, name, connection_secret)
		 VALUES ($1,'ssh','legacy-target', pgp_sym_encrypt($2::text,$3))`,
		id, legacyJSON, testKey); err != nil {
		t.Fatalf("seed legacy resource: %v", err)
	}

	n, err := BackfillFromResources(ctx, pool)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected at least 1 row backfilled, got %d", n)
	}

	// Decrypts to the original material under connector custody.
	out, err := Get(ctx, pool, id, testKey)
	if err != nil {
		t.Fatalf("get after backfill: %v", err)
	}
	if out.PrivateKey != "LEGACY-KEY" || out.Passphrase != "pp" {
		t.Fatalf("backfilled material mismatch: %+v", out)
	}

	// Verbatim: the copied ciphertext equals the source bytes exactly.
	var src, dst []byte
	if err := pool.QueryRow(ctx, `SELECT connection_secret FROM resources WHERE id=$1`, id).Scan(&src); err != nil {
		t.Fatalf("read source ciphertext: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT connection_secret FROM connector_credentials WHERE resource_id=$1`, id).Scan(&dst); err != nil {
		t.Fatalf("read copied ciphertext: %v", err)
	}
	if !bytes.Equal(src, dst) {
		t.Fatal("copied ciphertext is not byte-identical to the source — backfill is not a verbatim copy")
	}

	// Idempotent: a second run inserts nothing.
	n2, err := BackfillFromResources(ctx, pool)
	if err != nil {
		t.Fatalf("backfill re-run: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 rows on idempotent re-run, got %d", n2)
	}
}

// TestLeaseReturnsMaterialAndAuditsEgress proves the lease returns only the
// secret material and atomically writes an egress-decision audit row carrying no
// secret bytes (ADR-0053 / ADR-0045).
func TestLeaseReturnsMaterialAndAuditsEgress(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target' = $1`, "resource:"+id)
	})

	in := Material{PrivateKey: "LEASED-SECRET-KEY", Passphrase: "pp"}
	if err := Set(ctx, pool, id, testKey, in); err != nil {
		t.Fatalf("set: %v", err)
	}

	out, err := Lease(ctx, pool, id, "sess-123", "operator:admin", testKey)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	if out.PrivateKey != in.PrivateKey || out.Passphrase != in.Passphrase {
		t.Fatalf("lease material mismatch: %+v", out)
	}

	var actor, edge, target, outcome, evidence string
	if err := pool.QueryRow(ctx, `
		SELECT payload->>'actor', payload->>'edge', payload->>'target',
		       payload->>'outcome', payload->>'evidencePacketId'
		  FROM connector_outbox
		 WHERE payload->>'target' = $1
		 ORDER BY id DESC LIMIT 1`, "resource:"+id).Scan(&actor, &edge, &target, &outcome, &evidence); err != nil {
		t.Fatalf("read egress event: %v", err)
	}
	if actor != "operator:admin" || edge != "agent-remote-session" ||
		target != "resource:"+id || outcome != "credential.leased" || evidence != "session:sess-123" {
		t.Fatalf("egress event fields wrong: actor=%q edge=%q target=%q outcome=%q evidence=%q",
			actor, edge, target, outcome, evidence)
	}

	var raw string
	if err := pool.QueryRow(ctx, `SELECT payload::text FROM connector_outbox WHERE payload->>'target' = $1 ORDER BY id DESC LIMIT 1`, "resource:"+id).Scan(&raw); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if strings.Contains(raw, "LEASED-SECRET-KEY") {
		t.Fatal("secret leaked into the audit payload — the chain must carry no credential bytes")
	}
}

func TestLeaseAbsent(t *testing.T) {
	pool := testPool(t)
	if _, err := Lease(context.Background(), pool, mintID(t), "s", "operator:admin", testKey); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing credential, got %v", err)
	}
}
