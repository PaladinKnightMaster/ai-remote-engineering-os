// Package auth is the in-process capability-token adapter (ADR-0046). It issues
// and verifies PASETO v4 local (symmetric) tokens whose claims are the typed
// CapabilityToken — no IdP daemon at P0. This is the only package permitted to
// import the PASETO library (the T-011 depguard rule); other packages consume
// the Signer / Verifier / CapabilityToken types.
package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"aidanwoods.dev/go-paseto"
	"github.com/google/uuid"
)

// Tiered token lifetimes (ADR-0046 §Decision).
const (
	AgentTTL    = 5 * time.Minute
	OperatorTTL = 8 * time.Hour
	ServiceTTL  = 1 * time.Hour
	// PairedTTL is the paired-session tier's lifetime (ADR-0068 §D4, the
	// declared ADR-0046 divergence). Deliberately the SAME number as
	// OperatorTTL: inventing a fifth TTL number would imply the TTL is the
	// control, and it is not — revocability is. Unlike an operator session, a
	// paired session gets NO sliding refresh (SEC-39): it expires hard and
	// must be re-paired.
	PairedTTL = 8 * time.Hour
)

// ErrInvalidToken is returned by Verify for any missing/expired/forged token or
// failed rule (audience, issuer). Callers (RequireCapability) map it to 401.
var ErrInvalidToken = errors.New("invalid capability token")

// Scope is the typed scope object: target resource + allowed operations, plus an
// optional lease id for egress leases (ADR-0046).
type Scope struct {
	Resource   string   `json:"resource,omitempty"`
	Operations []string `json:"operations,omitempty"`
	LeaseID    string   `json:"leaseId,omitempty"`
}

// CapabilityToken is the typed claim set carried by every token.
type CapabilityToken struct {
	Subject  string // sub — operator id, agent session id, or service workload name
	Edge     string // one of the seven capability edges + service-service
	Scope    Scope
	IssuedAt time.Time
	Expiry   time.Time
	TokenID  string // jti
	Audience string // aud — target service
	Issuer   string // iss — cluster id
}

// Key is a PASETO v4 local symmetric key (the cluster signing key). The same
// key signs and verifies (symmetric), so every service is configured with it.
type Key struct{ k paseto.V4SymmetricKey }

// GenerateKey returns a fresh random key and its hex encoding (for install.sh /
// operator provisioning).
func GenerateKey() (Key, string) {
	k := paseto.NewV4SymmetricKey()
	return Key{k}, hex.EncodeToString(k.ExportBytes())
}

// NewKeyFromHex parses a hex-encoded 32-byte key (e.g. from AIREOS_PASETO_KEY).
func NewKeyFromHex(h string) (Key, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return Key{}, fmt.Errorf("decode key hex: %w", err)
	}
	k, err := paseto.V4SymmetricKeyFromBytes(b)
	if err != nil {
		return Key{}, fmt.Errorf("load symmetric key: %w", err)
	}
	return Key{k}, nil
}

// Signer issues capability tokens under the cluster key for a given issuer.
type Signer struct {
	key    Key
	issuer string
}

func NewSigner(key Key, issuer string) *Signer { return &Signer{key: key, issuer: issuer} }

// IssueParams is the general issuance request; the convenience methods below
// cover the common P0 actor classes.
type IssueParams struct {
	Subject  string
	Edge     string
	Audience string
	Scope    Scope
	TTL      time.Duration
	// TokenID, when non-empty, becomes the token's jti verbatim instead of a
	// fresh random one (ADR-0068 §D4: the paired-session tier mints jti = the
	// paired_sessions row id, so services/api's per-request liveness check has
	// something to look up). Every existing caller leaves this empty and gets
	// the prior random-uuid behavior unchanged (see buildToken).
	TokenID string
}

// Issue mints a token for params. The chain-authority and other receivers verify
// it with a Verifier configured for the same key.
func (s *Signer) Issue(p IssueParams) (string, error) {
	t, err := buildToken(s.issuer, p)
	if err != nil {
		return "", err
	}
	return t.V4Encrypt(s.key.k, nil), nil
}

// buildToken assembles the typed capability claims onto a fresh PASETO token for
// the given issuer. It is shared by the symmetric Signer (V4Encrypt) and the
// AsymmetricSigner (V4Sign, asymmetric.go) so the two minting paths cannot drift
// in the claims they set (ADR-0060).
func buildToken(issuer string, p IssueParams) (paseto.Token, error) {
	if p.Subject == "" || p.Edge == "" {
		return paseto.Token{}, errors.New("auth: subject and edge are required")
	}
	if p.TTL <= 0 {
		return paseto.Token{}, errors.New("auth: ttl must be positive")
	}
	now := time.Now()
	t := paseto.NewToken()
	t.SetIssuedAt(now)
	t.SetNotBefore(now)
	t.SetExpiration(now.Add(p.TTL))
	t.SetSubject(p.Subject)
	t.SetIssuer(issuer)
	if p.Audience != "" {
		t.SetAudience(p.Audience)
	}
	if p.TokenID != "" {
		t.SetJti(p.TokenID)
	} else {
		t.SetJti(uuid.NewString())
	}
	t.SetString("edge", p.Edge)
	if err := t.Set("scope", p.Scope); err != nil {
		return paseto.Token{}, fmt.Errorf("auth: set scope claim: %w", err)
	}
	return t, nil
}

// IssueService mints a service-to-service token (edge service-service, 1h TTL)
// for workload `subject` calling `audience` — the ADR-0045 outbox relay → audit
// path and other internal RPCs.
func (s *Signer) IssueService(subject, audience string) (string, error) {
	return s.Issue(IssueParams{Subject: subject, Edge: "service-service", Audience: audience, TTL: ServiceTTL})
}

// IssueOperator mints an operator session token (edge human-agent, 8h TTL) for
// the given audience (the service the operator authenticates to, e.g. "api"), so
// the token is rejected by other services' verifiers.
func (s *Signer) IssueOperator(subject, audience string) (string, error) {
	return s.Issue(IssueParams{Subject: subject, Edge: "human-agent", Audience: audience, TTL: OperatorTTL})
}

// IssuePaired mints a paired-session token (ADR-0068 §D4/§D5): edge
// human-agent — a narrowed operator session is a SCOPE within human-agent,
// not an eighth capability edge, so the canonical seven-edge set
// (docs/security/agent-permission-model.md) is unchanged — TTL PairedTTL,
// and jti = tokenID (the paired_sessions row id services/api's per-request
// liveness check looks up on every request; ADR-0046's stateless model is
// the declared divergence here, not the edge set).
func (s *Signer) IssuePaired(subject, audience, tokenID string, scope Scope) (string, error) {
	return s.Issue(IssueParams{
		Subject: subject, Edge: "human-agent", Audience: audience,
		Scope: scope, TTL: PairedTTL, TokenID: tokenID,
	})
}

// IssueAgent mints a scoped agent capability token (edge agent-tool, 5min TTL) for
// an autonomous agent calling `audience` (the MCP gateway). The scope bounds the
// resource(s) + operations the agent may invoke (SEC-16); the short TTL is the
// revocation mechanism at v0.3.0 — there is no agent registry (ADR-0046/0056/0057).
func (s *Signer) IssueAgent(subject, audience string, scope Scope) (string, error) {
	return s.Issue(IssueParams{Subject: subject, Edge: "agent-tool", Audience: audience, Scope: scope, TTL: AgentTTL})
}

// Verifier verifies tokens under the cluster key, enforcing expiry and
// (optionally) the expected issuer and audience.
type Verifier struct {
	key      Key
	issuer   string
	audience string
}

// NewVerifier builds a verifier. issuer/audience may be empty to skip those
// rules; a service should set audience to its own name so a token minted for a
// different service is rejected.
func NewVerifier(key Key, issuer, audience string) *Verifier {
	return &Verifier{key: key, issuer: issuer, audience: audience}
}

// Verify decrypts and validates the bearer token, returning the typed claims.
// The context is accepted for interface symmetry; verification is local.
func (v *Verifier) Verify(_ context.Context, bearer string) (CapabilityToken, error) {
	parser := newParser(v.issuer, v.audience)
	t, err := parser.ParseV4Local(v.key.k, bearer, nil)
	if err != nil {
		return CapabilityToken{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	return claimsFromToken(t)
}

// newParser builds a PASETO parser with the common capability rules (expiry +
// optional issuer/audience). Shared by the symmetric Verifier (ParseV4Local) and
// the AsymmetricVerifier (ParseV4Public, asymmetric.go) so the two verify paths
// enforce identical rules (ADR-0060).
func newParser(issuer, audience string) paseto.Parser {
	parser := paseto.NewParser()
	parser.AddRule(paseto.NotExpired())
	parser.AddRule(paseto.ValidAt(time.Now()))
	if issuer != "" {
		parser.AddRule(paseto.IssuedBy(issuer))
	}
	if audience != "" {
		parser.AddRule(paseto.ForAudience(audience))
	}
	return parser
}

// claimsFromToken extracts the typed CapabilityToken from a validated PASETO
// token. Shared by both verifiers so claim mapping cannot drift (ADR-0060).
func claimsFromToken(t *paseto.Token) (CapabilityToken, error) {
	edge, err := t.GetString("edge")
	if err != nil {
		return CapabilityToken{}, fmt.Errorf("%w: missing edge claim", ErrInvalidToken)
	}
	var scope Scope
	_ = t.Get("scope", &scope) // scope is optional
	sub, _ := t.GetSubject()
	jti, _ := t.GetJti()
	aud, _ := t.GetAudience()
	iss, _ := t.GetIssuer()
	iat, _ := t.GetIssuedAt()
	exp, _ := t.GetExpiration()
	return CapabilityToken{
		Subject:  sub,
		Edge:     edge,
		Scope:    scope,
		IssuedAt: iat,
		Expiry:   exp,
		TokenID:  jti,
		Audience: aud,
		Issuer:   iss,
	}, nil
}
