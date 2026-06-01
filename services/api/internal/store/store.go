// Package store persists resources for services/api. Unlike audit-service this
// is a standard mutable CRUD store (no append-only / forward-only policy).
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/resource"
)

// ErrNotFound is returned when no resource has the given id.
var ErrNotFound = errors.New("resource not found")

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

// Delete removes a resource. ErrNotFound if no row matched.
func (s *Store) Delete(ctx context.Context, id string) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM resources WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
