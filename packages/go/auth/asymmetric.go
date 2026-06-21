package auth

// asymmetric.go adds the PASETO v4 *public* (Ed25519) primitives (ADR-0060). They
// mirror the symmetric Signer/Verifier API and share buildToken/newParser/
// claimsFromToken (auth.go), so callers are uniform and the two token paths cannot
// drift. Unlike v4 local (symmetric, where verify == mint), v4 public splits the
// signing secret key (held only by the minting workload) from the verifying public
// key — so a workload that only verifies cannot mint. This is what lets the
// agent-facing mcp-gateway drop the symmetric cluster key (v0.4.0). T-045 ships the
// primitives only; the dual-mode verifier (T-046) and the service wiring (T-047+)
// are later rungs.

import (
	"context"
	"fmt"

	"aidanwoods.dev/go-paseto"
)

// AsymmetricSecretKey is a PASETO v4 public (Ed25519) signing key — held only by
// the minting workload.
type AsymmetricSecretKey struct{ k paseto.V4AsymmetricSecretKey }

// AsymmetricPublicKey is the verification half of an AsymmetricSecretKey,
// distributed to the services that verify (but must not be able to mint).
type AsymmetricPublicKey struct{ k paseto.V4AsymmetricPublicKey }

// GenerateAsymmetricKey returns a fresh Ed25519 key pair and the hex encodings of
// each half (secret → the signer workload's env; public → each verifier's env),
// for install.sh / operator provisioning (OI-11).
func GenerateAsymmetricKey() (AsymmetricSecretKey, AsymmetricPublicKey, string, string) {
	sk := paseto.NewV4AsymmetricSecretKey()
	pk := sk.Public()
	return AsymmetricSecretKey{sk}, AsymmetricPublicKey{pk}, sk.ExportHex(), pk.ExportHex()
}

// NewAsymmetricSecretKeyFromHex parses a hex-encoded v4 secret key (the signer
// workload's key env).
func NewAsymmetricSecretKeyFromHex(h string) (AsymmetricSecretKey, error) {
	k, err := paseto.NewV4AsymmetricSecretKeyFromHex(h)
	if err != nil {
		return AsymmetricSecretKey{}, fmt.Errorf("load asymmetric secret key: %w", err)
	}
	return AsymmetricSecretKey{k}, nil
}

// NewAsymmetricPublicKeyFromHex parses a hex-encoded v4 public key (the verifier
// half distributed to gateway-touching services).
func NewAsymmetricPublicKeyFromHex(h string) (AsymmetricPublicKey, error) {
	k, err := paseto.NewV4AsymmetricPublicKeyFromHex(h)
	if err != nil {
		return AsymmetricPublicKey{}, fmt.Errorf("load asymmetric public key: %w", err)
	}
	return AsymmetricPublicKey{k}, nil
}

// AsymmetricSigner mints v4 public (signed, not encrypted) capability tokens under
// a secret key for a given issuer. The claim set is identical to the symmetric
// Signer's (shared buildToken); only the wrapping differs (V4Sign vs V4Encrypt).
// Used by the workloads that sign onto the gateway-touching edges (ADR-0060):
// services/api (the agent-plane signer) and services/mcp-gateway (its outbound
// service signer).
type AsymmetricSigner struct {
	key    AsymmetricSecretKey
	issuer string
}

func NewAsymmetricSigner(key AsymmetricSecretKey, issuer string) *AsymmetricSigner {
	return &AsymmetricSigner{key: key, issuer: issuer}
}

// Issue mints a v4 public token for params; verified by an AsymmetricVerifier
// holding the matching public key.
func (s *AsymmetricSigner) Issue(p IssueParams) (string, error) {
	t, err := buildToken(s.issuer, p)
	if err != nil {
		return "", err
	}
	return t.V4Sign(s.key.k, nil), nil
}

// IssueService mints a service-to-service token (edge service-service, 1h TTL) —
// the asymmetric analog of Signer.IssueService (e.g. mcp-gateway → policy).
func (s *AsymmetricSigner) IssueService(subject, audience string) (string, error) {
	return s.Issue(IssueParams{Subject: subject, Edge: "service-service", Audience: audience, TTL: ServiceTTL})
}

// IssueAgent mints a scoped agent capability token (edge agent-tool, 5min TTL) —
// the asymmetric analog of Signer.IssueAgent (services/api → an agent, verified
// by the gateway under api's public key).
func (s *AsymmetricSigner) IssueAgent(subject, audience string, scope Scope) (string, error) {
	return s.Issue(IssueParams{Subject: subject, Edge: "agent-tool", Audience: audience, Scope: scope, TTL: AgentTTL})
}

// AsymmetricVerifier verifies v4 public tokens under a public key, enforcing expiry
// and (optionally) issuer + audience — the verify-only half held by a service that
// must NOT be able to mint (ADR-0060). Returns the same typed CapabilityToken as
// the symmetric Verifier (shared claimsFromToken).
type AsymmetricVerifier struct {
	key      AsymmetricPublicKey
	issuer   string
	audience string
}

// NewAsymmetricVerifier builds a verifier. issuer/audience may be empty to skip
// those rules; a service should set audience to its own name so a token minted for
// a different service is rejected.
func NewAsymmetricVerifier(key AsymmetricPublicKey, issuer, audience string) *AsymmetricVerifier {
	return &AsymmetricVerifier{key: key, issuer: issuer, audience: audience}
}

// Verify validates the bearer token's signature + rules, returning typed claims.
// A v4 local (symmetric) token is rejected here — that token-type mismatch is the
// isolation the dual-mode verifier (T-046) dispatches on.
func (v *AsymmetricVerifier) Verify(_ context.Context, bearer string) (CapabilityToken, error) {
	parser := newParser(v.issuer, v.audience)
	t, err := parser.ParseV4Public(v.key.k, bearer, nil)
	if err != nil {
		return CapabilityToken{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return claimsFromToken(t)
}
