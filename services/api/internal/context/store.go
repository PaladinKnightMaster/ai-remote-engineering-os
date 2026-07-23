package context

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no context source has the given (owner-scoped) id — the
// memory-store owner-404 pattern (SEC-31/N3): a foreign owner's id looks the same as an
// unknown one.
var ErrNotFound = errors.New("context source not found")

// selectCols is the shared column list for Get/List/Create (kept in one place so the
// scan order matches every call site).
const selectCols = `id, root_path, scope_type, scope_id, permission_scope, status, last_indexed_at, created_at, updated_at`

// Store persists context sources + their indexed chunks. It takes the pgx pool
// directly (the internal/run / internal/memory pattern) so the package stays
// extraction-ready. No outbox writer here (T-096): operator audit events land with the
// routes at T-100; the per-agent-read context.read chaining lands with SearchForAgent
// at T-098 — this ticket is data-op only.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds the context store.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateSource inserts a charter-bound context source (ADR-0066 §1). id is minted here
// (UUID v7, time-ordered) — never a DB default, the house convention every other store
// follows.
func (s *Store) CreateSource(ctx context.Context, in CreateSourceInput) (Source, error) {
	if in.Owner == "" || in.RootPath == "" || in.ScopeType == "" || in.ScopeID == "" {
		return Source{}, errors.New("context source requires an owner, root path, and scope")
	}
	id := uuid.Must(uuid.NewV7())

	row := s.pool.QueryRow(ctx, `
		INSERT INTO context_sources (id, root_path, scope_type, scope_id, permission_scope)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+selectCols,
		id.String(), in.RootPath, in.ScopeType, in.ScopeID, in.Owner)
	src, err := s.scan(row)
	if err != nil {
		return Source{}, fmt.Errorf("insert context source: %w", err)
	}
	return src, nil
}

// GetSource returns one owner-scoped context source, or ErrNotFound — including when
// the id exists but belongs to a different operator (the owner-404 house pattern,
// SEC-31/N3).
func (s *Store) GetSource(ctx context.Context, owner, id string) (Source, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+selectCols+` FROM context_sources WHERE id = $1 AND permission_scope = $2`, id, owner)
	src, err := s.scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("get context source: %w", err)
	}
	return src, nil
}

// ListSources returns owner-scoped context sources in id (≈ creation) order. When
// scopeType is "" every one of the owner's sources is returned (the operator overview
// case); otherwise the list narrows to (scopeType, scopeID) — the charter-bound list
// case (CTX-1/T-100). Non-owner rows never appear — the caller's owner param IS the
// filter, mirroring memory.List.
func (s *Store) ListSources(ctx context.Context, owner, scopeType, scopeID string) ([]Source, error) {
	var rows pgx.Rows
	var err error
	if scopeType == "" {
		rows, err = s.pool.Query(ctx, `
			SELECT `+selectCols+` FROM context_sources
			 WHERE permission_scope = $1
			 ORDER BY id ASC`, owner)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT `+selectCols+` FROM context_sources
			 WHERE permission_scope = $1 AND scope_type = $2 AND scope_id = $3
			 ORDER BY id ASC`, owner, scopeType, scopeID)
	}
	if err != nil {
		return nil, fmt.Errorf("list context sources: %w", err)
	}
	defer rows.Close()
	var out []Source
	for rows.Next() {
		src, serr := s.scan(rows)
		if serr != nil {
			return nil, serr
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// RemoveSource deletes one owner-scoped context source. ON DELETE CASCADE (migration
// 0014) removes its chunks in the same statement — no orphaned index rows (CTX-5).
// ErrNotFound when the id does not exist or belongs to a different owner.
func (s *Store) RemoveSource(ctx context.Context, owner, id string) error {
	ct, err := s.pool.Exec(ctx, `
		DELETE FROM context_sources WHERE id = $1 AND permission_scope = $2`, id, owner)
	if err != nil {
		return fmt.Errorf("remove context source: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReplaceSourceChunks is the incremental indexer's write path (T-097): it replaces
// every chunk of ONE source in a single transaction (delete-then-insert) and sets
// last_indexed_at = now() — the source's freshness signal (CTX-3). Chunk ids are
// minted here (UUID v7), not taken from the caller — the house convention every store
// follows; any ID/SourceID/CreatedAt the caller set on a Chunk is ignored. A nil or
// empty chunks slice is a valid call (a source with no readable content still records
// a fresh sweep, e.g. an empty directory).
func (s *Store) ReplaceSourceChunks(ctx context.Context, sourceID string, chunks []Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	// Confirm the source exists BEFORE touching chunks: an unknown sourceID must
	// answer ErrNotFound, not a raw foreign-key-violation error surfaced from the
	// insert loop below.
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM context_sources WHERE id = $1)`, sourceID).Scan(&exists); err != nil {
		return fmt.Errorf("check source exists: %w", err)
	}
	if !exists {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx, `DELETE FROM context_chunks WHERE source_id = $1`, sourceID); err != nil {
		return fmt.Errorf("delete existing chunks: %w", err)
	}

	for _, c := range chunks {
		id := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `
			INSERT INTO context_chunks (id, source_id, file_path, locator, content, file_hash)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			id.String(), sourceID, c.FilePath, c.Locator, c.Content, c.FileHash); err != nil {
			return fmt.Errorf("insert chunk %s#%s: %w", c.FilePath, c.Locator, err)
		}
	}

	ct, err := tx.Exec(ctx, `
		UPDATE context_sources SET last_indexed_at = now(), updated_at = now() WHERE id = $1`, sourceID)
	if err != nil {
		return fmt.Errorf("set last_indexed_at: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace source chunks: %w", err)
	}
	return nil
}

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query, via
// rows.Next()+scan) — the memory store's shared-scan-body pattern.
type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Store) scan(row rowScanner) (Source, error) {
	var src Source
	if err := row.Scan(&src.ID, &src.RootPath, &src.ScopeType, &src.ScopeID,
		&src.PermissionScope, &src.Status, &src.LastIndexedAt, &src.CreatedAt, &src.UpdatedAt); err != nil {
		return Source{}, err
	}
	return src, nil
}
