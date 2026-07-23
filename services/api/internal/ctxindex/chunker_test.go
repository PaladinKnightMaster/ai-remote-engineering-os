package ctxindex

import (
	"strings"
	"testing"
)

// TestChunkFileMarkdownHeadings — K2: a Markdown file is chunked by heading section;
// each heading (at any depth) gets its own chunk whose locator is the slash-joined
// slug path of its heading ancestry, and whose content runs through the next heading
// at the same or a shallower depth (so a parent's chunk includes its child's text too).
func TestChunkFileMarkdownHeadings(t *testing.T) {
	md := "# Setup\n" +
		"top-level intro\n" +
		"## Prerequisites\n" +
		"install go\n" +
		"install docker\n" +
		"## Install\n" +
		"run the installer\n" +
		"# Usage\n" +
		"how to run it\n"

	chunks := ChunkFile("runbook.md", []byte(md))
	if len(chunks) != 4 {
		t.Fatalf("chunk count = %d, want 4 (Setup, Prerequisites, Install, Usage): %+v", len(chunks), chunks)
	}

	byLocator := map[string]Chunk{}
	for _, c := range chunks {
		byLocator[c.Locator] = c
		if c.FilePath != "runbook.md" {
			t.Fatalf("chunk %+v: FilePath = %q, want runbook.md", c, c.FilePath)
		}
	}

	setup, ok := byLocator["heading:setup"]
	if !ok {
		t.Fatalf("missing heading:setup chunk, got locators: %v", locators(chunks))
	}
	// The top-level Setup chunk's content spans through BOTH its nested
	// subsections (Prerequisites, Install) since neither closes it (both are
	// deeper than depth 1).
	if !strings.Contains(setup.Content, "top-level intro") ||
		!strings.Contains(setup.Content, "install go") ||
		!strings.Contains(setup.Content, "run the installer") {
		t.Fatalf("Setup chunk missing expected nested content: %q", setup.Content)
	}
	if strings.Contains(setup.Content, "how to run it") {
		t.Fatalf("Setup chunk must not bleed into the next H1 (Usage): %q", setup.Content)
	}

	prereq, ok := byLocator["heading:setup/prerequisites"]
	if !ok {
		t.Fatalf("missing heading:setup/prerequisites chunk, got locators: %v", locators(chunks))
	}
	if !strings.Contains(prereq.Content, "install go") || strings.Contains(prereq.Content, "run the installer") {
		t.Fatalf("Prerequisites chunk boundary wrong: %q", prereq.Content)
	}

	if _, ok := byLocator["heading:setup/install"]; !ok {
		t.Fatalf("missing heading:setup/install chunk, got locators: %v", locators(chunks))
	}
	usage, ok := byLocator["heading:usage"]
	if !ok {
		t.Fatalf("missing heading:usage chunk, got locators: %v", locators(chunks))
	}
	if !strings.Contains(usage.Content, "how to run it") {
		t.Fatalf("Usage chunk missing its own content: %q", usage.Content)
	}

	// Determinism: chunking the same input again yields byte-identical chunks
	// (same order, same locators, same content, same hash) — K2.
	again := ChunkFile("runbook.md", []byte(md))
	assertSameChunks(t, chunks, again)

	// Every chunk of the file carries the same FileHash.
	h := chunks[0].FileHash
	for _, c := range chunks {
		if c.FileHash != h {
			t.Fatalf("chunk %+v: FileHash = %q, want %q (every chunk of a file shares one hash)", c, c.FileHash, h)
		}
	}
}

// TestChunkFileMarkdownNoHeadings — a Markdown file with no heading yields exactly one
// "whole" chunk.
func TestChunkFileMarkdownNoHeadings(t *testing.T) {
	chunks := ChunkFile("notes.txt", []byte("just some plain notes\nwith two lines\n"))
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].Locator != "whole" {
		t.Fatalf("locator = %q, want %q", chunks[0].Locator, "whole")
	}
}

// TestChunkFileCodeLineWindow — K2: a non-Markdown file is chunked by a fixed,
// non-overlapping line window with a "lines:<start>-<end>" locator.
func TestChunkFileCodeLineWindow(t *testing.T) {
	var b strings.Builder
	const total = 205
	for i := 1; i <= total; i++ {
		b.WriteString("line\n")
	}
	chunks := ChunkFile("main.go", []byte(b.String()))

	wantChunks := []struct{ locator string }{
		{"lines:1-80"}, {"lines:81-160"}, {"lines:161-205"},
	}
	if len(chunks) != len(wantChunks) {
		t.Fatalf("chunk count = %d, want %d: %v", len(chunks), len(wantChunks), locators(chunks))
	}
	for i, w := range wantChunks {
		if chunks[i].Locator != w.locator {
			t.Fatalf("chunk %d locator = %q, want %q", i, chunks[i].Locator, w.locator)
		}
	}
	// The last (partial) window has exactly the remaining line count.
	lastLines := strings.Count(chunks[len(chunks)-1].Content, "\n") + 1
	if lastLines != 45 {
		t.Fatalf("last window line count = %d, want 45", lastLines)
	}

	again := ChunkFile("main.go", []byte(b.String()))
	assertSameChunks(t, chunks, again)
}

// TestChunkFileEmpty — an empty file yields zero chunks, for either strategy.
func TestChunkFileEmpty(t *testing.T) {
	if got := ChunkFile("empty.md", []byte{}); got != nil {
		t.Fatalf("empty markdown file: got %d chunks, want 0", len(got))
	}
	if got := ChunkFile("empty.go", []byte{}); got != nil {
		t.Fatalf("empty code file: got %d chunks, want 0", len(got))
	}
}

func locators(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Locator
	}
	return out
}

func assertSameChunks(t *testing.T, a, b []Chunk) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("chunk count differs across identical input: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Locator != b[i].Locator || a[i].Content != b[i].Content || a[i].FileHash != b[i].FileHash || a[i].FilePath != b[i].FilePath {
			t.Fatalf("chunk %d differs across identical input:\n a=%+v\n b=%+v", i, a[i], b[i])
		}
	}
}
