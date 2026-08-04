package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
)

// ErrNotFound is returned when no live paired session has the given id (GetLive) —
// collapsing unknown/revoked/expired into one answer so the per-request liveness
// check is not an oracle (SEC-34).
var ErrNotFound = errors.New("paired session not found")

const (
	// RequestTTL is the pairing request's lifetime (ADR-0068 §D6): long enough to
	// unlock a phone and scan a QR, short enough that a QR left visible on a screen
	// is worth little.
	RequestTTL = 120 * time.Second

	// PairedTTL is the paired session's lifetime (ADR-0068 §D4) — deliberately the
	// SAME as auth.OperatorTTL (8h): inventing a fifth TTL number would imply the
	// TTL is the control, and it is not; revocability is. Unlike the desktop
	// session, the paired session gets NO sliding refresh — it expires hard and
	// must be re-paired.
	PairedTTL = 8 * time.Hour

	// secretBytes is the raw entropy of the one-time pairing secret (ADR-0068 §D6,
	// GOV-12): 256 bits needs no KDF (no argon2id) to resist brute force within the
	// 120s window — a KDF would only add ~100ms of latency to every redeem for no
	// real security gain here.
	secretBytes = 32

	// maxFailedAttempts is the redeem failed-attempt cap (ADR-0068 §D6 requires a
	// cap; the number is T-113's own consult decision). The Nth wrong secret burns
	// the request — consumed_at is set in the SAME transaction as the increment
	// ("a failed-attempt cap consumes the request").
	maxFailedAttempts = 3
)

// Store persists pairing_requests + paired_sessions. It takes the pgx pool
// directly (the internal/run / internal/grant pattern) so the package stays
// extraction-ready and depends on nothing api-internal.
type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

// NewStore builds the pairing store. outboxTable is the api outbox table (e.g.
// "api_outbox"). This ticket (T-113) chains no events through writer — it is wired
// now so T-114 can add its three pairing.* Enqueue calls INSIDE the transactions
// that already exist below (CreateInTx/RedeemInTx/RevokeInTx) rather than
// restructuring them (the T-058a→b lesson).
func NewStore(pool *pgxpool.Pool, outboxTable string) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: outboxTable}}
}

// Create begins its own transaction and delegates to CreateInTx; see CreateInTx for
// the behavior. Returns the new Request plus the PLAINTEXT secret — the only place
// it ever exists outside memory.
func (s *Store) Create(ctx context.Context, in CreateInput) (Request, []byte, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	req, secret, err := s.CreateInTx(ctx, tx, in)
	if err != nil {
		return Request{}, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, nil, fmt.Errorf("commit pairing request: %w", err)
	}
	return req, secret, nil
}

// CreateInTx generates secretBytes of crypto/rand entropy, stores ONLY its SHA-256
// digest, and returns the request row plus the raw plaintext secret (GOV-12) — the
// caller (Create, or T-114's handler) owns commit/rollback. The plaintext is never
// written to a column, a log line, or an error string; the only trace of it is this
// return value.
func (s *Store) CreateInTx(ctx context.Context, tx pgx.Tx, in CreateInput) (Request, []byte, error) {
	if in.Operator == "" {
		return Request{}, nil, errors.New("pairing request requires an operator")
	}
	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return Request{}, nil, fmt.Errorf("generate pairing secret: %w", err)
	}
	sum := sha256.Sum256(raw)

	id, err := uuid.NewV7()
	if err != nil {
		return Request{}, nil, fmt.Errorf("mint pairing request id: %w", err)
	}

	// expires_at is computed IN SQL (now() + RequestTTL), not with Go's time.Now():
	// Postgres is the single clock authority for every row this package writes and
	// every read that compares against a row (Commander review finding B). RETURNING
	// hands back what was actually stored rather than a second, separately-computed
	// Go value that could drift from it.
	var createdAt, expiresAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO pairing_requests (id, operator, label, secret_hash, expires_at)
		VALUES ($1, $2, $3, $4, now() + ($5 * interval '1 second'))
		RETURNING expires_at, created_at`,
		id.String(), in.Operator, in.Label, sum[:], RequestTTL.Seconds(),
	).Scan(&expiresAt, &createdAt); err != nil {
		return Request{}, nil, fmt.Errorf("insert pairing request: %w", err)
	}

	return Request{
		ID: id.String(), Operator: in.Operator, Label: in.Label,
		ExpiresAt: expiresAt, CreatedAt: createdAt,
	}, raw, nil
}

// Redeem begins its own transaction and delegates to RedeemInTx; see RedeemInTx for
// the behavior, including why it commits even on a refusal.
func (s *Store) Redeem(ctx context.Context, id string, secret []byte) (Session, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	sess, ok, err := s.RedeemInTx(ctx, tx, id, secret)
	if err != nil {
		return Session{}, false, err
	}
	// Commit even when ok=false: a refusal on a wrong secret still persisted an
	// attempts increment (and possibly the failed-attempt-cap burn) — that is real
	// state that must survive, not a probe to roll back.
	if err := tx.Commit(ctx); err != nil {
		return Session{}, false, fmt.Errorf("commit redeem: %w", err)
	}
	if !ok {
		return Session{}, false, nil
	}
	return sess, true, nil
}

// RedeemInTx is the single-use redeem transaction (ADR-0068 §D6, GOV-12 — this
// ticket's named G1/VER-6 live test): SELECT ... FOR UPDATE locks the request row
// so a concurrent second redeem attempt against the same id serializes behind the
// first and is refused (the concurrency this ticket proves against real Postgres);
// it verifies the hash with crypto/subtle.ConstantTimeCompare (timing-safe), checks
// the request is unconsumed and unexpired, marks it consumed, and inserts the
// paired_sessions row carrying the request's label — all inside the caller's
// transaction (T-114 adds pairing.redeemed here without restructuring, the
// T-058a→b lesson).
//
// ok=false, err=nil covers EVERY refusal alike — a malformed id, an unknown id,
// wrong secret, expired, already consumed — so a caller cannot distinguish reasons
// from the return value (no oracle, mirroring SEC-34's per-request liveness
// posture; the id here rides the URL fragment and is attacker-supplied at redeem,
// unlike grant's operator-authenticated ids — Commander review finding A). A
// malformed id is rejected by uuid.Parse BEFORE touching the database: passing it
// straight into `id = $1` against a UUID column would make Postgres raise SQLSTATE
// 22P02, which surfaces as a wrapped error distinguishable from "not found" — the
// exact oracle this refusal shape exists to close. err is reserved for genuine
// failures (a DB error), never for "wrong secret" or "malformed id."
//
// The expiry check compares against the DATABASE's now() (returned by the same
// locking SELECT), not Go's time.Now(). Postgres is the single clock authority for
// every row this package writes — CreateInTx's request insert and this method's
// session insert below both compute expires_at IN SQL (now() + TTL) rather than in
// Go, and RETURNING hands back the stored value — and for every other read
// (GetLive). A second, Go-side clock in any one of these spots is a latent
// split-clock bug waiting for a multi-host deployment (Commander review finding B;
// the write-side half of the fix landed after an initial pass left it comparison-
// only — the claim in this comment is now backed by code, not just intent).
//
// On a wrong secret, attempts increments in the SAME transaction; the
// maxFailedAttempts-th wrong secret ALSO burns the request (consumed_at set) in
// that same UPDATE — "a failed-attempt cap consumes the request" (§6). The caller
// MUST commit even when ok=false: the attempts increment (and possible burn) is
// real state that has to persist.
func (s *Store) RedeemInTx(ctx context.Context, tx pgx.Tx, id string, secret []byte) (Session, bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Session{}, false, nil
	}

	var (
		operator, label string
		secretHash      []byte
		expiresAt       time.Time
		consumedAt      *time.Time
		attempts        int
		dbNow           time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT operator, label, secret_hash, expires_at, consumed_at, attempts, now()
		  FROM pairing_requests WHERE id = $1 FOR UPDATE`, id).
		Scan(&operator, &label, &secretHash, &expiresAt, &consumedAt, &attempts, &dbNow)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("lock pairing request: %w", err)
	}

	// Already consumed (a prior successful redeem, OR a prior attempt-cap burn) or
	// past its 120s window: refuse without touching any state. dbNow (not
	// time.Now()) is the comparison clock — see the doc comment above.
	if consumedAt != nil || !expiresAt.After(dbNow) {
		return Session{}, false, nil
	}

	sum := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(sum[:], secretHash) != 1 {
		attempts++
		if attempts >= maxFailedAttempts {
			if _, err := tx.Exec(ctx, `
				UPDATE pairing_requests SET attempts = $2, consumed_at = now() WHERE id = $1`,
				id, attempts); err != nil {
				return Session{}, false, fmt.Errorf("burn pairing request on attempt cap: %w", err)
			}
		} else {
			if _, err := tx.Exec(ctx, `
				UPDATE pairing_requests SET attempts = $2 WHERE id = $1`, id, attempts); err != nil {
				return Session{}, false, fmt.Errorf("increment pairing attempts: %w", err)
			}
		}
		return Session{}, false, nil
	}

	// Correct secret: consume the request (single-use) and mint the session.
	if _, err := tx.Exec(ctx, `
		UPDATE pairing_requests SET consumed_at = now() WHERE id = $1`, id); err != nil {
		return Session{}, false, fmt.Errorf("consume pairing request: %w", err)
	}

	sessID, err := uuid.NewV7()
	if err != nil {
		return Session{}, false, fmt.Errorf("mint paired session id: %w", err)
	}

	// expires_at is computed IN SQL (now() + PairedTTL), same reasoning as
	// CreateInTx's request insert above — Postgres stays the single clock authority
	// for every write, and RETURNING hands back the value actually stored.
	var createdAt, sessExpiresAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO paired_sessions (id, operator, label, scope, expires_at)
		VALUES ($1, $2, $3, $4, now() + ($5 * interval '1 second'))
		RETURNING expires_at, created_at`,
		sessID.String(), operator, label, ApprovalsScopeV1, PairedTTL.Seconds(),
	).Scan(&sessExpiresAt, &createdAt); err != nil {
		return Session{}, false, fmt.Errorf("insert paired session: %w", err)
	}

	return Session{
		ID: sessID.String(), Operator: operator, Label: label, Scope: ApprovalsScopeV1,
		ExpiresAt: sessExpiresAt, CreatedAt: createdAt,
	}, true, nil
}

// GetLive returns the paired session by id, but ONLY when it is live — revoked_at
// IS NULL AND expires_at > now() — a single indexed primary-key lookup (PERF-12).
// This is the per-request liveness read T-114's middleware calls; ErrNotFound
// covers a malformed id, unknown id, revoked, and expired alike (SEC-34 — no
// oracle). uuid.Parse rejects a malformed id BEFORE the query: otherwise Postgres
// would raise SQLSTATE 22P02 on a non-UUID string, a distinguishable error a
// caller could map to a different response than "not found" (Commander review
// finding A — this id is attacker-supplied at redeem/liveness-check time).
func (s *Store) GetLive(ctx context.Context, id string) (Session, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Session{}, ErrNotFound
	}

	var sess Session
	err := s.pool.QueryRow(ctx, `
		SELECT id, operator, label, scope, expires_at, created_at
		  FROM paired_sessions
		 WHERE id = $1 AND revoked_at IS NULL AND expires_at > now()`, id).
		Scan(&sess.ID, &sess.Operator, &sess.Label, &sess.Scope, &sess.ExpiresAt, &sess.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get live paired session: %w", err)
	}
	return sess, nil
}

// ListForOperator returns up to limit of the operator's paired sessions (live and
// revoked alike — the desk's revoke list needs history too) in id (≈ creation)
// order with id > after (after="" starts at the head). Owner-scoped: an operator
// never sees another operator's sessions, mirroring grant.ListForOperator.
func (s *Store) ListForOperator(ctx context.Context, operator, after string, limit int) ([]Session, string, error) {
	const cols = `SELECT id, operator, label, scope, expires_at, revoked_at, created_at
		FROM paired_sessions WHERE operator = $1`
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, cols+` ORDER BY id ASC LIMIT $2`, operator, limit)
	} else {
		rows, err = s.pool.Query(ctx, cols+` AND id > $2 ORDER BY id ASC LIMIT $3`, operator, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Session
	var last string
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.Operator, &sess.Label, &sess.Scope,
			&sess.ExpiresAt, &sess.RevokedAt, &sess.CreatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, sess)
		last = sess.ID
	}
	return out, last, rows.Err()
}

// Revoke begins its own transaction and delegates to RevokeInTx.
func (s *Store) Revoke(ctx context.Context, id string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ok, err := s.RevokeInTx(ctx, tx, id)
	if err != nil || !ok {
		return false, err // no-op needs no commit (nothing changed)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit revoke: %w", err)
	}
	return true, nil
}

// RevokeInTx flips a live session to revoked (the audited terminal flip, ADR-0068
// §D4) inside the caller's transaction — T-114 adds pairing.revoked here without
// restructuring. ok=false is a no-op (a malformed id, an unknown id, OR an
// already-revoked one), making a second Revoke on the same id idempotent by
// construction, mirroring grant.RevokeInTx. uuid.Parse rejects a malformed id
// BEFORE the UPDATE for the same no-oracle reason as GetLive/RedeemInTx
// (Commander review finding A) — though the paired-session id here is desk-listed
// rather than attacker-supplied, a single guard idiom across all three id-taking
// methods is easier to keep correct than three different postures.
func (s *Store) RevokeInTx(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return false, nil
	}

	ct, err := tx.Exec(ctx, `
		UPDATE paired_sessions SET revoked_at = now()
		 WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("revoke paired session: %w", err)
	}
	return ct.RowsAffected() == 1, nil
}

// CleanupRequests deletes consumed and expired pairing_requests whose expiry is at
// or before "before" — PERF-13's bounded sweep, folded by main.go into the existing
// runReaper ticker (no new goroutine, no new env var). It is best-effort by
// contract: the caller logs a failure and continues, never failing a request.
// Nothing here chains an audit event — AUD-33 by decision: a lapsed, unredeemed
// request is a non-event. Only pairing_requests is ever swept; paired_sessions
// liveness is COMPUTED and gets no sweeper at all (see Session's doc comment).
func (s *Store) CleanupRequests(ctx context.Context, before time.Time) (int, error) {
	ct, err := s.pool.Exec(ctx, `
		DELETE FROM pairing_requests
		 WHERE consumed_at IS NOT NULL OR expires_at <= $1`, before)
	if err != nil {
		return 0, fmt.Errorf("cleanup pairing requests: %w", err)
	}
	return int(ct.RowsAffected()), nil
}
