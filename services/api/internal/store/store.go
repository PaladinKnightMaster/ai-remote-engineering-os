// Package store persists resources for services/api. Unlike audit-service this
// is a standard mutable CRUD store (no append-only / forward-only policy).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/resource"
)

// ErrNotFound is returned when no resource has the given id.
var ErrNotFound = errors.New("resource not found")

// ErrInUse is returned when a resource cannot be deleted because it is still
// referenced by a non-terminal session, run, or workstream (T-118c's
// referential guard). Distinct from ErrNotFound so the handler answers a
// clean 409 instead of the raw FK-violation 500 a naive delete would surface
// for sessions (a real FK), or the silent Run/Workstream orphan it would leave
// for the other two (opaque TEXT references, no FK).
var ErrInUse = errors.New("resource in use")

// resourceEdge is the bracket edge for resource lifecycle events — deleting a
// resource is an operator action on a governed object (T-118c), the same
// human-agent posture as grant/workstream lifecycle events
// (internal/grant/store.go, internal/workstream/store.go).
const resourceEdge = "human-agent"

// Outbox table + notify channel for services/api (T-022). Named distinctly from
// the broker's default "outbox" because both share the cockpit database
// (ADR-0048); each producer owns its own outbox + channel. main.go points api's
// relay at these too.
const (
	OutboxTable   = "api_outbox"
	OutboxChannel = "api_outbox_new"
)

type Store struct {
	pool   *pgxpool.Pool
	writer outbox.Writer
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, writer: outbox.Writer{Table: OutboxTable}}
}

// RecordEvent enqueues one audit evidence row into api's outbox (T-022 / SEC-9).
// api's auth events are env-based / stateless — there is no co-located state
// change — so the enqueue stands alone in its own transaction; the in-process
// relay (main.go) drains it to the audit-service chain authority.
func (s *Store) RecordEvent(ctx context.Context, e outbox.Event) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed
	if _, err := s.writer.Enqueue(ctx, tx, e); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

const selectCols = `SELECT id, type, name, COALESCE(site_id,''), labels FROM resources`

// Create inserts a resource with a server-assigned UUID v7 id (time-ordered, so
// it doubles as the list cursor).
func (s *Store) Create(ctx context.Context, in resource.Input) (resource.Resource, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return resource.Resource{}, fmt.Errorf("mint id: %w", err)
	}
	labels, err := json.Marshal(orEmpty(in.Labels))
	if err != nil {
		return resource.Resource{}, fmt.Errorf("marshal labels: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO resources (id, type, name, site_id, labels)
		VALUES ($1,$2,$3,NULLIF($4,''),$5::jsonb)`,
		id.String(), in.Type, in.Name, in.SiteID, string(labels))
	if err != nil {
		return resource.Resource{}, fmt.Errorf("insert resource: %w", err)
	}
	return resource.Resource{ID: id.String(), Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}, nil
}

// Get returns one resource by id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (resource.Resource, error) {
	r, err := scanResource(s.pool.QueryRow(ctx, selectCols+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return resource.Resource{}, ErrNotFound
	}
	return r, err
}

// List returns up to limit resources in id (≈ creation) order with id > after
// (after="" starts at the head). The cursor is the last id, or "" when empty.
func (s *Store) List(ctx context.Context, after string, limit int) ([]resource.Resource, string, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, selectCols+` ORDER BY id ASC LIMIT $1`, limit)
	} else {
		rows, err = s.pool.Query(ctx, selectCols+` WHERE id > $1 ORDER BY id ASC LIMIT $2`, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []resource.Resource
	var last string
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, r)
		last = r.ID
	}
	return out, last, rows.Err()
}

// Update replaces the mutable fields of an existing resource. ErrNotFound if no
// row matched.
func (s *Store) Update(ctx context.Context, id string, in resource.Input) (resource.Resource, error) {
	labels, err := json.Marshal(orEmpty(in.Labels))
	if err != nil {
		return resource.Resource{}, fmt.Errorf("marshal labels: %w", err)
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE resources SET type=$2, name=$3, site_id=NULLIF($4,''), labels=$5::jsonb, updated_at=now()
		WHERE id=$1`,
		id, in.Type, in.Name, in.SiteID, string(labels))
	if err != nil {
		return resource.Resource{}, fmt.Errorf("update resource: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return resource.Resource{}, ErrNotFound
	}
	return resource.Resource{ID: id, Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}, nil
}

// Delete removes a resource IF nothing non-terminal still references it
// (referentialGuard — sessions.resource_id is a real FK that would otherwise
// surface as a raw 500; runs/workstreams reference resources via their step
// tables with no FK at all, so a naive delete would silently orphan Run/
// Workstream history). ErrInUse on a live reference, ErrNotFound if no row
// matched. On success, resource.deleted chains in the SAME transaction as the
// row delete (T-118c) — a NEWLY MINTED convention, not an existing one:
// Create/Update remain unaudited (a disclosed v1.0 security-audit residual,
// not fixed here).
//
// A finding from the live-Postgres test, not anticipated by the guard's own
// doc comment: sessions.resource_id has NO `ON DELETE` clause (migration
// 0002), so Postgres itself still refuses the physical DELETE while ANY
// session row references the resource — including a TERMINAL (closed) one,
// which referentialGuard's pre-check deliberately does not flag. Rather than
// delete or null out that historical session row to force the delete through
// (which would break the SAME "history is immutable" principle this file
// leans on for Runs/Workstreams — see referentialGuard's doc comment), the
// fallback below maps that FK violation (23503) to the same ErrInUse: a
// resource that has EVER had a session opened against it, even long closed,
// cannot be deleted without a migration relaxing the FK (a disclosed v1.x
// residual, not fixed here) — stricter than "terminal allows" for this one
// leg, but never a raw 500.
func (s *Store) Delete(ctx context.Context, actor, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	inUse, err := referentialGuard(ctx, tx, id)
	if err != nil {
		return fmt.Errorf("check resource references: %w", err)
	}
	if inUse {
		return ErrInUse
	}

	ct, err := tx.Exec(ctx, `DELETE FROM resources WHERE id=$1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			return ErrInUse
		}
		return fmt.Errorf("delete resource: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: actor, Edge: resourceEdge,
		Target: "resource:" + id, Outcome: "resource.deleted",
		EvidencePacketID: "resource:" + id,
	}); err != nil {
		return fmt.Errorf("enqueue resource.deleted: %w", err)
	}

	return tx.Commit(ctx)
}

// referentialGuard reports whether id is referenced by any NON-terminal
// session, run, or workstream — the FAST, clean path for the common case.
// Terminal sets (verified against the CHECK constraints, not assumed — see
// the migrations cited): sessions terminal = {closed, terminated}
// (migrations 0002, 0005 — non-terminal: pending, pending_approval, active);
// runs terminal = {completed, failed, canceled} (migration 0006, extended by
// 0013 — non-terminal: proposed, pending, running); workstreams terminal =
// {retired} (migration 0011 — non-terminal: active, paused). runs/workstreams
// carry resource_id on their STEP tables, not on the parent row itself
// (run_steps/workstream_steps — migrations 0006, 0011), so both legs join
// through the step table and gate on the PARENT's status, matching the
// terminal sets above (run_steps/workstream_steps have no status column of
// their own that means the same thing).
//
// This check alone is NOT sufficient for sessions specifically: it only
// screens non-terminal rows, but a TERMINAL session row still physically
// blocks the delete via the hard FK (no `ON DELETE` clause) — see Delete's
// fallback mapping for that case. Runs/Workstreams have no such FK, so a
// false here for them is the full, final answer.
func referentialGuard(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var inUse bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM sessions
		 WHERE resource_id = $1 AND status NOT IN ('closed', 'terminated')
		UNION ALL
		SELECT 1 FROM run_steps rs JOIN runs r ON r.id = rs.run_id
		 WHERE rs.resource_id = $2 AND r.status NOT IN ('completed', 'failed', 'canceled')
		UNION ALL
		SELECT 1 FROM workstream_steps ws JOIN workstreams w ON w.id = ws.workstream_id
		 WHERE ws.resource_id = $2 AND w.status <> 'retired'
	)`, id, id).Scan(&inUse)
	if err != nil {
		return false, err
	}
	return inUse, nil
}

type scanner interface{ Scan(dest ...any) error }

func scanResource(row scanner) (resource.Resource, error) {
	var (
		r      resource.Resource
		labels []byte
	)
	if err := row.Scan(&r.ID, &r.Type, &r.Name, &r.SiteID, &labels); err != nil {
		return resource.Resource{}, err
	}
	if len(labels) > 0 {
		if err := json.Unmarshal(labels, &r.Labels); err != nil {
			return resource.Resource{}, fmt.Errorf("unmarshal labels: %w", err)
		}
	}
	if len(r.Labels) == 0 {
		r.Labels = nil
	}
	return r, nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
