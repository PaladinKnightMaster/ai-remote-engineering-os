package account

import (
	"testing"

	"aireos.local/packages/go/auth"
)

func TestAuthenticate(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	op := New("admin", hash)

	if sub, ok := op.Authenticate("admin", "hunter2"); !ok || sub != "operator:admin" {
		t.Fatalf("valid creds should authenticate, got sub=%q ok=%v", sub, ok)
	}
	if _, ok := op.Authenticate("admin", "wrong"); ok {
		t.Fatal("wrong password must fail")
	}
	if _, ok := op.Authenticate("intruder", "hunter2"); ok {
		t.Fatal("wrong username must fail")
	}
}

// A Holder authenticates nobody until an operator is Set, then that operator, in-process.
func TestHolderAuthenticatesOnlyAfterSet(t *testing.T) {
	hash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	h := NewHolder(nil)
	if _, ok := h.Authenticate("admin", "hunter2"); ok {
		t.Fatal("an empty Holder authenticated someone")
	}
	h.Set(New("admin", hash))
	if sub, ok := h.Authenticate("admin", "hunter2"); !ok || sub != "operator:admin" {
		t.Fatalf("after Set: sub=%q ok=%v", sub, ok)
	}
	if _, ok := h.Authenticate("admin", "wrong"); ok {
		t.Fatal("wrong password authenticated through the Holder")
	}
}
