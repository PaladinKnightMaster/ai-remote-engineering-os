// Package cmdcrypt seals/opens the persisted invocation command at rest (F3,
// ADR-0059 extension) with AES-256-GCM under a gateway-local key — distinct from
// the connector's resource-secret key and the gateway's PASETO keys (authority
// separation). The command is potentially secret-bearing (it may carry inline
// credentials); this keeps it encrypted in the gateway DB, decrypted only on the
// operator-only read path. Application-level (not pgcrypto) so the key never leaves
// the gateway process and the SEC-13 grep gate (connector-only pgp_sym_decrypt)
// stays pristine.
//
// A nil *Cipher is the dev/transition fallback: Seal/Open pass the value through as
// plaintext bytes (the gateway boots without AIREOS_GATEWAY_COMMAND_KEY until it is
// provisioned). nil-receiver methods are intentional and safe.
package cmdcrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Cipher is an AES-256-GCM sealer for the command column.
type Cipher struct{ aead cipher.AEAD }

// NewFromHex builds a Cipher from a hex-encoded 32-byte (AES-256) key.
func NewFromHex(h string) (*Cipher, error) {
	k, err := hex.DecodeString(h)
	if err != nil {
		return nil, fmt.Errorf("decode command key hex: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("command key must be 32 bytes (AES-256), got %d", len(k))
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Seal returns nonce||ciphertext for plaintext. A nil Cipher returns the plaintext
// bytes unchanged (dev/transition fallback).
func (c *Cipher) Seal(plaintext string) ([]byte, error) {
	if c == nil {
		return []byte(plaintext), nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Open reverses Seal. A nil Cipher returns the bytes as a plaintext string.
func (c *Cipher) Open(b []byte) (string, error) {
	if c == nil {
		return string(b), nil
	}
	ns := c.aead.NonceSize()
	if len(b) < ns {
		return "", errors.New("cmdcrypt: ciphertext too short")
	}
	pt, err := c.aead.Open(nil, b[:ns], b[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("open command: %w", err)
	}
	return string(pt), nil
}
