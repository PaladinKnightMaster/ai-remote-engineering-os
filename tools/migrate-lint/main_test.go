package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateLint exercises the binary contract: exit 0 on clean,
// non-zero on violations. We invoke the tool via `go run .` so the
// test runs against the real `main` rather than a refactored helper —
// the exit code is the contract.

func TestMigrateLint_CleanFixture(t *testing.T) {
	dir, err := filepath.Abs("testdata/clean")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command("go", "run", ".", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clean fixture should pass; exit err=%v output=%s", err, out)
	}
	if !strings.Contains(string(out), "no destructive DDL") {
		t.Fatalf("expected success message; got %s", out)
	}
}

func TestMigrateLint_ViolatingFixture(t *testing.T) {
	dir, err := filepath.Abs("testdata/violating")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command("go", "run", ".", dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("violating fixture should fail; got success output=%s", out)
	}
	s := string(out)
	if !strings.Contains(s, "DROP") && !strings.Contains(s, "TRUNCATE") {
		t.Fatalf("expected destructive-DDL diagnostic in stderr; got %s", s)
	}
	if !strings.Contains(s, "audit_events") {
		t.Fatalf("expected the protected table name in the diagnostic; got %s", s)
	}
}

func TestMigrateLint_RealRepoMigrations(t *testing.T) {
	// Sanity: the real audit-service migrations directory MUST pass.
	// If it doesn't, T-004b's migration shipped destructive DDL — which
	// is the load-bearing thing this lint exists to prevent.
	dir, err := filepath.Abs("../../services/audit-service/migrations")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command("go", "run", ".", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real audit migrations should pass; err=%v output=%s", err, out)
	}
}
