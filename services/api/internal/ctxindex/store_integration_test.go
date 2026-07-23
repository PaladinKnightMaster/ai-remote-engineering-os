//go:build integration

// Integration tests for the context-index store against a real Postgres (T-096 G1).
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/ctxindex/...
package ctxindex

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := testDSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool), pool
}

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("API_TEST_DATABASE_URL not set")
	}
	return dsn
}

// freshID mints a fresh UUID v7 per test run (the T-073 lesson: fixed ids are not
// re-runnable against a dirty DB).
func freshID(t *testing.T) string {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("mint id: %v", err)
	}
	return id.String()
}

// TestCreateAndGetSource — CTX-1 G1: CreateSource mints a fresh id and persists the
// charter-bound source; GetSource is owner-scoped — ErrNotFound (never a silent
// success) when the id belongs to a different operator or does not exist (the
// memory-store owner-404 pattern, SEC-31/N3).
func TestCreateAndGetSource(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	wsID := freshID(t)

	created, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:alice", RootPath: "/srv/docs/" + wsID, ScopeType: ScopeWorkstream, ScopeID: wsID,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create source must mint an id")
	}
	if created.Status != StatusActive {
		t.Fatalf("status = %q, want default %q", created.Status, StatusActive)
	}
	if created.LastIndexedAt != nil {
		t.Fatalf("last_indexed_at must be nil before the first sweep, got %v", created.LastIndexedAt)
	}

	got, err := s.GetSource(ctx, "operator:alice", created.ID)
	if err != nil {
		t.Fatalf("owner get: %v", err)
	}
	if got.RootPath != created.RootPath || got.ScopeID != wsID {
		t.Fatalf("get mismatch: %+v", got)
	}

	if _, err := s.GetSource(ctx, "operator:stranger", created.ID); err != ErrNotFound {
		t.Fatalf("stranger get: got %v, want ErrNotFound", err)
	}
	if _, err := s.GetSource(ctx, "operator:alice", "00000000-0000-0000-0000-000000000000"); err != ErrNotFound {
		t.Fatalf("unknown id get: got %v, want ErrNotFound", err)
	}

	// A second create mints a distinct id, even for the same charter (dirty-DB
	// re-run safety, the T-073 lesson).
	again, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:alice", RootPath: "/srv/docs2/" + wsID, ScopeType: ScopeWorkstream, ScopeID: wsID,
	})
	if err != nil {
		t.Fatalf("create source (second): %v", err)
	}
	if again.ID == created.ID {
		t.Fatalf("CreateSource must mint a fresh id per call, got a repeat: %s", again.ID)
	}
}

// TestListSources — owner+scope filtered (CTX-1): a stranger's list of the same
// charter is empty; scoping to a charter returns only that charter's sources; an
// unscoped list returns every one of the owner's sources.
func TestListSources(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	wsA, wsB := freshID(t), freshID(t)
	// A fresh owner per run (the T-073 lesson): ListSources' unscoped branch
	// filters ONLY by owner, so a fixed owner string would accumulate rows across
	// re-runs against a dirty DB and break the exact-count assertions below.
	owner := "operator:lister-" + freshID(t)

	a1, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: owner, RootPath: "/srv/a1", ScopeType: ScopeWorkstream, ScopeID: wsA,
	})
	if err != nil {
		t.Fatalf("create a1: %v", err)
	}
	a2, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: owner, RootPath: "/srv/a2", ScopeType: ScopeWorkstream, ScopeID: wsA,
	})
	if err != nil {
		t.Fatalf("create a2: %v", err)
	}
	b1, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: owner, RootPath: "/srv/b1", ScopeType: ScopeWorkstream, ScopeID: wsB,
	})
	if err != nil {
		t.Fatalf("create b1: %v", err)
	}

	scoped, err := s.ListSources(ctx, owner, ScopeWorkstream, wsA)
	if err != nil {
		t.Fatalf("list scoped: %v", err)
	}
	if len(scoped) != 2 {
		t.Fatalf("scoped list = %d rows, want 2", len(scoped))
	}
	ids := map[string]bool{scoped[0].ID: true, scoped[1].ID: true}
	if !ids[a1.ID] || !ids[a2.ID] {
		t.Fatalf("scoped list missing an expected row: %+v", scoped)
	}

	unscoped, err := s.ListSources(ctx, owner, "", "")
	if err != nil {
		t.Fatalf("list unscoped: %v", err)
	}
	if len(unscoped) != 3 {
		t.Fatalf("unscoped list = %d rows, want 3", len(unscoped))
	}

	strangerScoped, err := s.ListSources(ctx, "operator:stranger", ScopeWorkstream, wsA)
	if err != nil || len(strangerScoped) != 0 {
		t.Fatalf("stranger scoped list: rows=%d err=%v, want 0", len(strangerScoped), err)
	}

	if len(b1.ID) == 0 {
		t.Fatal("b1 create must have produced an id")
	}
}

// TestRemoveSourceCascadesChunks — CTX-5: removing an owner-scoped source removes its
// chunks in the same statement (ON DELETE CASCADE) — no orphaned index rows; a
// foreign owner's remove is ErrNotFound and leaves the row untouched.
func TestRemoveSourceCascadesChunks(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	wsID := freshID(t)

	src, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:remover", RootPath: "/srv/rm", ScopeType: ScopeWorkstream, ScopeID: wsID,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if err := s.ReplaceSourceChunks(ctx, src.ID, []Chunk{
		{FilePath: "readme.md", Locator: "intro", Content: "hello world", FileHash: "h1"},
	}); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}

	if err := s.RemoveSource(ctx, "operator:stranger", src.ID); err != ErrNotFound {
		t.Fatalf("stranger remove: got %v, want ErrNotFound", err)
	}
	if n := countChunks(t, pool, src.ID); n != 1 {
		t.Fatalf("chunk count after stranger's failed remove = %d, want 1 (untouched)", n)
	}

	if err := s.RemoveSource(ctx, "operator:remover", src.ID); err != nil {
		t.Fatalf("owner remove: %v", err)
	}
	if n := countChunks(t, pool, src.ID); n != 0 {
		t.Fatalf("chunk count after owner remove = %d, want 0 (cascade)", n)
	}
	if _, err := s.GetSource(ctx, "operator:remover", src.ID); err != ErrNotFound {
		t.Fatalf("get after remove: got %v, want ErrNotFound", err)
	}

	// A second remove of the now-absent id is ErrNotFound, not a crash.
	if err := s.RemoveSource(ctx, "operator:remover", src.ID); err != ErrNotFound {
		t.Fatalf("re-remove: got %v, want ErrNotFound", err)
	}
}

// TestReplaceSourceChunks — T-097's write path (CTX-2/CTX-3): the first sweep inserts
// every chunk and sets last_indexed_at; a second sweep with a DIFFERENT chunk set
// fully replaces the first (delete-then-insert, in one transaction) rather than
// accumulating, and last_indexed_at advances again.
func TestReplaceSourceChunks(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	wsID := freshID(t)

	src, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:sweeper", RootPath: "/srv/sweep", ScopeType: ScopeWorkstream, ScopeID: wsID,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if src.LastIndexedAt != nil {
		t.Fatal("precondition: last_indexed_at must start nil")
	}

	if err := s.ReplaceSourceChunks(ctx, src.ID, []Chunk{
		{FilePath: "a.md", Locator: "intro", Content: "alpha content", FileHash: "hash-a"},
		{FilePath: "b.md", Locator: "intro", Content: "beta content", FileHash: "hash-b"},
	}); err != nil {
		t.Fatalf("replace (pass 1): %v", err)
	}
	if n := countChunks(t, pool, src.ID); n != 2 {
		t.Fatalf("chunk count after pass 1 = %d, want 2", n)
	}

	afterFirst, err := s.GetSource(ctx, "operator:sweeper", src.ID)
	if err != nil {
		t.Fatalf("get after pass 1: %v", err)
	}
	if afterFirst.LastIndexedAt == nil {
		t.Fatal("last_indexed_at must be set after the first sweep")
	}
	firstIndexedAt := *afterFirst.LastIndexedAt

	// A second sweep with only ONE (different) chunk fully replaces the first two —
	// no accumulation.
	if err := s.ReplaceSourceChunks(ctx, src.ID, []Chunk{
		{FilePath: "c.md", Locator: "intro", Content: "gamma content", FileHash: "hash-c"},
	}); err != nil {
		t.Fatalf("replace (pass 2): %v", err)
	}
	if n := countChunks(t, pool, src.ID); n != 1 {
		t.Fatalf("chunk count after pass 2 = %d, want 1 (replaced, not accumulated)", n)
	}
	if hasChunkFile(t, pool, src.ID, "a.md") || hasChunkFile(t, pool, src.ID, "b.md") {
		t.Fatal("pass 2 must have removed the pass-1 chunks")
	}
	if !hasChunkFile(t, pool, src.ID, "c.md") {
		t.Fatal("pass 2's chunk must be present")
	}

	afterSecond, err := s.GetSource(ctx, "operator:sweeper", src.ID)
	if err != nil {
		t.Fatalf("get after pass 2: %v", err)
	}
	if afterSecond.LastIndexedAt == nil || !afterSecond.LastIndexedAt.After(firstIndexedAt) {
		t.Fatalf("last_indexed_at must advance on the second sweep: first=%v second=%v", firstIndexedAt, afterSecond.LastIndexedAt)
	}

	// Replacing chunks for an unknown source id is ErrNotFound (no phantom write).
	if err := s.ReplaceSourceChunks(ctx, "00000000-0000-0000-0000-000000000000", []Chunk{
		{FilePath: "x.md", Locator: "intro", Content: "x", FileHash: "hx"},
	}); err != ErrNotFound {
		t.Fatalf("replace for unknown source: got %v, want ErrNotFound", err)
	}

	// An empty chunk set is a valid sweep (e.g. an empty directory) — it clears any
	// existing chunks and still advances last_indexed_at.
	if err := s.ReplaceSourceChunks(ctx, src.ID, nil); err != nil {
		t.Fatalf("replace with empty set: %v", err)
	}
	if n := countChunks(t, pool, src.ID); n != 0 {
		t.Fatalf("chunk count after empty sweep = %d, want 0", n)
	}
}

func countChunks(t *testing.T, pool *pgxpool.Pool, sourceID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM context_chunks WHERE source_id = $1`, sourceID).Scan(&n); err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	return n
}

func hasChunkFile(t *testing.T, pool *pgxpool.Pool, sourceID, filePath string) bool {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM context_chunks WHERE source_id = $1 AND file_path = $2`, sourceID, filePath).Scan(&n); err != nil {
		t.Fatalf("count chunk file: %v", err)
	}
	return n > 0
}
