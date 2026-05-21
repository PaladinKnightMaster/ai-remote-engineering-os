// Package store persists audit events and is where the append-only chain is
// extended (ADR-0045). Appends are serialized so prevHash always reads the true
// chain tail; dedup is enforced by a UNIQUE(idempotency_key) index.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
)

// ErrNotFound is returned by Get when no event has the given id.
var ErrNotFound = errors.New("audit event not found")

// chainLockKey namespaces the pg_advisory_xact_lock that serializes appends.
// One key for the single P0 chain; "AUDI" in ASCII.
const chainLockKey int64 = 0x41554449

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append writes one event and extends the chain. It is idempotent on
// idempotencyKey: a replay returns the already-stored event with deduped=true
// and does NOT extend the chain (at-least-once delivery, ADR-0045).
//
// The whole operation runs in one transaction holding a transaction-scoped
// advisory lock, so concurrent appends serialize into a single linear chain
// with no forks or gaps (ADR-0045 §Verification concurrent-append test).
func (s *Store) Append(ctx context.Context, in audit.Ingest) (audit.Event, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return audit.Event{}, false, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockKey); err != nil {
		return audit.Event{}, false, fmt.Errorf("acquire chain lock: %w", err)
	}

	// Dedup check inside the lock: a concurrent replay cannot slip past.
	if existing, err := scanByIdempotencyKey(ctx, tx, in.IdempotencyKey); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return audit.Event{}, false, fmt.Errorf("dedup lookup: %w", err)
	}

	prev := chain.Genesis
	var tail string
	switch err := tx.QueryRow(ctx, `SELECT chain_hash FROM audit_events ORDER BY seq DESC LIMIT 1`).Scan(&tail); {
	case err == nil:
		prev = tail
	case errors.Is(err, pgx.ErrNoRows):
		// empty chain → genesis
	default:
		return audit.Event{}, false, fmt.Errorf("read chain tail: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return audit.Event{}, false, fmt.Errorf("mint event id: %w", err)
	}
	e := audit.Event{
		ID:               id.String(),
		Timestamp:        in.Timestamp,
		Actor:            in.Actor,
		Edge:             in.Edge,
		Target:           in.Target,
		Outcome:          in.Outcome,
		EvidencePacketID: in.EvidencePacketID,
		IdempotencyKey:   in.IdempotencyKey,
		PrevHash:         prev,
	}
	e.ChainHash = chain.Compute(prev, e)

	err = tx.QueryRow(ctx, `
		INSERT INTO audit_events
		  (id, idempotency_key, timestamp, actor, edge, target, outcome, evidence_packet_id, prev_hash, chain_hash)
		VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),$9,$10)
		RETURNING seq`,
		e.ID, e.IdempotencyKey, e.Timestamp, e.Actor, e.Edge, e.Target,
		e.Outcome, e.EvidencePacketID, e.PrevHash, e.ChainHash,
	).Scan(&e.Seq)
	if err != nil {
		return audit.Event{}, false, fmt.Errorf("insert event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return audit.Event{}, false, fmt.Errorf("commit append: %w", err)
	}
	return e, false, nil
}

// Get returns one event by its server-assigned id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (audit.Event, error) {
	e, err := scanOne(s.pool.QueryRow(ctx, selectCols+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return audit.Event{}, ErrNotFound
	}
	return e, err
}

// List returns up to limit events in ascending chain order with seq > afterSeq
// (afterSeq=0 starts at the head). The returned cursor is the last seq, or 0
// when the page is empty.
func (s *Store) List(ctx context.Context, afterSeq int64, limit int) ([]audit.Event, int64, error) {
	rows, err := s.pool.Query(ctx, selectCols+` WHERE seq > $1 ORDER BY seq ASC LIMIT $2`, afterSeq, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []audit.Event
	var last int64
	for rows.Next() {
		e, err := scanOneRow(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, e)
		last = e.Seq
	}
	return out, last, rows.Err()
}

const selectCols = `
	SELECT seq, id, idempotency_key, timestamp, actor, edge, target,
	       COALESCE(outcome,''), COALESCE(evidence_packet_id,''), prev_hash, chain_hash
	FROM audit_events`

type scanner interface {
	Scan(dest ...any) error
}

func scanOne(row scanner) (audit.Event, error) {
	var e audit.Event
	err := row.Scan(&e.Seq, &e.ID, &e.IdempotencyKey, &e.Timestamp, &e.Actor, &e.Edge, &e.Target,
		&e.Outcome, &e.EvidencePacketID, &e.PrevHash, &e.ChainHash)
	return e, err
}

func scanOneRow(rows pgx.Rows) (audit.Event, error) { return scanOne(rows) }

func scanByIdempotencyKey(ctx context.Context, tx pgx.Tx, key string) (audit.Event, error) {
	return scanOne(tx.QueryRow(ctx, selectCols+` WHERE idempotency_key = $1`, key))
}
