package store

// heads.go adds the store-side primitives for signed chain heads
// (ADR-0069). Deliberately separate from the ingest path (store.go's Append
// + chainLockKey): head creation is a bounded, off-write-path sweep
// (T-124b) and MUST NOT share a lock or transaction with Append — see
// WithCheckpointLock below.

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"aireos.local/services/audit-service/internal/chain"
)

// checkpointLockKey namespaces the pg_try_advisory_xact_lock that makes head
// creation single-writer (ADR-0069 D10: "two audit-service instances at
// cadence would race"). Deliberately a different constant from Append's
// chainLockKey — a checkpoint and an ingest append must never serialize
// against each other, which is what D2/PERF-14 require.
const checkpointLockKey int64 = 0x41554448 // "AUDH", distinct from Append's "AUDI"

// InsertHead persists a signed chain head. h.Seq must already exist as an
// audit_events row — the migration's FK (RESTRICT, no ON DELETE) enforces
// that a head can never outlive or precede its event.
func (s *Store) InsertHead(ctx context.Context, h chain.Head) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO audit_chain_heads (seq, chain_hash, head_timestamp, key_id, signature)
		VALUES ($1, $2, $3, $4, $5)`,
		h.Seq, h.ChainHash, h.Timestamp, h.KeyID, h.Signature,
	)
	if err != nil {
		return fmt.Errorf("insert chain head at seq %d: %w", h.Seq, err)
	}
	return nil
}

// LastHead returns the highest-seq chain head, or ok=false when none exists
// yet — a fresh install has no heads at all until the first checkpoint fires
// (ADR-0069 D6's "three outcomes, never two" starts here: absence is a real,
// expected state, not an error).
func (s *Store) LastHead(ctx context.Context) (h chain.Head, ok bool, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT seq, chain_hash, head_timestamp, key_id, signature
		FROM audit_chain_heads
		ORDER BY seq DESC
		LIMIT 1`,
	).Scan(&h.Seq, &h.ChainHash, &h.Timestamp, &h.KeyID, &h.Signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return chain.Head{}, false, nil
	}
	if err != nil {
		return chain.Head{}, false, fmt.Errorf("last chain head: %w", err)
	}
	return h, true, nil
}

// EventCountSince returns the number of audit_events with seq > since — the
// input the cadence sweep's "100 events since the last head" trigger reads
// (T-124b; not called from this ticket's own code).
func (s *Store) EventCountSince(ctx context.Context, since int64) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE seq > $1`, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("event count since seq %d: %w", since, err)
	}
	return n, nil
}

// WithCheckpointLock runs fn inside a transaction holding
// pg_try_advisory_xact_lock(checkpointLockKey) and commits on success — head
// creation is single-writer (ADR-0069 D10). If another writer already holds
// the lock, it returns acquired=false IMMEDIATELY with no error and without
// blocking (pg_try_advisory_xact_lock never waits, unlike
// pg_advisory_xact_lock); the losing caller must skip its turn rather than
// retry into a duplicate seq.
//
// PERF-14-load-bearing: this lock is acquired ONLY here, around checkpoint
// creation, and is never taken on or interleaved with the Append ingest
// path (unchanged, still on chainLockKey). Were ingest ever made to wait on
// checkpointLockKey, every governed action would serialize behind the
// head-signing cadence — exactly what D2 ("signing is off the audit write
// path") rules out.
func (s *Store) WithCheckpointLock(ctx context.Context, fn func(pgx.Tx) error) (acquired bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin checkpoint tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, checkpointLockKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("try checkpoint lock: %w", err)
	}
	if !acquired {
		return false, nil
	}
	if err := fn(tx); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit checkpoint: %w", err)
	}
	return true, nil
}
