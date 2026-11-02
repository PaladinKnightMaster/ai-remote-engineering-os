package setup

import (
	"regexp"
	"strings"
	"testing"
)

var codeShape = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{5}(-[0-9A-HJKMNP-TV-Z]{5}){3}$`)

// The code is 20 Crockford base32 characters in four groups of five, drawn from
// crypto/rand (ADR-0078 §D1): no I, L, O, U, and no two codes alike.
func TestGenerateCodeShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		if !codeShape.MatchString(c) {
			t.Fatalf("code %q does not match XXXXX-XXXXX-XXXXX-XXXXX over the Crockford alphabet", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q in 200 draws", c)
		}
		seen[c] = true
	}
}

// Typing is forgiving: case, spaces, dashes and Crockford's O/I/L aliases all normalise
// to the same code, so the hash matches however the operator reads it off the terminal.
func TestNormalize(t *testing.T) {
	want := "01ABCDEFGHJKMNPQRSTV"
	for _, in := range []string{
		"01ABC-DEFGH-JKMNP-QRSTV",
		"o1abc defgh jkmnp qrstv",
		"OIABC-DEFGH-JKMNP-QRSTV", // O -> 0, I -> 1
		"0lABC-DEFGH-JKMNP-QRSTV", // L -> 1
		"  01abcdefghjkmnpqrstv  ",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if hashCode("01ABC-DEFGH-JKMNP-QRSTV") != hashCode("o1abc defgh jkmnp qrstv") {
		t.Error("equivalent spellings must hash alike")
	}
	if hashCode("01ABC-DEFGH-JKMNP-QRSTV") == hashCode("01ABC-DEFGH-JKMNP-QRSTW") {
		t.Error("different codes must hash differently")
	}
}

func TestValidateCredentials(t *testing.T) {
	good := strings.Repeat("p", MinPasswordLen)
	for _, u := range []string{"admin", "ops.team", "a@b.co", "x-y_z"} {
		if err := ValidateCredentials(u, good); err != nil {
			t.Errorf("username %q refused: %v", u, err)
		}
	}
	// '#' and ':' would corrupt "operator:<u>" and the "#paired" audit suffix.
	for _, u := range []string{"", "ab", "-lead", "bad#name", "bad:name", "has space", strings.Repeat("a", 65)} {
		if err := ValidateCredentials(u, good); err == nil {
			t.Errorf("username %q accepted", u)
		}
	}
	if err := ValidateCredentials("admin", good[:MinPasswordLen-1]); err == nil {
		t.Error("password one character short accepted")
	}
	if err := ValidateCredentials("admin", strings.Repeat("p", maxPasswordLen+1)); err == nil {
		t.Error("oversized password accepted")
	}
}
