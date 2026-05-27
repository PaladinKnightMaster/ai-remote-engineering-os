// forbidden-features-check — T-011. Enforces ADR-0046's forbidden-features
// list by path-existence assertion. The presence of ANY file matching
// the patterns below is a release-blocker (criteria SEC-8).
//
// ADR-0046 §Decision explicitly forbids at P0:
//   - password reset UI / flow
//   - self-service signup
//   - OAuth provider integration
//   - JWKS-style asymmetric rotation
//
// Each of these has well-known filename patterns; if a contributor lands
// a file matching them, the lint surfaces the violation. The check is
// path-based (not content-based) so it catches even draft / TODO stubs.
//
// Usage:
//   forbidden-features-check <repo-root>
//
// Exit 0 = clean. Exit 1 = at least one match (printed to stderr). The
// search skips node_modules, .git, dist, build, and the tool's own
// testdata directory.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// File-basename patterns that signal a forbidden feature. Match against
// the basename (case-insensitive) — generous on prefixes/suffixes since
// a contributor could name the file in many ways.
// Character classes: dash must be at the end of the class (or escaped)
// because Go's regex parses `[_-.]` as a range _–. which is invalid
// (0x5F > 0x2E). Using `[_.-]` puts the dash last → literal.
//
// Each pattern intentionally allows END-OF-STRING after the keyword so
// directory names (e.g. an `oauth/` directory containing benign filenames)
// are caught. Without `$` anchoring, `oauth[_-]` requires a char after —
// matches `oauth_google.go` but misses bare `oauth/`. T-011 review pass
// surfaced this as a directory-bypass vector.
var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)password[_-]?reset`),
	regexp.MustCompile(`(?i)(^|[_-])signup($|[_.-])`),
	regexp.MustCompile(`(?i)(^|[_-])oauth($|[_-])`),
	regexp.MustCompile(`(?i)jwks`),
}

// Directories to skip entirely — irrelevant scope or vendored content.
var skipDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	"dist":         {},
	"build":        {},
	".vite":        {},
	".task":        {},
	"testdata":     {}, // tools' own fixtures; the test exercises them via subdir search.
}

type match struct {
	Path    string
	Pattern string
}

func scan(root string) ([]match, error) {
	var out []match
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if _, skip := skipDirs[name]; skip {
				return filepath.SkipDir
			}
			// Directory-name check — added by the T-011 review pass.
			// A directory named oauth/ or signup/ signals feature
			// presence even if no individual filename matches.
			// Root dir itself ("." or absolute path) is exempt — the
			// scan root is passed by the operator, not authored.
			if p != root {
				for _, re := range patterns {
					if re.MatchString(name) {
						out = append(out, match{Path: p, Pattern: re.String() + " (directory)"})
						break
					}
				}
			}
			return nil
		}
		// Only inspect Go source + TypeScript / TSX. Other files (docs,
		// SQL, configs) might legitimately contain the keyword without
		// shipping the feature. Known limit at v0.1.0: .sql / .json
		// filenames could bypass; accepted trade-off vs. false-positive
		// surface on docs.
		if !strings.HasSuffix(name, ".go") &&
			!strings.HasSuffix(name, ".ts") &&
			!strings.HasSuffix(name, ".tsx") {
			return nil
		}
		for _, re := range patterns {
			if re.MatchString(name) {
				out = append(out, match{Path: p, Pattern: re.String()})
				break
			}
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forbidden-features-check: resolve %q: %v\n", root, err)
		os.Exit(2)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "forbidden-features-check: %s is not a directory\n", root)
		os.Exit(2)
	}
	hits, err := scan(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forbidden-features-check: scan %s: %v\n", root, err)
		os.Exit(2)
	}
	if len(hits) == 0 {
		fmt.Printf("forbidden-features-check: %s: no forbidden-feature files present (ADR-0046).\n", root)
		return
	}
	for _, m := range hits {
		fmt.Fprintf(os.Stderr,
			"forbidden-features-check: %s: matches forbidden-feature pattern %q (ADR-0046 / criteria SEC-8). Forbidden at P0: password reset, self-service signup, OAuth providers, JWKS rotation.\n",
			m.Path, m.Pattern)
	}
	fmt.Fprintf(os.Stderr, "forbidden-features-check: %d violation(s); failing.\n", len(hits))
	os.Exit(1)
}
