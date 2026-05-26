package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestForbiddenFeatures_CleanFixture(t *testing.T) {
	dir, err := filepath.Abs("testdata/clean")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command("go", "run", ".", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("clean fixture should pass; exit err=%v output=%s", err, out)
	}
	if !strings.Contains(string(out), "no forbidden-feature files") {
		t.Fatalf("expected success message; got %s", out)
	}
}

func TestForbiddenFeatures_ViolatingFixture(t *testing.T) {
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
	// The fixture file is named password_reset_handler.go and oauth_google.go;
	// both should be diagnosed.
	if !strings.Contains(s, "password") {
		t.Fatalf("expected 'password' in diagnostic; got %s", s)
	}
	if !strings.Contains(s, "oauth") {
		t.Fatalf("expected 'oauth' in diagnostic; got %s", s)
	}
}

func TestForbiddenFeatures_RealRepoRoot(t *testing.T) {
	// Sanity: the real repo MUST pass — no forbidden-feature files
	// shipped by T-008/T-015a/T-015b. If this fails on main, something
	// got past review.
	dir, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	cmd := exec.Command("go", "run", ".", dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real repo should pass; err=%v output=%s", err, out)
	}
}
