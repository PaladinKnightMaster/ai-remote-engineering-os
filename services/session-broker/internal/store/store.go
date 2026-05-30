// Package store is the session-broker's Postgres access layer. At T-017a it owns
// the producer half of the transactional outbox (ADR-0045): Enqueue writes an
// evidence Event into the broker's local outbox table. The relay
// (packages/go/outbox) drains it into the audit-service chain authority.
//
// T-017b extends this with session-state reads and the session-status-update
// that enqueues session.open/session.close inside the SAME transaction, so the
// evidence and the state change commit atomically.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// Store wraps the broker's pgx pool and the shared outbox Writer.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Enqueue writes one evidence Event into the outbox in its own transaction and
// returns the minted idempotency key. This is the standalone producer path; the
// in-tx variant that couples evidence to a session-state change lands in T-017b.
func (s *Store) Enqueue(ctx context.Context, e outbox.Event) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed
	key, err := s.writer.Enqueue(ctx, tx, e)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit outbox enqueue: %w", err)
	}
	return key, nil
}
