//go:build integration

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/connector-service/internal/store"
)

const bindTestKey = "bind-pending-cli-key-0123456789abcd"

func bindTestPool(t *testing.T) *pgxpool.Pool {
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

// captureStdout runs f and returns what it wrote to stdout.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	return string(out)
}

// TestBindPendingCLI: the script path's bind (T-176c). Success moves the pending key onto the resource, audits it as
// system:setup, and prints NOTHING on stdout; an unknown label fails and writes nothing.
func TestBindPendingCLI(t *testing.T) {
	pool := bindTestPool(t)
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	label := "t176d-" + suffix
	rid := uuid.NewString()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, rid)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_pending_credentials WHERE label=$1`, label)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target' = $1`, "resource:"+rid)
	})

	t.Run("unknown label", func(t *testing.T) {
		err := bindPending(ctx, pool, []string{"--label", "no-such-" + suffix, "--resource-id", rid})
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM connector_credentials WHERE resource_id=$1`, rid).Scan(&n); err != nil || n != 0 {
			t.Fatalf("a refused bind left %d credential row(s) (err %v)", n, err)
		}
	})

	t.Run("success", func(t *testing.T) {
		pub, err := store.GeneratePending(ctx, pool, label, "aireos-demo", "ssh-ed25519 AAAAhostkey", bindTestKey, "system:setup")
		if err != nil {
			t.Fatalf("generate pending: %v", err)
		}
		var runErr error
		stdout := captureStdout(t, func() {
			runErr = bindPending(ctx, pool, []string{"--label", label, "--resource-id", rid})
		})
		if runErr != nil {
			t.Fatalf("bind-pending: %v", runErr)
		}
		if strings.TrimSpace(stdout) != "" {
			t.Fatalf("bind-pending wrote %q to stdout; it must print nothing there", stdout)
		}
		got, err := store.PublicKey(ctx, pool, rid)
		if err != nil || got.PublicKey != pub.PublicKey {
			t.Fatalf("bound key = %+v, %v; want the pending key %q", got, err, pub.PublicKey)
		}
		var pending int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM connector_pending_credentials WHERE label=$1`, label).Scan(&pending)
		if pending != 0 {
			t.Fatalf("the pending row survived the bind")
		}
		var actor, outcome string
		if err := pool.QueryRow(ctx, `SELECT payload->>'actor', payload->>'outcome' FROM connector_outbox WHERE payload->>'target'=$1`, "resource:"+rid).
			Scan(&actor, &outcome); err != nil {
			t.Fatalf("read audit event: %v", err)
		}
		if actor != "system:setup" || outcome != "credential.bound" {
			t.Fatalf("audit event = actor %q outcome %q, want system:setup / credential.bound", actor, outcome)
		}
	})
}
