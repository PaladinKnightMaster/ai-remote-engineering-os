package main

import (
	"context"
	"testing"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/account"
)

// ADR-0078 §D4: the environment operator ALWAYS wins. With it set, buildAuth returns it,
// returns NO setup option (so setup mode is never entered), and never reads the operators
// table -- the nil pool would panic on the first query if it did.
func TestBuildAuthEnvOperatorWins(t *testing.T) {
	_, keyHex := auth.GenerateKey()
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIREOS_PASETO_KEY", keyHex)
	t.Setenv("AIREOS_BOOTSTRAP_OPERATOR_USER", "admin")
	t.Setenv("AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH", hash)

	_, _, authn, setupOpts, err := buildAuth(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(setupOpts) != 0 {
		t.Fatalf("an env operator still produced %d setup option(s)", len(setupOpts))
	}
	if _, isHolder := authn.(*account.Holder); isHolder {
		t.Fatal("login is wired to the setup holder, not the env operator")
	}
	if sub, ok := authn.Authenticate("admin", "hunter2"); !ok || sub != "operator:admin" {
		t.Fatalf("env operator does not authenticate: %q %v", sub, ok)
	}
}
