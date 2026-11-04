//go:build integration

package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// T-176c1: a GENERATED credential is ciphertext at rest, in both tables, exactly
// as an imported one is (ADR-0053): the stored column holds no plaintext key bytes,
// the right key decrypts it, and a wrong key cannot.
func TestGeneratedCredentialIsCiphertextAtRest(t *testing.T) {
	pool, ctx, id := testPool(t), context.Background(), mintID(t)
	label := "t176c1-" + id
	cleanup(t, pool, id, label)
	cleanup(t, pool, mintID(t), "")
	if _, err := Generate(ctx, pool, id, testKey, testActor, false); err != nil {
		t.Fatal(err)
	}
	if _, err := GeneratePending(ctx, pool, label, "aireos-demo", "ssh-ed25519 AAAAhost", testKey, testActor); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, q string }{
		{"connector_credentials.connection_secret", `SELECT connection_secret FROM connector_credentials WHERE resource_id=$1`},
		{"connector_pending_credentials.private_secret", `SELECT private_secret FROM connector_pending_credentials WHERE label=$1`},
	} {
		arg := id
		if strings.Contains(c.q, "pending") {
			arg = label
		}
		var blob []byte
		if err := pool.QueryRow(ctx, c.q, arg).Scan(&blob); err != nil || len(blob) == 0 {
			t.Fatalf("%s: read %v (len %d)", c.name, err, len(blob))
		}
		for _, plain := range []string{"private_key", "OPENSSH PRIVATE KEY", "BEGIN"} {
			if bytes.Contains(blob, []byte(plain)) {
				t.Errorf("%s holds plaintext %q", c.name, plain)
			}
		}
		var out []byte
		if err := pool.QueryRow(ctx, `SELECT pgp_sym_decrypt($1::bytea, $2)`, blob, testKey).Scan(&out); err != nil || !bytes.Contains(out, []byte("OPENSSH PRIVATE KEY")) {
			t.Errorf("%s: the right key must decrypt to the key material: %v", c.name, err)
		}
		if err := pool.QueryRow(ctx, `SELECT pgp_sym_decrypt($1::bytea, $2)`, blob, testKey+"-wrong").Scan(&out); err == nil {
			t.Errorf("%s: a WRONG key decrypted the column", c.name)
		}
	}
}
