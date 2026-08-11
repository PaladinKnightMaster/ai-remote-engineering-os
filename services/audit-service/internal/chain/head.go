package chain

// head.go adds the Ed25519 chain-head signing primitive (ADR-0069). A chain
// head is a checkpoint over the tamper-evidence chain: because chainHash_N
// commits to the entire chain prefix (Compute above chains prevHash
// through), a single signature over head N attests every event with
// seq <= N (D1) — letting a third party with only an export and a public
// key verify the record offline, with no database access.
//
// Deliberately crypto/ed25519 from the stdlib, not packages/go/auth: that
// package wraps Ed25519 inside PASETO v4 asymmetric tokens (unexported
// field, mints tokens) — a chain head is a raw signature over raw framed
// bytes, not a token. Key material still comes from the same place
// operationally (`authctl genkeypair`, ADR-0060): a PASETO v4 asymmetric
// keypair IS a standard Ed25519 keypair, so the hex this file parses is
// byte-for-byte what that command already prints.
import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
)

// HeadDomain is the domain separator mixed into every signed head payload. A
// signed head must never be mistakable for, or replayable as, any other
// signed artifact in the system (a PASETO capability token, or the release
// bundle signature of ADR-0070).
const HeadDomain = "aireos-audit-chain-head-v1"

// Head is one signed checkpoint over the chain (ADR-0069 D1).
type Head struct {
	// Seq is the chain position checkpointed. NOT hashed into chain_hash
	// itself (Compute's field set is fixed and excludes it), but IS bound
	// into the signed payload here — a renumbering attack produces a head
	// whose Seq no longer matches the chain it commits to, reported by a
	// verifier as divergence.
	Seq int64
	// ChainHash is the chain_hash value AT Seq — the prefix commitment this
	// head attests.
	ChainHash string
	// Timestamp is the signer-produced occurrence time (RFC3339Nano), signed
	// and stored verbatim — same "TEXT, no normalization" discipline as
	// audit_events.timestamp, because this value is part of the signed
	// payload and must be byte-identical on every read.
	Timestamp string
	// KeyID identifies the signing key: first 16 lowercase hex chars of
	// SHA-256(pubkey) — see KeyIDFor.
	KeyID string
	// Signature is lowercase hex of the 64-byte Ed25519 signature (128 hex
	// chars) over headPayload's framed bytes.
	Signature string
}

// Typed verification failures (D6): a caller needs to distinguish "this key
// ring doesn't recognize the keyId" (rotation/operator question) from "the
// bytes don't check out under a key we do recognize" (a tamper finding).
var (
	ErrUnknownKeyID         = errors.New("chain: head key id does not match the verifying public key")
	ErrInvalidHeadSignature = errors.New("chain: head signature does not verify")
)

// KeyIDFor derives the fingerprint used as Head.KeyID: the first 16
// lowercase hex characters of SHA-256(pub). It is a pure function of the
// public key, so a verifier holding only the public key can derive it
// independently — no key registry is needed (ADR-0069 D5).
func KeyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

// headPayload builds the exact bytes that are signed/verified:
//
//	framed(HeadDomain) || framed(decimal(seq)) || framed(chainHash) ||
//	framed(timestamp) || framed(keyId)
//
// Framing uses the SAME length-prefix idiom as Compute above (uint32
// big-endian length + bytes) — one framing convention in this codebase,
// never two. EVERY element is framed here, including the domain separator:
// Compute writes its prevHash unframed, but that is safe there ONLY because
// prevHash is fixed-width (64 hex chars always) — no boundary can shift. A
// head's fields are not all fixed-width (keyId and timestamp aren't), so
// headPayload frames uniformly rather than relying on any one field's
// current width.
func headPayload(seq int64, chainHash, timestamp, keyID string) []byte {
	var buf bytes.Buffer
	frame(&buf, []byte(HeadDomain))
	frame(&buf, []byte(strconv.FormatInt(seq, 10)))
	frame(&buf, []byte(chainHash))
	frame(&buf, []byte(timestamp))
	frame(&buf, []byte(keyID))
	return buf.Bytes()
}

func frame(buf *bytes.Buffer, b []byte) {
	var lp [4]byte
	binary.BigEndian.PutUint32(lp[:], uint32(len(b)))
	buf.Write(lp[:])
	buf.Write(b)
}

// SignHead produces a Head signed under sk over (seq, chainHash, timestamp).
// KeyID is derived from sk's public half (KeyIDFor), never passed in — a
// caller cannot mint a head under a keyId that doesn't match the signing key.
func SignHead(sk ed25519.PrivateKey, seq int64, chainHash, timestamp string) (Head, error) {
	if len(sk) != ed25519.PrivateKeySize {
		return Head{}, fmt.Errorf("chain: sign head: invalid ed25519 private key size %d (want %d)", len(sk), ed25519.PrivateKeySize)
	}
	pub, ok := sk.Public().(ed25519.PublicKey)
	if !ok {
		return Head{}, errors.New("chain: sign head: private key has no ed25519 public half")
	}
	keyID := KeyIDFor(pub)
	sig := ed25519.Sign(sk, headPayload(seq, chainHash, timestamp, keyID))
	return Head{
		Seq:       seq,
		ChainHash: chainHash,
		Timestamp: timestamp,
		KeyID:     keyID,
		Signature: hex.EncodeToString(sig),
	}, nil
}

// VerifyHead checks h against pub, per ADR-0069 D6's two-step proof (this is
// step 1 only — step 2, recomputing the chain from Genesis, is chain.Verify's
// job and must be run alongside this by the caller). Returns
// ErrUnknownKeyID when h.KeyID doesn't match pub, or ErrInvalidHeadSignature
// when the signature itself fails — see the package doc above for why that
// distinction matters to a caller.
func VerifyHead(pub ed25519.PublicKey, h Head) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("chain: verify head: invalid ed25519 public key size %d (want %d)", len(pub), ed25519.PublicKeySize)
	}
	if h.KeyID != KeyIDFor(pub) {
		return ErrUnknownKeyID
	}
	sig, err := hex.DecodeString(h.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrInvalidHeadSignature
	}
	if !ed25519.Verify(pub, headPayload(h.Seq, h.ChainHash, h.Timestamp, h.KeyID), sig) {
		return ErrInvalidHeadSignature
	}
	return nil
}

// SecretKeyFromHex parses a hex-encoded Ed25519 secret key — the exact
// format `authctl genkeypair` prints as its first field (a PASETO v4
// asymmetric secret key IS a standard 64-byte Ed25519 private key). Rejects
// anything the wrong length with a clear error rather than panicking later
// inside ed25519.Sign.
func SecretKeyFromHex(s string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("chain: decode secret key hex: %w", err)
	}
	if len(b) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("chain: secret key must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PrivateKeySize, ed25519.PrivateKeySize*2, len(b))
	}
	return ed25519.PrivateKey(b), nil
}

// PublicKeyFromHex parses a hex-encoded Ed25519 public key — the exact
// format `authctl genkeypair` prints as its second field.
func PublicKeyFromHex(s string) (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("chain: decode public key hex: %w", err)
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("chain: public key must be %d bytes (%d hex chars), got %d bytes",
			ed25519.PublicKeySize, ed25519.PublicKeySize*2, len(b))
	}
	return ed25519.PublicKey(b), nil
}
