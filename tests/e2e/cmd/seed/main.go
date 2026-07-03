// Command seed provisions the T-020b/T-030b live e2e cluster. It generates the
// SSH host + client keypairs at runtime (so no private keys are committed to
// git), writes the host private key and the authorized client key to the shared
// sshd-keys volume for the test sshd, and inserts the ssh resource the
// walkthrough drives a session against with its non-secret coordinates in labels.
//
// As of T-030b the credential itself is NOT seeded here: connector-service is the
// sole custodian, so the compose command provisions it via the real CLI
// (`connector-service credentials set-secret … --key-file /seed-keys/client_ed25519`)
// after this binary writes the client key. This binary only writes keys + the
// resource row (labels-only); service migrations are run by the compose command
// before it (see docker-compose.e2e.yml).
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"
)

// resourceID / flaggedResourceID MUST match the constants in
// tests/e2e/harness_test.go — the walkthroughs create sessions against these
// resources. Kept in sync by comment (not a shared symbol) to keep the harness
// within its file budget. The flagged resource (requires_approval=true,
// T-033a / WD-2) points at the SAME sshd target with the same credential: only
// the approval gate differs, so the approval round-trip is the only variable.
const (
	resourceID                 = "01900000-0000-7000-8000-0000000000e2"
	flaggedResourceID          = "01900000-0000-7000-8000-0000000000a2"
	delegatedResourceID        = "01900000-0000-7000-8000-0000000000d3" // ungated; the T-065b agent-initiated-Run capstone (isolated from resourceID's audit chain)
	delegatedFlaggedResourceID = "01900000-0000-7000-8000-0000000000d4" // requires_approval=true; the T-068 WD-6 flagged delegated leg (isolated from flaggedResourceID's audit chain)
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("seed: %v", err)
	}
}

func run() error {
	keyDir := envOr("SEED_KEY_DIR", "/seed-keys")
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL must be set")
	}

	// Idempotent: the sshd ingests its host key once at startup, so regenerating
	// keys on a re-run (e.g. a second `compose run tester` against a live stack)
	// would desync the DB's pinned ssh_host_key from the running sshd → host key
	// mismatch. If the volume already holds keys, the cluster is provisioned;
	// no-op. A clean run (`task e2e` wipes volumes with `down -v`) seeds fully.
	hostKeyPath := filepath.Join(keyDir, "ssh_host_ed25519_key")
	if fi, statErr := os.Stat(hostKeyPath); statErr == nil && fi.Size() > 0 {
		log.Printf("seed: keys already present in %s — cluster provisioned, skipping", keyDir)
		return nil
	}

	// Host keypair: private → sshd HostKey; public (authorized_keys line) → the
	// resource's ssh_host_key label, pinned by the broker via ssh.FixedHostKey.
	hostPriv, hostAuthLine, err := genKey()
	if err != nil {
		return fmt.Errorf("host key: %w", err)
	}
	// Client keypair: private → encrypted connection_secret (broker dials with
	// it); public → the target's authorized_keys.
	clientPriv, clientAuthLine, err := genKey()
	if err != nil {
		return fmt.Errorf("client key: %w", err)
	}

	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(keyDir, "ssh_host_ed25519_key"), hostPriv, 0o600); err != nil {
		return fmt.Errorf("write host key: %w", err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "authorized_keys"), []byte(clientAuthLine+"\n"), 0o644); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}
	// Also emit the host PUBLIC key (→ the resource's ssh_host_key label) and the
	// client PRIVATE key (→ the encrypted credential) so a separate seeder can
	// provision the resource — used by the T-021d-b installed-stack WD-1 harness,
	// where the resource is inserted via psql + `api resources set-secret` inside
	// the host container (the installed postgres is localhost-only).
	if err := os.WriteFile(filepath.Join(keyDir, "ssh_host_key_pub"), []byte(hostAuthLine+"\n"), 0o644); err != nil {
		return fmt.Errorf("write host pubkey: %w", err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "client_ed25519"), clientPriv, 0o600); err != nil {
		return fmt.Errorf("write client key: %w", err)
	}

	// keys-only mode (T-021d-b): write the key material + skip the DB insert (the
	// installed postgres is not reachable from this build-host container).
	if os.Getenv("SEED_KEYS_ONLY") == "1" {
		log.Printf("seed: keys-only — wrote sshd keys + host pubkey + client key to %s (no DB insert)", keyDir)
		return nil
	}

	coords := map[string]string{
		"ssh_host":     "sshd",
		"ssh_port":     "2222",
		"ssh_user":     "aireos",
		"ssh_host_key": hostAuthLine,
	}
	labels, _ := json.Marshal(coords)
	coords["requires_approval"] = "true" // the WD-2 approval trigger (ADR-0054)
	flaggedLabels, _ := json.Marshal(coords)

	// Insert the resource rows with coordinates ONLY (T-030b): the credentials live
	// in connector custody, provisioned by `connector credentials set-secret` in
	// the compose command after this binary. connection_secret is left NULL — the
	// broker reads coords from labels and leases the key from connector at connect.
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	for _, r := range []struct{ id, name, labels string }{
		{resourceID, "e2e-target", string(labels)},
		{flaggedResourceID, "e2e-target-gated", string(flaggedLabels)},
		{delegatedResourceID, "e2e-target-delegated", string(labels)},                     // ungated (T-065b capstone)
		{delegatedFlaggedResourceID, "e2e-target-delegated-gated", string(flaggedLabels)}, // gated (T-068 WD-6)
	} {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO resources (id, type, name, labels)
			VALUES ($1, 'ssh', $2, $3::jsonb)
			ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels`,
			r.id, r.name, r.labels); err != nil {
			return fmt.Errorf("seed resource %s: %w", r.name, err)
		}
	}
	log.Printf("seed: provisioned ssh resources %s + %s (labels only; gated=requires_approval) + sshd keys in %s",
		resourceID, flaggedResourceID, keyDir)
	return nil
}

// genKey returns an OpenSSH-PEM private key and the matching authorized_keys
// public line for a fresh ed25519 keypair.
func genKey() (privPEM []byte, authLine string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return nil, "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(block), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
