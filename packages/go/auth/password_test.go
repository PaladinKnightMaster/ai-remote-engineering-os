package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$") {
		t.Fatalf("unexpected PHC prefix: %s", h)
	}
	if err := VerifyPassword("correct horse battery staple", h); err != nil {
		t.Fatalf("verify should succeed: %v", err)
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	h, _ := HashPassword("s3cret")
	if err := VerifyPassword("guess", h); !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("want ErrPasswordMismatch, got %v", err)
	}
}

func TestHashIsSalted(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password must differ (random salt)")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "plain", "$argon2id$v=19$bad", "$bcrypt$x$y$z$w$v"} {
		if err := VerifyPassword("x", bad); err == nil {
			t.Fatalf("expected error for malformed hash %q", bad)
		}
	}
}
