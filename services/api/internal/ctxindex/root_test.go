package ctxindex

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// newTestRoot builds a Root over a fresh temp directory seeded with one in-root file
// (notes/readme.md) so every attack case has a real, existing root to attack.
func newTestRoot(t *testing.T) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatalf("seed notes dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes", "readme.md"), []byte("in-root content"), 0o644); err != nil {
		t.Fatalf("seed readme: %v", err)
	}
	root, err := NewRoot(dir)
	if err != nil {
		t.Fatalf("new root: %v", err)
	}
	return root, dir
}

// TestRootReadConfinement — SEC-33/K5, the load-bearing security test: every
// traversal/escape vector is rejected with ErrOutsideRoot and NO file is ever
// read (the offending path's content never reaches the caller), while a genuine
// in-root file reads correctly.
func TestRootReadConfinement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows; this test runs in the Dockerized (linux) verify")
	}
	root, dir := newTestRoot(t)

	// An out-of-root sibling directory with content that must never surface,
	// however it is reached.
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside content — must never leak"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}

	// A symlink INSIDE the root pointing to the outside directory — the
	// classic indexer-escape vector (N2/ADR-0066 §1 red-team #4).
	if err := os.Symlink(outside, filepath.Join(dir, "escape-dir")); err != nil {
		t.Fatalf("seed symlink-to-dir: %v", err)
	}
	// A symlink INSIDE the root pointing directly at an outside FILE.
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "escape-file")); err != nil {
		t.Fatalf("seed symlink-to-file: %v", err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"dotdot traversal", "../../etc/passwd"},
		{"dotdot traversal via subdir", "notes/../../../etc/passwd"},
		{"absolute path", "/etc/passwd"},
		{"symlinked directory escape", "escape-dir/secret.txt"},
		{"symlinked file escape", "escape-file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := root.Read(tc.path)
			if !errors.Is(err, ErrOutsideRoot) {
				t.Fatalf("Read(%q) err = %v, want ErrOutsideRoot", tc.path, err)
			}
			if b != nil {
				t.Fatalf("Read(%q) returned content %q on a rejected path — must read nothing", tc.path, b)
			}
		})
	}

	// A genuine in-root file still reads correctly — the guard rejects escapes,
	// not legitimate access.
	got, err := root.Read("notes/readme.md")
	if err != nil {
		t.Fatalf("read in-root file: %v", err)
	}
	if string(got) != "in-root content" {
		t.Fatalf("in-root content = %q, want %q", got, "in-root content")
	}
}

// TestRootListNeverEscapes — K5's List half (WD-10 N2): a symlink inside the root
// (to a directory or a file) that would resolve outside is never followed and never
// appears in the listing, in either form; ordinary in-root files still surface.
func TestRootListNeverEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows; this test runs in the Dockerized (linux) verify")
	}
	root, dir := newTestRoot(t)

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside content"), 0o644); err != nil {
		t.Fatalf("seed outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape-dir")); err != nil {
		t.Fatalf("seed symlink-to-dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "escape-file")); err != nil {
		t.Fatalf("seed symlink-to-file: %v", err)
	}

	got, err := root.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	seen := map[string]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen["notes/readme.md"] {
		t.Fatalf("List() = %v, want it to include the in-root file notes/readme.md", got)
	}
	if seen["escape-dir/secret.txt"] {
		t.Fatalf("List() = %v, must never surface a path reached through a symlinked directory escape", got)
	}
	if seen["escape-file"] || seen["escape-dir"] {
		t.Fatalf("List() = %v, must never list a symlink entry itself", got)
	}
	for _, p := range got {
		if p == "escape-dir/secret.txt" || p == "escape-file" {
			t.Fatalf("List() surfaced an out-of-root target: %q", p)
		}
	}
}

// TestNewRootMissingDir — the confinement primitive itself fails loud on an absent
// root rather than silently succeeding over nothing (CTX-5's "errored" state is the
// caller's response to this error, not this package's job to paper over).
func TestNewRootMissingDir(t *testing.T) {
	if _, err := NewRoot(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("NewRoot over a missing directory: got nil error, want one")
	}
}
