package ctxindex

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// AllSources returns EVERY registered context source, regardless of owner or status
// (T-097): the background sweep is not an operator-plane read (unlike ListSources, it
// is never scoped to a caller), and it must retry an "errored" source too — a crashed
// or unreadable root can start reading again on a later sweep (CTX-5's self-heal).
func (s *Store) AllSources(ctx context.Context) ([]Source, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+selectCols+` FROM context_sources ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list all context sources: %w", err)
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

// FileState is one file's tracked incremental state (migration 0015): mtime+size is
// the sweep's cheap prefilter (K6); file_hash confirms a genuine content change once a
// candidate's mtime/size no longer matches.
type FileState struct {
	Mtime time.Time
	Size  int64
	Hash  string
}

// FileStates returns a source's current context_files state, keyed by file_path. The
// indexer's sweep loads this ONCE per source per pass and compares every listed file
// against it before touching file content (T-097, PERF-11(3)).
func (s *Store) FileStates(ctx context.Context, sourceID string) (map[string]FileState, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT file_path, mtime, size, file_hash FROM context_files WHERE source_id = $1`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list file states: %w", err)
	}
	defer rows.Close()
	out := map[string]FileState{}
	for rows.Next() {
		var path, hash string
		var st FileState
		if err := rows.Scan(&path, &st.Mtime, &st.Size, &hash); err != nil {
			return nil, fmt.Errorf("scan file state: %w", err)
		}
		st.Hash = hash
		out[path] = st
	}
	return out, rows.Err()
}

// ReindexFile is the incremental indexer's per-file write path (T-097, CTX-2/CTX-3):
// when one file's content hash has genuinely changed (or it is new), this replaces
// JUST that file's chunks and upserts its context_files tracking row, in a single
// transaction — every OTHER file's chunks and tracked state are left completely
// untouched, which is the incremental guarantee the indexer test proves. Chunk ids are
// minted here (UUID v7), like every other store write; any ID/SourceID/FileHash the
// caller set on a Chunk is ignored (hash is the explicit parameter, the one source of
// truth for both context_chunks and context_files).
func (s *Store) ReindexFile(ctx context.Context, sourceID, filePath string, mtime time.Time, size int64, hash string, chunks []Chunk) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	// Confirm the source exists BEFORE touching chunks/files: an unknown sourceID
	// must answer ErrNotFound, not a raw foreign-key-violation error surfaced from
	// the insert below (the ReplaceSourceChunks precedent).
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM context_sources WHERE id = $1)`, sourceID).Scan(&exists); err != nil {
		return fmt.Errorf("check source exists: %w", err)
	}
	if !exists {
		return ErrNotFound
	}

	if _, err := tx.Exec(ctx, `
		DELETE FROM context_chunks WHERE source_id = $1 AND file_path = $2`, sourceID, filePath); err != nil {
		return fmt.Errorf("delete existing chunks for %s: %w", filePath, err)
	}
	for _, c := range chunks {
		id := uuid.Must(uuid.NewV7())
		if _, err := tx.Exec(ctx, `
			INSERT INTO context_chunks (id, source_id, file_path, locator, content, file_hash)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			id.String(), sourceID, filePath, c.Locator, c.Content, hash); err != nil {
			return fmt.Errorf("insert chunk %s#%s: %w", filePath, c.Locator, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO context_files (source_id, file_path, mtime, size, file_hash, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (source_id, file_path) DO UPDATE
		   SET mtime = excluded.mtime, size = excluded.size, file_hash = excluded.file_hash, updated_at = now()`,
		sourceID, filePath, mtime, size, hash); err != nil {
		return fmt.Errorf("upsert context_files %s: %w", filePath, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit reindex file: %w", err)
	}
	return nil
}

// TouchFile refreshes a tracked file's mtime/size ONLY (T-097): the case where the
// mtime-then-hash prefilter's fast path missed (mtime or size changed) but the
// re-hash proved the content is unchanged — a touch, or a save reproducing identical
// bytes. Chunks are never touched. ErrNotFound when no context_files row exists yet
// for (sourceID, filePath) — the caller must have a prior state to "touch."
func (s *Store) TouchFile(ctx context.Context, sourceID, filePath string, mtime time.Time, size int64) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE context_files SET mtime = $3, size = $4, updated_at = now()
		 WHERE source_id = $1 AND file_path = $2`, sourceID, filePath, mtime, size)
	if err != nil {
		return fmt.Errorf("touch file %s: %w", filePath, err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveFile deletes one file's chunks and its context_files tracking row (T-097): the
// sweep calls this for any previously-tracked file no longer present under the
// confined root. A no-op (not an error) when nothing was tracked for it — the sweep's
// vanished-file pass is idempotent by construction.
func (s *Store) RemoveFile(ctx context.Context, sourceID, filePath string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	if _, err := tx.Exec(ctx, `
		DELETE FROM context_chunks WHERE source_id = $1 AND file_path = $2`, sourceID, filePath); err != nil {
		return fmt.Errorf("delete chunks for vanished %s: %w", filePath, err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM context_files WHERE source_id = $1 AND file_path = $2`, sourceID, filePath); err != nil {
		return fmt.Errorf("delete file state for vanished %s: %w", filePath, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit remove file: %w", err)
	}
	return nil
}

// MarkSwept records a clean sweep pass over a source (CTX-3): last_indexed_at
// advances to now and status flips (back) to active — the source's freshness signal.
// ErrNotFound when the source no longer exists (removed mid-sweep).
func (s *Store) MarkSwept(ctx context.Context, sourceID string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE context_sources SET last_indexed_at = now(), status = $2, updated_at = now()
		 WHERE id = $1`, sourceID, StatusActive)
	if err != nil {
		return fmt.Errorf("mark swept: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkErrored records a source whose root is unreadable/absent (CTX-5): status flips
// to errored (surfaced to the operator) rather than crashing the sweep.
// last_indexed_at is left untouched (its staleness IS the honest signal — CTX-3/N3).
// ErrNotFound when the source no longer exists.
func (s *Store) MarkErrored(ctx context.Context, sourceID string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE context_sources SET status = $2, updated_at = now() WHERE id = $1`, sourceID, StatusErrored)
	if err != nil {
		return fmt.Errorf("mark errored: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
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
