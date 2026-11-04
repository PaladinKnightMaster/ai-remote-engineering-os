//go:build integration

package store

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
)

const testActor = "operator:admin"

func cleanup(t *testing.T, pool *pgxpool.Pool, id, label string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM connector_credentials WHERE resource_id=$1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_pending_credentials WHERE label=$1`, label)
		_, _ = pool.Exec(ctx, `DELETE FROM connector_outbox WHERE payload->>'target' = $1`, "resource:"+id)
	})
}

// outcomes returns the outbox outcomes for a resource, oldest first, plus the
// concatenated raw payloads (for leak searches).
func outcomes(t *testing.T, pool *pgxpool.Pool, id string) ([]string, string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT payload->>'outcome', payload::text FROM connector_outbox WHERE payload->>'target'=$1 ORDER BY id`, "resource:"+id)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	defer rows.Close()
	var out []string
	var raw strings.Builder
	for rows.Next() {
		var o, p string
		if err := rows.Scan(&o, &p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, o)
		raw.WriteString(p)
	}
	return out, raw.String()
}

// assertLeasedKeyMatches: the leased private half is a valid ed25519 key whose
// public half equals what generation returned. Returns the leased PEM.
func assertLeasedKeyMatches(t *testing.T, pool *pgxpool.Pool, id string, want PublicInfo) string {
	t.Helper()
	m, err := Lease(context.Background(), pool, id, "sess", testActor, testKey)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	raw, err := ssh.ParseRawPrivateKey([]byte(m.PrivateKey))
	if err != nil {
		t.Fatalf("leased key does not parse: %v", err)
	}
	if _, ok := raw.(*ed25519.PrivateKey); !ok {
		t.Fatalf("leased key is %T, want ed25519", raw)
	}
	signer, err := ssh.ParsePrivateKey([]byte(m.PrivateKey))
	if err != nil {
		t.Fatalf("leased key does not parse as a signer: %v", err)
	}
	if got := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))); got != want.PublicKey {
		t.Fatalf("leased key's public half %q != returned %q", got, want.PublicKey)
	}
	if ssh.FingerprintSHA256(signer.PublicKey()) != want.Fingerprint {
		t.Fatal("fingerprint mismatch")
	}
	return m.PrivateKey
}

func TestGenerateRefusesExistingUnlessRotate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := mintID(t)
	cleanup(t, pool, id, "")

	first, err := Generate(ctx, pool, id, testKey, testActor, false)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	firstPEM := assertLeasedKeyMatches(t, pool, id, first)
	if _, err := Generate(ctx, pool, id, testKey, testActor, false); !errors.Is(err, ErrExists) {
		t.Fatalf("second generate without rotate = %v, want ErrExists", err)
	}
	if got := assertLeasedKeyMatches(t, pool, id, first); got != firstPEM {
		t.Fatal("a refused generate changed the stored key")
	}
	second, err := Generate(ctx, pool, id, testKey, testActor, true)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if second.PublicKey == first.PublicKey {
		t.Fatal("rotation did not replace the key")
	}
	if pk, err := PublicKey(ctx, pool, id); err != nil || pk != second {
		t.Fatalf("PublicKey after rotate = %+v, %v", pk, err)
	}
	if got := assertLeasedKeyMatches(t, pool, id, second); got == firstPEM {
		t.Fatal("old private key survived rotation")
	}
	evs, _ := outcomes(t, pool, id)
	want := "credential.generated,credential.leased,credential.leased,credential.rotated,credential.leased"
	if strings.Join(evs, ",") != want {
		t.Fatalf("events = %v, want %s", evs, want)
	}
}

func TestPendingIsNeverLeasableAndBindMoves(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id, other := mintID(t), mintID(t)
	label := "t176a-" + id
	cleanup(t, pool, id, label)
	cleanup(t, pool, other, "")

	pub, err := GeneratePending(ctx, pool, label, "aireos-demo", "ssh-ed25519 AAAAhostkey", testKey)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if _, err := GeneratePending(ctx, pool, label, "x", "y", testKey); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate label = %v, want ErrExists", err)
	}
	meta, err := PendingMetadata(ctx, pool, label)
	if err != nil || meta.PublicInfo != pub || meta.Account != "aireos-demo" || meta.HostKey != "ssh-ed25519 AAAAhostkey" {
		t.Fatalf("metadata = %+v, %v", meta, err)
	}
	// Never leasable: not under the label, not under any resource id.
	for _, probe := range []string{label, id, other} {
		if _, err := Lease(ctx, pool, probe, "s", testActor, testKey); err == nil || (probe != label && !errors.Is(err, ErrNotFound)) {
			t.Fatalf("lease of %q while pending = %v, want a refusal", probe, err)
		}
	}
	// Bind onto a resource that already has a credential: refused, nothing moved.
	if err := Set(ctx, pool, other, testKey, Material{PrivateKey: "mine"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(ctx, pool, label, other, testActor); !errors.Is(err, ErrExists) {
		t.Fatalf("bind onto existing = %v, want ErrExists", err)
	}
	if _, err := PendingMetadata(ctx, pool, label); err != nil {
		t.Fatalf("a refused bind consumed the pending row: %v", err)
	}
	if m, _ := Get(ctx, pool, other, testKey); m.PrivateKey != "mine" {
		t.Fatal("a refused bind overwrote the existing credential")
	}
	// Real bind.
	bound, err := Bind(ctx, pool, label, id, testActor)
	if err != nil || bound != pub {
		t.Fatalf("bind = %+v, %v", bound, err)
	}
	assertLeasedKeyMatches(t, pool, id, pub)
	if _, err := PendingMetadata(ctx, pool, label); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending row survived bind: %v", err)
	}
	if _, err := Bind(ctx, pool, label, mintID(t), testActor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double bind = %v, want ErrNotFound", err)
	}
	evs, raw := outcomes(t, pool, id)
	if evs[0] != "credential.bound" || !strings.Contains(raw, "label:"+label) || !strings.Contains(raw, pub.Fingerprint) {
		t.Fatalf("bound event wrong: %v %s", evs, raw)
	}
	if err := DeletePending(ctx, pool, label); err != nil {
		t.Fatalf("delete pending idempotent: %v", err)
	}
}

// N concurrent first-generates for one resource: exactly one wins, the rest are
// refused, and exactly one key and one credential.generated event exist.
func TestConcurrentFirstGenerateHasOneWinner(t *testing.T) {
	pool := testPool(t)
	id := mintID(t)
	cleanup(t, pool, id, "")
	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { _, err := Generate(context.Background(), pool, id, testKey, testActor, false); errs <- err }()
	}
	ok, refused := 0, 0
	for i := 0; i < n; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrExists):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || refused != n-1 {
		t.Fatalf("winners=%d refused=%d, want 1 and %d", ok, refused, n-1)
	}
	var rows int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM connector_credentials WHERE resource_id=$1`, id).Scan(&rows)
	if evs, _ := outcomes(t, pool, id); rows != 1 || len(evs) != 1 || evs[0] != "credential.generated" {
		t.Fatalf("rows=%d events=%v", rows, evs)
	}
}
