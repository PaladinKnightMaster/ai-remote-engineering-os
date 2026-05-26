// migrate-lint — T-011. Enforces the ADR-0044 forward-only / no-destructive-DDL
// rule on the audit-service migrations.
//
// The audit_events table is the chained-hash evidence chain (ADR-0045).
// Any DDL that breaks chain continuity is a release-blocker. This tool
// reads every *.sql in the target directory and flags statements that
// match the destructive-DDL patterns against the protected table set.
//
// Out of scope at v0.1.0: full SQL parsing. Pattern matching is sufficient
// because golang-migrate migrations are operator-authored, not generated;
// adversarial-input concerns don't apply. A future Atlas adoption per
// ADR-0044 §Reserve could replace this tool with a structured-AST check.
//
// Usage:
//   migrate-lint <dir>
//
// Exit 0 = clean, exit 1 = violations (printed to stderr). The exit
// code is the load-bearing contract — CI reads it.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Tables whose DDL must be append-only per ADR-0045 (the chained-hash
// evidence chain). Extend this list when new chain-bearing tables land.
var protectedTables = []string{
	"audit_events",
}

// Destructive-DDL patterns. The space-handling is intentional: SQL is
// case-insensitive and whitespace-tolerant; (?i) handles case,
// `\s+` handles inter-token whitespace and newlines.
var destructivePatterns = func() []*regexp.Regexp {
	var ps []*regexp.Regexp
	for _, t := range protectedTables {
		// DROP TABLE [IF EXISTS] <table>
		ps = append(ps, regexp.MustCompile(`(?i)\bDROP\s+TABLE\b(\s+IF\s+EXISTS)?\s+`+regexp.QuoteMeta(t)+`\b`))
		// TRUNCATE [TABLE] <table>
		ps = append(ps, regexp.MustCompile(`(?i)\bTRUNCATE\b(\s+TABLE)?\s+`+regexp.QuoteMeta(t)+`\b`))
		// ALTER TABLE <table> ... DROP COLUMN ...
		ps = append(ps, regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+`+regexp.QuoteMeta(t)+`\b[\s\S]*?\bDROP\s+(COLUMN|CONSTRAINT|TRIGGER)\b`))
		// DELETE FROM <table>
		ps = append(ps, regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+`+regexp.QuoteMeta(t)+`\b`))
		// UPDATE <table> SET ...
		ps = append(ps, regexp.MustCompile(`(?i)\bUPDATE\s+`+regexp.QuoteMeta(t)+`\b[\s\S]*?\bSET\b`))
	}
	return ps
}()

// Comment-stripping is approximate but sufficient: SQL line comments
// (-- ...) and block comments (/* ... */). A statement legitimately
// inside a comment doesn't count as a violation.
var (
	reLineComment  = regexp.MustCompile(`(?m)--[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

type violation struct {
	File    string
	Line    int
	Snippet string
	Rule    string
}

func stripComments(s string) string {
	s = reBlockComment.ReplaceAllString(s, "")
	s = reLineComment.ReplaceAllString(s, "")
	return s
}

func lintFile(path string) ([]violation, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	stripped := stripComments(string(raw))
	var vs []violation
	for _, p := range destructivePatterns {
		for _, m := range p.FindAllStringIndex(stripped, -1) {
			// Approximate the line number by counting newlines in the
			// stripped source up to the match. Stripping removes
			// comments but leaves line breaks, so this is close enough
			// for operator messages.
			line := strings.Count(stripped[:m[0]], "\n") + 1
			vs = append(vs, violation{
				File:    path,
				Line:    line,
				Snippet: strings.TrimSpace(stripped[m[0]:m[1]]),
				Rule:    p.String(),
			})
		}
	}
	return vs, nil
}

func lintDir(dir string) ([]violation, error) {
	var files []string
	if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(p), ".sql") {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}
	sort.Strings(files)
	var all []violation
	for _, f := range files {
		vs, err := lintFile(f)
		if err != nil {
			return nil, err
		}
		all = append(all, vs...)
	}
	return all, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: migrate-lint <dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Fprintf(os.Stderr, "migrate-lint: %s is not a directory\n", dir)
		os.Exit(2)
	}
	vs, err := lintDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate-lint: %v\n", err)
		os.Exit(2)
	}
	if len(vs) == 0 {
		fmt.Printf("migrate-lint: %s: no destructive DDL on protected tables (ADR-0044).\n", dir)
		return
	}
	for _, v := range vs {
		fmt.Fprintf(os.Stderr, "migrate-lint: %s:%d: forbidden DDL %q (ADR-0044 forward-only / ADR-0045 chain integrity)\n",
			v.File, v.Line, v.Snippet)
	}
	fmt.Fprintf(os.Stderr, "migrate-lint: %d violation(s); failing.\n", len(vs))
	os.Exit(1)
}
