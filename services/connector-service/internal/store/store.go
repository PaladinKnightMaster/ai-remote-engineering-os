// Package store is connector-service's credential custody layer (ADR-0053).
//
// connector-service is the sole holder of AIREOS_RESOURCE_SECRET_KEY and the
// only service that runs pgp_sym_encrypt / pgp_sym_decrypt on target
// credentials; services/api and services/session-broker lose their decrypt
// paths in T-030 and lease the credential from here instead.
//
// Credentials live in connector-service's own connector_credentials table
// (pgcrypto BYTEA), keyed by a SOFT resource_id reference to the api-owned
// resources(id) — no cross-service foreign key (T-029a consult).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no credential is stored for the resource id.
var ErrNotFound = errors.New("connector credential not found")

// Material is the SSH auth material encrypted into connector_credentials. It is
// byte-identical to the legacy services/api Material shape, so the T-029a
// backfill is a verbatim ciphertext copy (same key, no re-encryption).
type Material struct {
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Set encrypts material with pgp_sym_encrypt(key) and upserts it onto the
// resource's credential row. connector custody is keyed by resource_id (a soft
// reference); existence of the api-owned resources row is api's concern.
func Set(ctx context.Context, pool *pgxpool.Pool, resourceID, key string, m Material) error {
	blob, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal material: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO connector_credentials (resource_id, connection_secret)
		VALUES ($1, pgp_sym_encrypt($2::text, $3))
		ON CONFLICT (resource_id)
		DO UPDATE SET connection_secret = EXCLUDED.connection_secret,
		              updated_at = now()`, resourceID, string(blob), key)
	if err != nil {
		return fmt.Errorf("encrypt+store credential: %w", err)
	}
	return nil
}

// Get decrypts the resource's stored material with pgp_sym_decrypt(key).
// ErrNotFound if no credential is stored; a wrong key surfaces as a decrypt
// error ("Wrong key or corrupt data").
func Get(ctx context.Context, pool *pgxpool.Pool, resourceID, key string) (Material, error) {
	var blob []byte
	err := pool.QueryRow(ctx, `
		SELECT pgp_sym_decrypt(connection_secret, $2)
		  FROM connector_credentials
		 WHERE resource_id = $1`, resourceID, key).Scan(&blob)
	if errors.Is(err, pgx.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	if err != nil {
		return Material{}, fmt.Errorf("decrypt credential: %w", err)
	}
	var m Material
	if err := json.Unmarshal(blob, &m); err != nil {
		return Material{}, fmt.Errorf("unmarshal material: %w", err)
	}
	return m, nil
}

// BackfillFromResources copies existing ciphertext from the legacy api-owned
// resources.connection_secret column into connector custody. This is the
// "expand" leg of the ADR-0053 expand-migrate-contract relocation: a pure byte
// copy (the AIREOS_RESOURCE_SECRET_KEY value is unchanged, so no
// decrypt/re-encrypt — connector never sees plaintext here). Idempotent via
// ON CONFLICT DO NOTHING; safe to re-run. Returns the number of rows inserted.
//
// This is the ONLY connector query that reads an api-owned table: a one-time
// operational backfill invoked by the `credentials migrate-legacy` CLI, kept
// out of the schema migrations so connector's migration history stays
// self-contained (T-029a consult).
func BackfillFromResources(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	ct, err := pool.Exec(ctx, `
		INSERT INTO connector_credentials (resource_id, connection_secret)
		SELECT id, connection_secret
		  FROM resources
		 WHERE connection_secret IS NOT NULL
		ON CONFLICT (resource_id) DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("backfill from resources: %w", err)
	}
	return ct.RowsAffected(), nil
}
