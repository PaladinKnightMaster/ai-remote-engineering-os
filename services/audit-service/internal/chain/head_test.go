package chain

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
)

// goldenKeyLabel names the fixed, NON-SECRET input the golden-vector key is
// derived from (T-168). The Ed25519 seed is SHA-256 of this label, computed in
// code — no key literal is committed anywhere in this package, so secret
// scanners have nothing to flag. The key is public by construction: anyone can
// recompute it from this string, and it signs nothing but test vectors.
const goldenKeyLabel = "aireos/audit-service/chain/head_test.go golden-vector key v1 (public, non-secret)"

// goldenKey derives the golden-vector keypair from goldenKeyLabel.
func goldenKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(goldenKeyLabel))
	return ed25519.NewKeyFromSeed(seed[:])
}

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
//
// T-168: the pair is no longer a committed literal. It is produced here by
// auth.GenerateAsymmetricKey — the very function `authctl genkeypair` calls and
// prints (packages/go/auth/cmd/authctl/main.go) — so the seam is exercised
// against the real producer on every run rather than against a snapshot of its
// output, and no secret-shaped string lives in the repo. The layout assertion
// (secret hex = 32-byte seed || 32-byte public, i.e. its trailing 64 hex chars
// equal the public hex) is kept: it is what proves the two encodings agree.
func TestAuthctlKeyFormatCompatibility(t *testing.T) {
	_, _, authctlSecretHex, authctlPublicHex := auth.GenerateAsymmetricKey()

	sk, err := SecretKeyFromHex(authctlSecretHex)
	if err != nil {
		t.Fatalf("parse authctl secret key: %v", err)
	}
	pub, err := PublicKeyFromHex(authctlPublicHex)
	if err != nil {
		t.Fatalf("parse authctl public key: %v", err)
	}
	if len(sk) != ed25519.PrivateKeySize {
		t.Fatalf("secret key wrong size: %d", len(sk))
	}
	if len(pub) != ed25519.PublicKeySize {
		t.Fatalf("public key wrong size: %d", len(pub))
	}
	if !strings.HasSuffix(authctlSecretHex, authctlPublicHex) {
		t.Fatal("authctl secret hex is not the raw Ed25519 layout (seed || public): its tail != the public hex")
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
	if _, err := SecretKeyFromHex(authctlPublicHex); err == nil {
		t.Fatal("expected error for a public-length key passed as secret")
	}
	if _, err := PublicKeyFromHex("zz"); err == nil {
		t.Fatal("expected error for malformed public hex")
	}
	if _, err := PublicKeyFromHex(authctlSecretHex); err == nil {
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
// SignHead(goldenKey(), seq=42, a fixed chainHash, a fixed RFC3339Nano
// timestamp) and hard-coded below as a literal, independent of whatever
// headPayload does today. A signature is a public value, not a secret.
//
// RE-PINNED at T-168 (and only then): the golden key used to be a committed
// hex literal, which secret scanners (GitGuardian) flagged on the public repo.
// It is now derived in code from goldenKeyLabel, so the signing key — and
// therefore this signature — changed while the signed-head FORMAT did not
// (the unchanged TestDomainSeparationRequired / TestTamperEachFieldDetected
// / TestFieldShiftResistance pin that). From here on the rule below is
// unchanged: a failure means the format moved.
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
		goldenSignature = "3964a3f334c494d3556129266325fb0bb31e1e67df6a9110a9906816f948b2b2" +
			"2687f07cf0be7cc52ef7d820470e0f9c1c17a8d43c57f7b450ed6c372558f904"
	)

	sk := goldenKey()
	h, err := SignHead(sk, goldenSeq, goldenChainHash, goldenTimestamp)
	if err != nil {
		t.Fatalf("sign golden vector: %v", err)
	}
	if h.Signature != goldenSignature {
		t.Fatalf("signed-head format has changed — got %s, want %s (see this test's doc comment: "+
			"bump HeadDomain to -v2, do not update this expected value)", h.Signature, goldenSignature)
	}

	pub, ok := sk.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("golden key has no ed25519 public half")
	}
	if err := VerifyHead(pub, h); err != nil {
		t.Fatalf("golden vector must verify: %v", err)
	}
}
