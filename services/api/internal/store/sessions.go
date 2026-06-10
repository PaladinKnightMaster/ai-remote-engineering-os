package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aireos.local/services/api/internal/session"
)

// sessionColList is shared by SELECT (read paths) and INSERT/UPDATE ... RETURNING
// (write paths) so the column order scanSession expects can never drift between
// them. COALESCE collapses the nullable text columns to ” for a clean scan into
// non-pointer string fields; ended_at stays nullable (scanned into *time.Time).
const sessionColList = `id, resource_id, operator_id, mode, status, started_at, ended_at, ` +
	`COALESCE(closed_reason,''), COALESCE(cast_path,''), COALESCE(cast_sha256,'')`

const sessionSelect = `SELECT ` + sessionColList + ` FROM sessions`

// CreateSession inserts a session in 'pending' status with a server-assigned
// UUID v7 id (time-ordered, so it doubles as the list cursor) owned by
// operatorID. A foreign-key violation means the resourceId is unknown — surfaced
// as ErrNotFound so the handler answers 404 rather than 500.
func (s *Store) CreateSession(ctx context.Context, operatorID string, in session.CreateInput) (session.Session, error) {
	return s.insertSession(ctx, operatorID, in, session.StatusPending)
}

// ParkForApproval inserts a session in 'pending_approval' (ADR-0054, T-031b): a
// session-start to a requires_approval resource, awaiting the operator's decision.
// The broker's status guard rejects it until it is flipped to 'pending' on
// approval (T-031b-ii). Same FK-violation → ErrNotFound mapping as CreateSession.
func (s *Store) ParkForApproval(ctx context.Context, operatorID string, in session.CreateInput) (session.Session, error) {
	return s.insertSession(ctx, operatorID, in, session.StatusPendingApproval)
}

// insertSession is the shared insert path for CreateSession / ParkForApproval; the
// only difference is the initial status (decided by the caller).
func (s *Store) insertSession(ctx context.Context, operatorID string, in session.CreateInput, status string) (session.Session, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return session.Session{}, fmt.Errorf("mint id: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (id, resource_id, operator_id, mode, status)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING `+sessionColList,
		id.String(), in.ResourceID, operatorID, in.Mode, status)
	sess, err := scanSession(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			return session.Session{}, ErrNotFound
		}
		return session.Session{}, fmt.Errorf("insert session: %w", err)
	}
	return sess, nil
}

// ApproveSession flips a parked session (owned by operatorID) from
// 'pending_approval' to 'pending' so the broker may attach (ADR-0054 / T-031b-ii).
// The `status='pending_approval'` guard makes it a no-op on an already-resolved
// session — ErrNotFound then, which the handler maps to 409.
func (s *Store) ApproveSession(ctx context.Context, operatorID, id string) (session.Session, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE sessions
		   SET status = 'pending'
		 WHERE id=$1 AND operator_id=$2 AND status='pending_approval'
		RETURNING `+sessionColList, id, operatorID)
	sess, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, ErrNotFound
	}
	return sess, err
}

// MarkClosed force-closes a session owned by operatorID with an EXPLICIT reason
// (unlike EndSession, which COALESCEs to 'operator'). T-031b uses it to fail-close
// a parked session when the policy decision point is unreachable at create.
// ErrNotFound if the session is absent or owned by another operator.
func (s *Store) MarkClosed(ctx context.Context, operatorID, id, reason string) (session.Session, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE sessions
		   SET status        = 'closed',
		       ended_at      = COALESCE(ended_at, now()),
		       closed_reason = $3
		WHERE id=$1 AND operator_id=$2
		RETURNING `+sessionColList, id, operatorID, reason)
	sess, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, ErrNotFound
	}
	return sess, err
}

// ReapStale closes sessions stranded in a PRE-OPEN state past their cutoff
// (T-032, the reaper ADR-0049 anticipated): a 'pending' session no broker ever
// attached to, or a 'pending_approval' session whose approval was never resolved
// (policy-service auto-denies the approval but cannot write this api session row).
// It is a status-only janitor — these sessions never opened, so NO session.close
// is emitted (the broker owns that for connected sessions; the denial is already
// audit-chained by policy-service). Reuses the existing closed_reason values
// (pending → ttl, pending_approval → approval_timeout). Idempotent: a reaped row
// is 'closed', so it falls out of the next sweep. ACTIVE sessions are never
// touched — a live one is indistinguishable from a broker-crashed one without a
// heartbeat (that audit-close is deferred to v0.3.0). Returns the number reaped.
func (s *Store) ReapStale(ctx context.Context, pendingCutoff, approvalCutoff time.Time) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE sessions
		   SET status        = 'closed',
		       ended_at      = now(),
		       closed_reason = CASE WHEN status = 'pending_approval' THEN 'approval_timeout' ELSE 'ttl' END
		 WHERE (status = 'pending'          AND started_at < $1)
		    OR (status = 'pending_approval' AND started_at < $2)`,
		pendingCutoff, approvalCutoff)
	if err != nil {
		return 0, fmt.Errorf("reap stale sessions: %w", err)
	}
	return ct.RowsAffected(), nil
}

// GetSession returns one session owned by operatorID, or ErrNotFound. The
// operator scope is the security boundary: a session owned by another operator
// is indistinguishable from a missing one (ADR-0048 §5, no cross-operator scope
// at v0.1.0).
func (s *Store) GetSession(ctx context.Context, operatorID, id string) (session.Session, error) {
	sess, err := scanSession(s.pool.QueryRow(ctx,
		sessionSelect+` WHERE id=$1 AND operator_id=$2`, id, operatorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, ErrNotFound
	}
	return sess, err
}

// ListSessions returns up to limit of operatorID's sessions in id (≈ creation)
// order with id > after (after="" starts at the head). Backed by the
// (operator_id, id) index from migration 0002. The cursor is the last id, or ""
// when empty.
func (s *Store) ListSessions(ctx context.Context, operatorID, after string, limit int) ([]session.Session, string, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx,
			sessionSelect+` WHERE operator_id=$1 ORDER BY id ASC LIMIT $2`, operatorID, limit)
	} else {
		rows, err = s.pool.Query(ctx,
			sessionSelect+` WHERE operator_id=$1 AND id>$2 ORDER BY id ASC LIMIT $3`, operatorID, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []session.Session
	var last string
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, sess)
		last = sess.ID
	}
	return out, last, rows.Err()
}

// EndSession records the operator's control-plane intent to end a session: a
// pending/active session transitions to 'closed' with ended_at + closed_reason
// stamped. The CASE keeps an already-ended session (closed/terminated) unchanged
// so a repeated end is idempotent and never downgrades a 'terminated' row. The
// broker still performs the real teardown and emits session.close (slice b);
// this is the REST-side state transition only. ErrNotFound if the session does
// not exist or is owned by another operator.
func (s *Store) EndSession(ctx context.Context, operatorID, id string) (session.Session, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE sessions
		SET status        = CASE WHEN status IN ('pending','active') THEN 'closed' ELSE status END,
		    ended_at      = COALESCE(ended_at, now()),
		    closed_reason = COALESCE(closed_reason, 'operator')
		WHERE id=$1 AND operator_id=$2
		RETURNING `+sessionColList, id, operatorID)
	sess, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, ErrNotFound
	}
	return sess, err
}

func scanSession(row scanner) (session.Session, error) {
	var sess session.Session
	if err := row.Scan(
		&sess.ID, &sess.ResourceID, &sess.OperatorID, &sess.Mode, &sess.Status,
		&sess.StartedAt, &sess.EndedAt, &sess.ClosedReason, &sess.CastPath, &sess.CastSha256,
	); err != nil {
		return session.Session{}, err
	}
	return sess, nil
}
