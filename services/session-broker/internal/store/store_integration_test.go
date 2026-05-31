//go:build integration

// Integration tests for the broker store against a real Postgres that has BOTH
// the api migrations (resources/sessions/pgcrypto) and the broker migration
// (outbox) applied:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export BROKER_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up && go run ./services/session-broker migrate up
//	go test -tags integration ./services/session-broker/internal/store/...
package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const secretKey = "broker-test-secret-key"

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("BROKER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("BROKER_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return New(pool), pool
}

// seed inserts an ssh resource (with ssh_* labels + an encrypted private key) and
// a pending session, returning their ids.
func seed(t *testing.T, pool *pgxpool.Pool) (sessionID, resourceID, operatorID string) {
	t.Helper()
	ctx := context.Background()
	resID := mustUUID(t)
	sessID := mustUUID(t)
	operatorID = "operator:" + sessID[:8]

	labels, _ := json.Marshal(map[string]string{
		"ssh_host":     "ssh-target",
		"ssh_port":     "2222",
		"ssh_user":     "spike",
		"ssh_host_key": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITESTKEY",
	})
	mat, _ := json.Marshal(material{PrivateKey: "-----BEGIN KEY-----\nabc\n-----END KEY-----\n"})
	if _, err := pool.Exec(ctx, `
		INSERT INTO resources (id, type, name, labels, connection_secret)
		VALUES ($1, 'ssh', 'broker-test', $2::jsonb, pgp_sym_encrypt($3::text, $4))`,
		resID, string(labels), string(mat), secretKey); err != nil {
		t.Fatalf("seed resource: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (id, resource_id, operator_id, mode, status)
		VALUES ($1, $2, $3, 'terminal', 'pending')`, sessID, resID, operatorID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sessID, resID, operatorID
}

func mustUUID(t *testing.T) string {
	t.Helper()
	u, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return u.String()
}

func TestLoadTargetDecryptsAndPins(t *testing.T) {
	s, pool := testStore(t)
	_, resID, _ := seed(t, pool)
	ctx := context.Background()

	tgt, err := s.LoadTarget(ctx, resID, secretKey)
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	if tgt.Host != "ssh-target" || tgt.Port != "2222" || tgt.User != "spike" {
		t.Fatalf("coordinates: %+v", tgt)
	}
	if !strings.HasPrefix(tgt.HostKey, "ssh-ed25519 ") {
		t.Fatalf("host key not pinned: %q", tgt.HostKey)
	}
	if !strings.Contains(tgt.PrivateKey, "BEGIN KEY") {
		t.Fatalf("private key not decrypted: %q", tgt.PrivateKey)
	}

	// Wrong key must surface as a decrypt error, never silent garbage.
	if _, err := s.LoadTarget(ctx, resID, "wrong-key"); err == nil {
		t.Fatal("load target with wrong key: want error, got nil")
	}
}

// TestLifecycleEmitsOpenAndClose — Activate then Close transition the session and
// enqueue exactly two outbox rows (session.open, then session.close with the cast
// SHA anchored in evidencePacketId). This is the audit half end-to-end (the relay
// then delivers these to the chain authority).
func TestLifecycleEmitsOpenAndClose(t *testing.T) {
	s, pool := testStore(t)
	sessID, _, operatorID := seed(t, pool)
	ctx := context.Background()

	if got, err := s.LoadSession(ctx, sessID); err != nil || got.Status != "pending" {
		t.Fatalf("load session: %+v err=%v", got, err)
	}

	if err := s.Activate(ctx, sessID, operatorID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got, _ := s.LoadSession(ctx, sessID); got.Status != "active" {
		t.Fatalf("status after activate: %q", got.Status)
	}
	// A second activate is a no-op transition (already active).
	if err := s.Activate(ctx, sessID, operatorID); err != ErrNotActivatable {
		t.Fatalf("re-activate: want ErrNotActivatable, got %v", err)
	}

	const castSHA = "abababababababababababababababababababababababababababababababab" // 64 hex
	if err := s.Close(ctx, sessID, operatorID, "operator", "/var/lib/aireos/sessions/"+sessID+".cast", castSHA); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got, _ := s.LoadSession(ctx, sessID); got.Status != "closed" {
		t.Fatalf("status after close: %q", got.Status)
	}
	if err := s.Close(ctx, sessID, operatorID, "operator", "", ""); err != ErrNotActivatable {
		t.Fatalf("re-close: want ErrNotActivatable, got %v", err)
	}

	// Exactly two evidence rows for this session, in order, with the cast SHA on
	// the close event only.
	rows, err := pool.Query(ctx, `
		SELECT payload->>'outcome', COALESCE(payload->>'evidencePacketId',''), payload->>'edge'
		  FROM outbox WHERE payload->>'target' = $1 ORDER BY id`, "session:"+sessID)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	type ev struct{ outcome, evidence, edge string }
	var got []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.outcome, &e.evidence, &e.edge); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, e)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 evidence rows, got %d: %+v", len(got), got)
	}
	if got[0].outcome != "opened" || got[0].evidence != "" || got[0].edge != auditEdge {
		t.Fatalf("open event wrong: %+v", got[0])
	}
	if got[1].outcome != "closed:operator" || got[1].evidence != "sha256:"+castSHA || got[1].edge != auditEdge {
		t.Fatalf("close event wrong: %+v", got[1])
	}
}
