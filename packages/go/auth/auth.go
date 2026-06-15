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
}

// Issue mints a token for params. The chain-authority and other receivers verify
// it with a Verifier configured for the same key.
func (s *Signer) Issue(p IssueParams) (string, error) {
	if p.Subject == "" || p.Edge == "" {
		return "", errors.New("auth: subject and edge are required")
	}
	if p.TTL <= 0 {
		return "", errors.New("auth: ttl must be positive")
	}
	now := time.Now()
	t := paseto.NewToken()
	t.SetIssuedAt(now)
	t.SetNotBefore(now)
	t.SetExpiration(now.Add(p.TTL))
	t.SetSubject(p.Subject)
	t.SetIssuer(s.issuer)
	if p.Audience != "" {
		t.SetAudience(p.Audience)
	}
	t.SetJti(uuid.NewString())
	t.SetString("edge", p.Edge)
	if err := t.Set("scope", p.Scope); err != nil {
		return "", fmt.Errorf("auth: set scope claim: %w", err)
	}
	return t.V4Encrypt(s.key.k, nil), nil
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
	parser := paseto.NewParser()
	parser.AddRule(paseto.NotExpired())
	parser.AddRule(paseto.ValidAt(time.Now()))
	if v.issuer != "" {
		parser.AddRule(paseto.IssuedBy(v.issuer))
	}
	if v.audience != "" {
		parser.AddRule(paseto.ForAudience(v.audience))
	}
	t, err := parser.ParseV4Local(v.key.k, bearer, nil)
	if err != nil {
		return CapabilityToken{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}

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
