//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
)

// Importing a key over a generated credential must not leave the OLD generated
// public half readable: the cockpit would show an authorized_keys line for a key
// the connector no longer holds (wrong-identity mapping).
func TestSetOverGeneratedClearsPublicKey(t *testing.T) {
	pool, ctx, id := testPool(t), context.Background(), mintID(t)
	cleanup(t, pool, id, "")
	k1, err := Generate(ctx, pool, id, testKey, testActor, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := PublicKey(ctx, pool, id); err != nil || got != k1 {
		t.Fatalf("before import: %+v, %v", got, err)
	}
	if err := Set(ctx, pool, id, testKey, Material{PrivateKey: "operator's own key"}); err != nil {
		t.Fatal(err)
	}
	if got, err := PublicKey(ctx, pool, id); !errors.Is(err, ErrNoPublicKey) {
		t.Fatalf("after import PublicKey = %+v, %v; want ErrNoPublicKey (stale K1 must not be served)", got, err)
	}
	// Delete takes the public half with the row; Backfill's DO NOTHING never touches an existing row.
	if err := Delete(ctx, pool, id); err != nil {
		t.Fatal(err)
	}
	if _, err := PublicKey(ctx, pool, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
