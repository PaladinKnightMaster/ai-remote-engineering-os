// Package secret stores and retrieves a resource's encrypted target-connection
// credentials in services/api's Postgres (ADR-0048 §7, v0.1.0 §SEC-12).
//
// The credential material is pgcrypto-encrypted at rest in the
// resources.connection_secret BYTEA column via pgp_sym_encrypt, keyed by
// AIREOS_RESOURCE_SECRET_KEY (a separate authority from the PASETO signing key).
// Only the SSH auth material lives encrypted; non-secret connection coordinates
// (host, port, login user) live in resources.labels.
//
// This is the known v0.1.0 security debt: compromising services/api's database
// exposes every target. The architecturally-correct destination is
// services/connector-service (held in reserve with the §10 promotion trigger).
package secret

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no resource has the given id, or the resource has
// no stored secret.
var ErrNotFound = errors.New("resource secret not found")

// Material is the SSH auth material encrypted into resources.connection_secret.
// Forward-compatible JSON: session-broker (slice b) decrypts and reads it.
type Material struct {
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Set encrypts material with pgp_sym_encrypt(key) and stores it on the resource
// row. ErrNotFound if no resource matched the id.
func Set(ctx context.Context, pool *pgxpool.Pool, id, key string, m Material) error {
	blob, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal material: %w", err)
	}
	ct, err := pool.Exec(ctx, `
		UPDATE resources
		   SET connection_secret = pgp_sym_encrypt($2::text, $3),
		       updated_at = now()
		 WHERE id = $1`, id, string(blob), key)
	if err != nil {
		return fmt.Errorf("encrypt+store secret: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get decrypts the resource's stored material with pgp_sym_decrypt(key).
// ErrNotFound if the resource is absent or has no stored secret. A wrong key
// surfaces as a decrypt error (pgcrypto "Wrong key or corrupt data").
func Get(ctx context.Context, pool *pgxpool.Pool, id, key string) (Material, error) {
	var blob []byte
	err := pool.QueryRow(ctx, `
		SELECT pgp_sym_decrypt(connection_secret, $2)
		  FROM resources
		 WHERE id = $1 AND connection_secret IS NOT NULL`, id, key).Scan(&blob)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("decrypt secret: %w", err)
	}
	var m Material
	if err := json.Unmarshal(blob, &m); err != nil {
		return Material{}, fmt.Errorf("unmarshal material: %w", err)
	}
	return m, nil
}
