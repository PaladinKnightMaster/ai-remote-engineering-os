//go:build integration

// Integration test for the incremental indexer against a real Postgres + a real temp
// directory (T-097 G1). Run alongside store_integration_test.go's harness:
//
//	docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
//	export API_TEST_DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
//	go run ./services/api migrate up
//	go test -tags integration ./services/api/internal/ctxindex/...
package ctxindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIndexerSweepIncremental is the T-097 capstone: a fresh source with one
// Markdown file and one code file is fully indexed on the first sweep; editing ONE
// file re-chunks only that file — the OTHER file's chunk ids are byte-identical
// across the second sweep, proving the mtime/size-then-hash prefilter actually skips
// unchanged files rather than merely claiming to (K6); deleting a file removes its
// chunks; a source pointed at a missing root is marked errored WITHOUT the sweep
// itself erroring (CTX-5).
func TestIndexerSweepIncremental(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	ix := NewIndexer(s)

	dir := t.TempDir()
	mdPath := filepath.Join(dir, "readme.md")
	codePath := filepath.Join(dir, "main.go")
	writeFile(t, mdPath, "# Title\nhello world\n")
	writeFile(t, codePath, "package main\n\nfunc main() {}\n")

	wsID := freshID(t)
	src, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:indexer", RootPath: dir, ScopeType: ScopeWorkstream, ScopeID: wsID,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	if err := ix.Sweep(ctx); err != nil {
		t.Fatalf("sweep 1: %v", err)
	}

	got, err := s.GetSource(ctx, "operator:indexer", src.ID)
	if err != nil {
		t.Fatalf("get after sweep 1: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("status after sweep 1 = %q, want active", got.Status)
	}
	if got.LastIndexedAt == nil {
		t.Fatal("last_indexed_at must be set after sweep 1")
	}
	firstIndexedAt := *got.LastIndexedAt

	if n := countChunks(t, pool, src.ID); n == 0 {
		t.Fatal("sweep 1 must have indexed at least one chunk")
	}
	mdChunksBefore := chunkIDs(t, pool, src.ID, "readme.md")
	codeChunksBefore := chunkIDs(t, pool, src.ID, "main.go")
	if len(mdChunksBefore) == 0 || len(codeChunksBefore) == 0 {
		t.Fatalf("both files must be chunked: md=%d code=%d", len(mdChunksBefore), len(codeChunksBefore))
	}

	// Edit only the Markdown file — its size (hence its content, hence its hash)
	// changes; the code file is untouched.
	writeFile(t, mdPath, "# Title\nhello world EDITED\n")

	if err := ix.Sweep(ctx); err != nil {
		t.Fatalf("sweep 2 (after edit): %v", err)
	}

	got2, err := s.GetSource(ctx, "operator:indexer", src.ID)
	if err != nil {
		t.Fatalf("get after sweep 2: %v", err)
	}
	if got2.LastIndexedAt == nil || !got2.LastIndexedAt.After(firstIndexedAt) {
		t.Fatalf("last_indexed_at must advance on sweep 2: first=%v second=%v", firstIndexedAt, got2.LastIndexedAt)
	}

	mdChunksAfter := chunkIDs(t, pool, src.ID, "readme.md")
	if sameIDs(mdChunksBefore, mdChunksAfter) {
		t.Fatalf("the EDITED file's chunks must be replaced, got the same ids: %v", mdChunksAfter)
	}
	codeChunksAfter := chunkIDs(t, pool, src.ID, "main.go")
	if !sameIDs(codeChunksBefore, codeChunksAfter) {
		t.Fatalf("the UNCHANGED file must NOT be re-chunked (the incremental prefilter) — before=%v after=%v",
			codeChunksBefore, codeChunksAfter)
	}

	// Delete the code file — the next sweep must remove its chunks, leaving the
	// surviving file's chunks intact.
	if err := os.Remove(codePath); err != nil {
		t.Fatalf("remove code file: %v", err)
	}
	if err := ix.Sweep(ctx); err != nil {
		t.Fatalf("sweep 3 (after delete): %v", err)
	}
	if n := len(chunkIDs(t, pool, src.ID, "main.go")); n != 0 {
		t.Fatalf("deleted file must have zero chunks after sweep, got %d", n)
	}
	if n := len(chunkIDs(t, pool, src.ID, "readme.md")); n == 0 {
		t.Fatal("the surviving file's chunks must remain after sweep 3")
	}

	// A source pointed at a missing root is marked errored — the sweep itself
	// must not error out (CTX-5): a bad source never crashes the shared goroutine.
	missingWsID := freshID(t)
	missingSrc, err := s.CreateSource(ctx, CreateSourceInput{
		Owner: "operator:indexer", RootPath: filepath.Join(dir, "does-not-exist"), ScopeType: ScopeWorkstream, ScopeID: missingWsID,
	})
	if err != nil {
		t.Fatalf("create missing-root source: %v", err)
	}
	if err := ix.Sweep(ctx); err != nil {
		t.Fatalf("sweep over a missing root must not error: %v", err)
	}
	missingGot, err := s.GetSource(ctx, "operator:indexer", missingSrc.ID)
	if err != nil {
		t.Fatalf("get missing-root source: %v", err)
	}
	if missingGot.Status != StatusErrored {
		t.Fatalf("missing-root source status = %q, want errored", missingGot.Status)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func chunkIDs(t *testing.T, pool *pgxpool.Pool, sourceID, filePath string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id FROM context_chunks WHERE source_id = $1 AND file_path = $2 ORDER BY id`, sourceID, filePath)
	if err != nil {
		t.Fatalf("query chunk ids: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan chunk id: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("chunk id rows: %v", err)
	}
	return out
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
