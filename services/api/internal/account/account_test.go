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
