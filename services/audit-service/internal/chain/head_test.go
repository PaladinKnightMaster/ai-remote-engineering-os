package chain

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"
)

// authctlFixtureSecretHex / authctlFixturePublicHex were produced live by
// `go run ./packages/go/auth/cmd/authctl genkeypair` (ADR-0060's existing
// keypair primitive) and hard-coded here to prove — not assume —
// SecretKeyFromHex/PublicKeyFromHex accept the exact hex format that command
// prints. Confirmed by inspection: the secret hex's trailing 64 hex chars
// equal the public hex exactly, i.e. this is the standard raw Ed25519
// private-key layout (32-byte seed || 32-byte public), which is exactly what
// crypto/ed25519.PrivateKey expects — the installer seam this ticket must not
// silently break.
const (
	authctlFixtureSecretHex = "6d21c02dfd9843625699dc4c6aefc8e69fd42692cf787fc6cf9df1135c83326" +
		"a1752d095ae7fa02fae08cde0a2f7b41b959dd27dce12bf0b6461cbd0e20a818b"
	authctlFixturePublicHex = "1752d095ae7fa02fae08cde0a2f7b41b959dd27dce12bf0b6461cbd0e20a818b"
)

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	return pub, sk
}

// Signing the same inputs twice produces the same signature (SignHead has no
// hidden entropy or clock dependency beyond the timestamp the caller passes
// in), and it verifies.
func TestSignHeadDeterministic(t *testing.T) {
	pub, sk := testKeypair(t)

	h1, err := SignHead(sk, 42, "ab", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign 1: %v", err)
	}
	h2, err := SignHead(sk, 42, "ab", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign 2: %v", err)
	}
	if h1.Signature != h2.Signature {
		t.Fatalf("SignHead not deterministic: %s != %s", h1.Signature, h2.Signature)
	}
	if len(h1.Signature) != ed25519.SignatureSize*2 {
		t.Fatalf("expected %d hex chars, got %d", ed25519.SignatureSize*2, len(h1.Signature))
	}
	if err := VerifyHead(pub, h1); err != nil {
		t.Fatalf("expected valid head to verify, got %v", err)
	}
}

// A payload signed WITHOUT the domain separator must not verify against a
// head claiming HeadDomain — the mandatory domain separation ADR-0069 D1
// requires (a signed head must never be mistakable for another artifact's
// signature).
func TestDomainSeparationRequired(t *testing.T) {
	pub, sk := testKeypair(t)

	// Hand-build a payload that omits the domain separator entirely (mirrors
	// headPayload's framing but starts at seq, never writing HeadDomain),
	// sign it, and smuggle that signature into an otherwise-valid Head. If
	// domain framing weren't load-bearing, this would still verify.
	frame := func(buf []byte, b []byte) []byte {
		var lp [4]byte
		binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
		buf = append(buf, lp[:]...)
		return append(buf, b...)
	}
	keyID := KeyIDFor(pub)
	var noDomain []byte
	noDomain = frame(noDomain, []byte("42"))
	noDomain = frame(noDomain, []byte("aa"))
	noDomain = frame(noDomain, []byte("2026-08-11T00:00:00.000000000Z"))
	noDomain = frame(noDomain, []byte(keyID))

	sig := ed25519.Sign(sk, noDomain)
	forged := Head{
		Seq:       42,
		ChainHash: "aa",
		Timestamp: "2026-08-11T00:00:00.000000000Z",
		KeyID:     keyID,
		Signature: hex.EncodeToString(sig),
	}
	if err := VerifyHead(pub, forged); !errors.Is(err, ErrInvalidHeadSignature) {
		t.Fatalf("expected ErrInvalidHeadSignature for a domain-less signature, got %v", err)
	}
}

// Field-shift resistance: (seq=1, chainHash="ab") and (seq=11, chainHash="b")
// concatenate to the same raw string but must frame — and therefore sign —
// differently, exactly like Compute's own concatenation-injection guard.
func TestFieldShiftResistance(t *testing.T) {
	_, sk := testKeypair(t)

	h1, err := SignHead(sk, 1, "ab", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign h1: %v", err)
	}
	h2, err := SignHead(sk, 11, "b", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign h2: %v", err)
	}
	if h1.Signature == h2.Signature {
		t.Fatal("field-shifted seq/chainHash pair produced the same signature — framing is not boundary-safe")
	}
}

// Tampering with any one field independently must fail verification.
func TestTamperEachFieldDetected(t *testing.T) {
	pub, sk := testKeypair(t)
	base, err := SignHead(sk, 7, "deadbeef", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyHead(pub, base); err != nil {
		t.Fatalf("baseline head must verify, got %v", err)
	}

	cases := []struct {
		name string
		mut  func(h Head) Head
	}{
		{"seq", func(h Head) Head { h.Seq++; return h }},
		{"chainHash", func(h Head) Head { h.ChainHash = "cafebabe"; return h }},
		{"timestamp", func(h Head) Head { h.Timestamp = "2026-08-11T00:00:00.000000001Z"; return h }},
		{"keyId", func(h Head) Head { h.KeyID = "0000000000000000"; return h }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tampered := tc.mut(base)
			if err := VerifyHead(pub, tampered); err == nil {
				t.Fatalf("expected verification failure after mutating %s", tc.name)
			}
		})
	}
}

// A head verified against a public key it wasn't signed for fails, and fails
// with ErrUnknownKeyID (the keyId won't match a different key's fingerprint).
func TestWrongPublicKeyRejected(t *testing.T) {
	_, skA := testKeypair(t)
	pubB, _ := testKeypair(t)

	h, err := SignHead(skA, 3, "aabbcc", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := VerifyHead(pubB, h); !errors.Is(err, ErrUnknownKeyID) {
		t.Fatalf("expected ErrUnknownKeyID for a mismatched public key, got %v", err)
	}
}

// KEY-FORMAT COMPATIBILITY: the installer seam. A keypair in the exact hex
// format `authctl genkeypair` emits must parse via SecretKeyFromHex /
// PublicKeyFromHex and round-trip a sign/verify. Nothing else in this ticket
// checks that the two independently-written encodings (go-paseto's
// ExportHex, this file's raw ed25519 parsing) actually agree.
func TestAuthctlKeyFormatCompatibility(t *testing.T) {
	sk, err := SecretKeyFromHex(authctlFixtureSecretHex)
	if err != nil {
		t.Fatalf("parse authctl secret key: %v", err)
	}
	pub, err := PublicKeyFromHex(authctlFixturePublicHex)
	if err != nil {
		t.Fatalf("parse authctl public key: %v", err)
	}
	if len(sk) != ed25519.PrivateKeySize {
		t.Fatalf("secret key wrong size: %d", len(sk))
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("public key wrong size: %d", len(pub))
	}

	h, err := SignHead(sk, 100, "0123456789abcdef", "2026-08-11T00:00:00.000000000Z")
	if err != nil {
		t.Fatalf("sign with authctl-format key: %v", err)
	}
	if err := VerifyHead(pub, h); err != nil {
		t.Fatalf("verify with authctl-format public key: %v", err)
	}

	// Malformed hex / wrong length must fail loud, never panic.
	if _, err := SecretKeyFromHex("zz"); err == nil {
		t.Fatal("expected error for malformed secret hex")
	}
	if _, err := SecretKeyFromHex(authctlFixturePublicHex); err == nil {
		t.Fatal("expected error for a public-length key passed as secret")
	}
	if _, err := PublicKeyFromHex("zz"); err == nil {
		t.Fatal("expected error for malformed public hex")
	}
	if _, err := PublicKeyFromHex(authctlFixtureSecretHex); err == nil {
		t.Fatal("expected error for a secret-length key passed as public")
	}
}

// TestSignHeadGoldenVector pins the signed-head wire format against a fixed
// expected signature (Commander review of 93f3e39, FINDING 2). Every other
// test in this file computes its "expected" value with the same headPayload
// it is testing, so a change to field order, the domain separator string,
// decimal-vs-binary seq encoding, or a dropped frame would pass the whole
// suite while silently invalidating every head ever signed by a prior
// version — exactly the ambiguity ADR-0069 §Consequences names the `-v1`
// domain separator to avoid.
//
// goldenSignature was generated ONCE from this package's own
// SignHead(goldenSecretHex fixture, seq=42, a fixed chainHash, a fixed
// RFC3339Nano timestamp) and hard-coded below as a literal, independent of
// whatever headPayload does today.
//
// IF THIS TEST FAILS: the signed-head format has changed. That means every
// head signed by every prior version of this code is now unverifiable. The
// fix is NEVER to paste in a new expected value — it is to bump HeadDomain
// to a `-v2` string and ship a verifier that accepts both, per ADR-0069's
// stated compatibility posture.
func TestSignHeadGoldenVector(t *testing.T) {
	const (
		goldenSeq       = 42
		goldenChainHash = "5f2f3c9a6f1a8e6c2d9b7a4e1f3c8d6b5a2f0e9d7c4b1a8f6e3d0c9b7a5f2e1d"
		goldenTimestamp = "2026-08-11T00:00:00.000000000Z"
		goldenSignature = "268db37766e9ef53bbe235cdb503c6341f34757c9fad344cb7081a03b388d2" +
			"bbbfad0c9957860e9d412087abaadfc0b94358c0601fb7311a2fd7263d226c5f0b"
	)

	sk, err := SecretKeyFromHex(authctlFixtureSecretHex)
	if err != nil {
		t.Fatalf("parse fixture secret key: %v", err)
	}
	h, err := SignHead(sk, goldenSeq, goldenChainHash, goldenTimestamp)
	if err != nil {
		t.Fatalf("sign golden vector: %v", err)
	}
	if h.Signature != goldenSignature {
		t.Fatalf("signed-head format has changed — got %s, want %s (see this test's doc comment: "+
			"bump HeadDomain to -v2, do not update this expected value)", h.Signature, goldenSignature)
	}

	pub, err := PublicKeyFromHex(authctlFixturePublicHex)
	if err != nil {
		t.Fatalf("parse fixture public key: %v", err)
	}
	if err := VerifyHead(pub, h); err != nil {
		t.Fatalf("golden vector must verify: %v", err)
	}
}
