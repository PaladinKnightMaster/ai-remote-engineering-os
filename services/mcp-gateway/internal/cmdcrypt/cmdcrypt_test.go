package cmdcrypt

import (
	"strings"
	"testing"
)

const testKeyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 32 bytes

func TestSealOpenRoundTrip(t *testing.T) {
	c, err := NewFromHex(testKeyHex)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	const pt = "echo SECRET-42 && rm -rf /tmp/x"
	sealed, err := c.Seal(pt)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(sealed) == pt {
		t.Fatal("sealed bytes equal plaintext — not encrypted")
	}
	got, err := c.Open(sealed)
	if err != nil || got != pt {
		t.Fatalf("open = %q err=%v, want %q", got, err, pt)
	}
}

func TestNonceMakesCiphertextUnique(t *testing.T) {
	c, _ := NewFromHex(testKeyHex)
	a, _ := c.Seal("same")
	b, _ := c.Seal("same")
	if string(a) == string(b) {
		t.Fatal("identical plaintext sealed to identical bytes — nonce not applied")
	}
}

func TestOpenWrongKeyFails(t *testing.T) {
	c1, _ := NewFromHex(testKeyHex)
	c2, _ := NewFromHex(strings.Repeat("ab", 32))
	sealed, _ := c1.Seal("secret")
	if _, err := c2.Open(sealed); err == nil {
		t.Fatal("open under the wrong key must fail (GCM auth)")
	}
}

func TestOpenTamperFails(t *testing.T) {
	c, _ := NewFromHex(testKeyHex)
	sealed, _ := c.Seal("secret")
	sealed[len(sealed)-1] ^= 0xff
	if _, err := c.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext must fail GCM auth")
	}
}

func TestNilCipherPassthrough(t *testing.T) {
	var c *Cipher // nil → plaintext fallback
	sealed, err := c.Seal("plain")
	if err != nil || string(sealed) != "plain" {
		t.Fatalf("nil Seal = %q err=%v, want plaintext passthrough", sealed, err)
	}
	got, err := c.Open([]byte("plain"))
	if err != nil || got != "plain" {
		t.Fatalf("nil Open = %q err=%v, want plaintext passthrough", got, err)
	}
}

func TestBadKey(t *testing.T) {
	if _, err := NewFromHex("zzz"); err == nil {
		t.Fatal("non-hex key must error")
	}
	if _, err := NewFromHex("abcd"); err == nil {
		t.Fatal("wrong-length key must error")
	}
}
