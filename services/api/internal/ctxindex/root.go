package ctxindex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot is returned by Root.Read whenever a requested path would resolve
// outside the root's confined directory (ADR-0066 §1, SEC-33/K5) — a `..` traversal,
// an absolute path, or a symlink escaping the root. No read of the offending path is
// attempted: every check below runs BEFORE any file content is touched, so an
// out-of-root access reads nothing, ever.
var ErrOutsideRoot = errors.New("path resolves outside the confined context-source root")

// Root confines file access to one operator-registered directory (ADR-0066 §1 — a
// context source's root_path). This is the load-bearing security guarantee behind
// SEC-33: a file-reading process in a security product is jailed to its declared
// source, full stop. It is built once per registered source and enforces containment
// on every subsequent Read/List call — no `..` traversal, no absolute path, no
// symlink resolving outside the root ever escapes it.
type Root struct {
	// path is the cleaned, absolute root directory as registered.
	path string
	// real is path with every symlink resolved (filepath.EvalSymlinks), captured
	// once at construction — the authority every later containment check compares
	// against, so a root itself reached via a symlink is handled uniformly with
	// everything under it.
	real string
}

// NewRoot builds a confined Root over dir. dir is resolved to a cleaned absolute path
// and its real (symlink-resolved) location is captured up front; a dir that does not
// exist or cannot be resolved is an error here (the caller — the indexer, T-097 —
// is responsible for marking a source errored/stale rather than crashing on this,
// per CTX-5; that policy lives one layer up, not in this confinement primitive).
func NewRoot(dir string) (*Root, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve root path: %w", err)
	}
	abs = filepath.Clean(abs)
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve root real path: %w", err)
	}
	return &Root{path: abs, real: real}, nil
}

// Path returns the root's cleaned, absolute directory (the registered root_path as
// resolved) — for display/diagnostics only; it is never itself used as a basis for a
// fresh containment decision (resolve always re-derives from r.path/r.real).
func (r *Root) Path() string { return r.path }

// resolve turns a caller-supplied relative path into its real, symlink-resolved
// absolute location IFF it stays inside the root — the one containment check every
// Read call routes through, in three layers:
//  1. reject outright: empty or ABSOLUTE input (SEC-33 — a source is addressed by
//     relative path only; an absolute path is never honored even if it would
//     coincidentally land inside the root).
//  2. reject a `..`-escape: filepath.Join + Clean collapses "../../etc/passwd" into a
//     path outside r.path — checked via withinDir BEFORE the filesystem is touched.
//  3. reject a symlink escape: filepath.EvalSymlinks resolves the WHOLE chain (the
//     target file or any parent directory component), so a path that looks contained
//     as a string but walks out via a symlink is still caught — the check a string
//     comparison alone cannot close.
func (r *Root) resolve(relPath string) (string, error) {
	if relPath == "" || filepath.IsAbs(relPath) {
		return "", ErrOutsideRoot
	}
	joined := filepath.Clean(filepath.Join(r.path, relPath))
	if !withinDir(joined, r.path) {
		return "", ErrOutsideRoot
	}
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("resolve real path: %w", err)
	}
	if !withinDir(real, r.real) {
		return "", ErrOutsideRoot
	}
	return real, nil
}

// withinDir reports whether target is dir itself or a descendant of dir, via
// filepath.Rel — so a sibling with a shared prefix (e.g. "/root-evil" against
// "/root") is never wrongly admitted the way a naive strings.HasPrefix would allow.
func withinDir(target, dir string) bool {
	rel, err := filepath.Rel(dir, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Read returns the contents of the file at relPath, confined to the root (SEC-33).
// Any path that would escape the root — via `..`, an absolute path, or a symlink
// resolving outside — returns ErrOutsideRoot; the file is never opened in that case.
func (r *Root) Read(relPath string) ([]byte, error) {
	real, err := r.resolve(relPath)
	if err != nil {
		if errors.Is(err, ErrOutsideRoot) {
			return nil, err
		}
		return nil, fmt.Errorf("resolve %q: %w", relPath, err)
	}
	b, err := os.ReadFile(real)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", relPath, err)
	}
	return b, nil
}

// List returns every regular file's path (relative to the root, slash-separated)
// reachable by walking the root directory (K5). Symlinks are never followed and
// never listed — even one that would happen to resolve back inside the root — which
// is what keeps a symlink escaping the root invisible to List as well as Read: a
// symlinked directory is a leaf to fs.WalkDir (it does not descend into one, so
// nothing beyond it is ever visited), and a symlinked file is explicitly skipped
// below. No out-of-root target can therefore ever appear in the returned slice.
func (r *Root) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(r.path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == r.path {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never followed, never listed — see doc comment
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(r.path, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list root: %w", err)
	}
	return out, nil
}
