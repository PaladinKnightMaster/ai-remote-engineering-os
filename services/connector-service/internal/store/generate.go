package store

// Connector-side target-key generation (ADR-0079 D1, D3, D4). connector-service
// creates the ed25519 pair, keeps the private half encrypted exactly as Set does,
// and returns ONLY public material. Every audit event is enqueued in the same
// transaction as the state change and carries no private bytes.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/ssh"

	"aireos.local/packages/go/outbox"
)

// ErrExists means a credential (or a pending label) already exists and the caller did
// not ask to replace it.
var ErrExists = errors.New("connector credential already exists")

// ErrNoPublicKey means a credential exists but was imported (set-secret), so no
// public half was ever recorded.
var ErrNoPublicKey = errors.New("credential has no recorded public key")

// PublicInfo is the only thing generation ever hands back.
type PublicInfo struct {
	PublicKey   string // "ssh-ed25519 AAAA..." (no comment)
	Fingerprint string // "SHA256:..."
}

// PendingInfo is a pending credential's NON-secret fields.
type PendingInfo struct {
	PublicInfo
	Account string
	HostKey string
}

// newKeyPair returns the OpenSSH PEM private key plus the public half.
func newKeyPair() (material Material, pub PublicInfo, err error) {
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Material{}, PublicInfo{}, fmt.Errorf("generate ed25519: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(sk, "")
	if err != nil {
		return Material{}, PublicInfo{}, fmt.Errorf("marshal private key: %w", err)
	}
	sshPub, err := ssh.NewPublicKey(pk)
	if err != nil {
		return Material{}, PublicInfo{}, fmt.Errorf("marshal public key: %w", err)
	}
	return Material{PrivateKey: string(pem.EncodeToMemory(block))},
		PublicInfo{
			PublicKey:   strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))),
			Fingerprint: ssh.FingerprintSHA256(sshPub),
		}, nil
}

// fingerprintOf re-derives the fingerprint from a stored public-key line.
func fingerprintOf(line string) (string, error) {
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "", fmt.Errorf("parse stored public key: %w", err)
	}
	return ssh.FingerprintSHA256(pub), nil
}

func enqueue(ctx context.Context, tx pgx.Tx, actor, target, outcome, evidence string) error {
	w := outbox.Writer{Table: outboxTable}
	if _, err := w.Enqueue(ctx, tx, outbox.Event{
		Timestamp:        time.Now(),
		Actor:            actor,
		Edge:             "agent-remote-session",
		Target:           target,
		Outcome:          outcome,
		EvidencePacketID: evidence,
	}); err != nil {
		return fmt.Errorf("enqueue %s event: %w", outcome, err)
	}
	return nil
}

// Generate creates (or, with rotate, replaces) the resource's credential. It
// refuses with ErrExists when one is already stored and rotate is false.
func Generate(ctx context.Context, pool *pgxpool.Pool, resourceID, key, actor string, rotate bool) (PublicInfo, error) {
	m, pub, err := newKeyPair()
	if err != nil {
		return PublicInfo{}, err
	}
	blob, err := json.Marshal(m)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("marshal material: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("begin generate tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	outcome := "credential.generated"
	if rotate {
		var one int
		switch err := tx.QueryRow(ctx, `SELECT 1 FROM connector_credentials WHERE resource_id=$1 FOR UPDATE`, resourceID).Scan(&one); {
		case err == nil:
			outcome = "credential.rotated"
		case !errors.Is(err, pgx.ErrNoRows):
			return PublicInfo{}, fmt.Errorf("lock credential: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO connector_credentials (resource_id, connection_secret, public_key)
			VALUES ($1, pgp_sym_encrypt($2::text, $3), $4)
			ON CONFLICT (resource_id)
			DO UPDATE SET connection_secret = EXCLUDED.connection_secret,
			              public_key = EXCLUDED.public_key, updated_at = now()`,
			resourceID, string(blob), key, pub.PublicKey); err != nil {
			return PublicInfo{}, fmt.Errorf("encrypt+store credential: %w", err)
		}
	} else {
		ct, err := tx.Exec(ctx, `
			INSERT INTO connector_credentials (resource_id, connection_secret, public_key)
			VALUES ($1, pgp_sym_encrypt($2::text, $3), $4)
			ON CONFLICT (resource_id) DO NOTHING`,
			resourceID, string(blob), key, pub.PublicKey)
		if err != nil {
			return PublicInfo{}, fmt.Errorf("encrypt+store credential: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return PublicInfo{}, ErrExists
		}
	}
	if err := enqueue(ctx, tx, actor, "resource:"+resourceID, outcome, "fingerprint:"+pub.Fingerprint); err != nil {
		return PublicInfo{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicInfo{}, fmt.Errorf("commit generate: %w", err)
	}
	return pub, nil
}

// PublicKey returns the public half recorded for a resource's credential.
func PublicKey(ctx context.Context, pool *pgxpool.Pool, resourceID string) (PublicInfo, error) {
	var line *string
	err := pool.QueryRow(ctx, `SELECT public_key FROM connector_credentials WHERE resource_id=$1`, resourceID).Scan(&line)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicInfo{}, ErrNotFound
	}
	if err != nil {
		return PublicInfo{}, fmt.Errorf("read public key: %w", err)
	}
	if line == nil || *line == "" {
		return PublicInfo{}, ErrNoPublicKey
	}
	fp, err := fingerprintOf(*line)
	if err != nil {
		return PublicInfo{}, err
	}
	return PublicInfo{PublicKey: *line, Fingerprint: fp}, nil
}

// GeneratePending creates a key not yet bound to any resource, keyed by label,
// with the demo account and host-key line recorded as non-secret metadata. It
// is not leasable (the lease reads connector_credentials only). Refuses an
// existing label. credential.pending is audited under actor, targeting the label.
func GeneratePending(ctx context.Context, pool *pgxpool.Pool, label, account, hostKey, key, actor string) (PublicInfo, error) {
	for name, v := range map[string]string{"label": label, "account": account, "host key": hostKey} {
		if strings.TrimSpace(v) == "" || strings.ContainsAny(v, "\r\n") {
			return PublicInfo{}, fmt.Errorf("%s must be a non-empty single line", name)
		}
	}
	m, pub, err := newKeyPair()
	if err != nil {
		return PublicInfo{}, err
	}
	blob, err := json.Marshal(m)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("marshal material: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("begin pending tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ct, err := tx.Exec(ctx, `
		INSERT INTO connector_pending_credentials (label, private_secret, public_key, account, host_key)
		VALUES ($1, pgp_sym_encrypt($2::text, $3), $4, $5, $6)
		ON CONFLICT (label) DO NOTHING`, label, string(blob), key, pub.PublicKey, account, hostKey)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("store pending credential: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return PublicInfo{}, ErrExists
	}
	if err := enqueue(ctx, tx, actor, "credential-pending:"+label, "credential.pending", "fingerprint:"+pub.Fingerprint); err != nil {
		return PublicInfo{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicInfo{}, fmt.Errorf("commit pending: %w", err)
	}
	return pub, nil
}

// PendingMetadata returns a pending credential's non-secret fields.
func PendingMetadata(ctx context.Context, pool *pgxpool.Pool, label string) (PendingInfo, error) {
	var p PendingInfo
	err := pool.QueryRow(ctx, `SELECT public_key, account, host_key FROM connector_pending_credentials WHERE label=$1`, label).
		Scan(&p.PublicKey, &p.Account, &p.HostKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return PendingInfo{}, ErrNotFound
	}
	if err != nil {
		return PendingInfo{}, fmt.Errorf("read pending credential: %w", err)
	}
	if p.Fingerprint, err = fingerprintOf(p.PublicKey); err != nil {
		return PendingInfo{}, err
	}
	return p, nil
}

// DeletePending removes a pending credential; idempotent.
func DeletePending(ctx context.Context, pool *pgxpool.Pool, label string) error {
	if _, err := pool.Exec(ctx, `DELETE FROM connector_pending_credentials WHERE label=$1`, label); err != nil {
		return fmt.Errorf("delete pending credential: %w", err)
	}
	return nil
}

// Bind moves a pending credential onto a resource in ONE transaction: the
// ciphertext is copied verbatim (no decrypt), the pending row is deleted and
// credential.bound is enqueued. ErrNotFound if the label is absent; ErrExists
// (nothing moved) if the resource already has a credential.
func Bind(ctx context.Context, pool *pgxpool.Pool, label, resourceID, actor string) (PublicInfo, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("begin bind tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var secret []byte
	var pubLine string
	err = tx.QueryRow(ctx, `SELECT private_secret, public_key FROM connector_pending_credentials WHERE label=$1 FOR UPDATE`, label).
		Scan(&secret, &pubLine)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicInfo{}, ErrNotFound
	}
	if err != nil {
		return PublicInfo{}, fmt.Errorf("read pending credential: %w", err)
	}
	fp, err := fingerprintOf(pubLine)
	if err != nil {
		return PublicInfo{}, err
	}
	ct, err := tx.Exec(ctx, `
		INSERT INTO connector_credentials (resource_id, connection_secret, public_key)
		VALUES ($1, $2, $3) ON CONFLICT (resource_id) DO NOTHING`, resourceID, secret, pubLine)
	if err != nil {
		return PublicInfo{}, fmt.Errorf("bind credential: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return PublicInfo{}, ErrExists
	}
	if _, err := tx.Exec(ctx, `DELETE FROM connector_pending_credentials WHERE label=$1`, label); err != nil {
		return PublicInfo{}, fmt.Errorf("drop pending credential: %w", err)
	}
	if err := enqueue(ctx, tx, actor, "resource:"+resourceID, "credential.bound", "label:"+label+" fingerprint:"+fp); err != nil {
		return PublicInfo{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicInfo{}, fmt.Errorf("commit bind: %w", err)
	}
	return PublicInfo{PublicKey: pubLine, Fingerprint: fp}, nil
}
