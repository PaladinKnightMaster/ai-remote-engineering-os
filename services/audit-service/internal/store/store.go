// Package store persists audit events and is where the append-only chain is
// extended (ADR-0045). Appends are serialized so prevHash always reads the true
// chain tail; dedup is enforced by a UNIQUE(idempotency_key) index.
package store

import (
	"context"
	"errors"
	"fmt"
	"math"

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

// Direction selects which way a keyset page walks the chain (AUD-22, AUD-27).
// It is a closed, typed enum rather than a bool or a bare string: at this
// boundary a stringly-typed direction is a bug waiting to happen (an
// unrecognized value must fail loud, not silently pick a default).
type Direction int

const (
	// Asc walks toward higher seq — the original, chain-order default.
	Asc Direction = iota
	// Desc walks toward lower seq — newest-first (AUD-24 is set by the
	// caller; this store has no opinion on which direction is "default").
	Desc
)

// ListQuery is the input to List: the keyset cursor and direction, plus the
// optional equality filters and the lifecycle-noise preset (ADR-0067 §D3/D4).
// Actor, Outcome, and Edge are nil when unset — an unset filter applies no
// predicate. Preset and the filters compose freely with either direction
// (AUD-25's "composition" requirement).
type ListQuery struct {
	// Cursor is the seq boundary to walk from. 0 means "no cursor": for Asc
	// that is already the natural start (seq > 0 matches the whole chain);
	// for Desc, List substitutes the natural sentinel (math.MaxInt64) so
	// seq < sentinel also matches the whole chain — computed from the
	// direction, never built by string concatenation.
	Cursor    int64
	Direction Direction
	Actor     *string
	Outcome   *string
	Edge      *string
	// Preset, when true, applies the governed-actions@1 lifecycle-noise
	// preset: excludes exactly the outcomes in governedActionsV1ExcludedOutcomes.
	Preset bool
	Limit  int
}

// build renders q into one parameterized SQL statement and its bound args.
// Split out from List so integration tests can EXPLAIN the exact statement
// List executes (PERF-10) without duplicating the SQL text.
//
// Only the comparison operator and sort order vary by text (chosen in Go from
// the closed Direction enum — never from a filter value); the cursor, every
// filter, the preset's excluded-outcome list, and the limit are always bound
// $n parameters (never interpolated).
func (q ListQuery) build() (string, []any) {
	cursor := q.Cursor
	var cmp, order string
	switch q.Direction {
	case Desc:
		if cursor == 0 {
			cursor = math.MaxInt64 // natural sentinel: seq < MaxInt64 matches the whole chain
		}
		cmp, order = "seq < $1", "seq DESC"
	default:
		cmp, order = "seq > $1", "seq ASC"
	}

	query := selectCols + `
		WHERE ` + cmp + `
		  AND ($2::text IS NULL OR actor = $2)
		  AND ($3::text IS NULL OR outcome = $3)
		  AND ($4::text IS NULL OR edge = $4)
		  AND ($5::text[] IS NULL OR COALESCE(outcome,'') <> ALL($5))
		ORDER BY ` + order + `
		LIMIT $6`

	return query, []any{cursor, q.Actor, q.Outcome, q.Edge, presetExcludedOutcomes(q.Preset), q.Limit}
}

// List returns up to q.Limit events matching q, walking the keyset from
// q.Cursor in q.Direction. Both directions are served by this one
// parameterized query (AUD-22): ascending compares seq > $1 and orders ASC;
// descending compares seq < $1 and orders DESC. Every filter value — the
// cursor, the equality filters, the preset's excluded-outcome list, and the
// limit — is always a bound parameter; only the comparison operator and sort
// order are chosen in Go, from the closed Direction enum, never from a filter
// value.
//
// The returned cursor is the seq of the last row visited in walked order —
// pass it back as Cursor to fetch the next page in the SAME direction
// (ascending: the highest seq in the page; descending: the lowest). This is
// the pre-existing (events, cursor, error) shape, unchanged.
//
// AUD-31 (block-if-violated): the PRESET exclusion coalesces outcome to the
// empty string before comparing, rather than filtering the raw nullable
// column directly. Plain `outcome NOT IN (...)` evaluates to NULL — not true
// — for a NULL outcome in SQL's three-valued logic, which would silently
// exclude every event with no outcome from the default view. Coalescing to
// the empty string first is never NULL, so `<> ALL(...)` always evaluates to
// a real true/false. The Outcome equality FILTER, by contrast, compares the raw
// column (`outcome = $3`): it is matching a caller-supplied concrete outcome
// value, never NULL, so the raw comparison is both correct (a NULL outcome
// legitimately fails to match a specific requested outcome) and — unlike a
// COALESCE(...)-wrapped comparison — able to use the plain btree index built
// by migration 0002 (PERF-7/PERF-10).
func (s *Store) List(ctx context.Context, q ListQuery) ([]audit.Event, int64, error) {
	query, args := q.build()
	rows, err := s.pool.Query(ctx, query, args...)
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

// ChainHead returns MAX(seq), the newest chain position (AUD-29, PERF-9) —
// O(1) on the primary key, never a COUNT(*) or a complement count. seq is
// BIGSERIAL and nextval is non-transactional (a rolled-back Append still
// burns a value), so gaps exist and ChainHead is deliberately a *position*,
// not a row count. An empty table returns 0.
func (s *Store) ChainHead(ctx context.Context) (int64, error) {
	var head int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(seq), 0) FROM audit_events`).Scan(&head)
	return head, err
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
