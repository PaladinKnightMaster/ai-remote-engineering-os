package middleware

import (
	"context"
	"errors"
	"time"
)

// Capability is the verified capability a bearer token encodes (ADR-0046).
// The seven capability edges from docs/security/agent-permission-model.md plus
// service-service ride on Edge; Scope carries the typed target + operations.
//
// ExpiresAt + Audience are populated by adapters that can extract them from
// the underlying token (packages/go/auth's PASETO Verifier does). Adapters
// that cannot (e.g. dev allow-all stubs) leave them zero-valued; downstream
// features that depend on them (services/api sliding-refresh, ADR-0047 §6)
// short-circuit on a zero ExpiresAt. Adding fields to this struct is backward-
// compatible for consumers that only read Subject/Edge/Scope.
type Capability struct {
	Subject   string
	Edge      string
	Scope     Scope
	ExpiresAt time.Time
	Audience  string
}

// Scope is the typed scope object from ADR-0046 (resource + operations, plus
// an optional lease id for egress leases).
type Scope struct {
	Resource   string
	Operations []string
	LeaseID    string
}

// ErrUnauthorized is returned by a Verifier when a token is missing, malformed,
// expired, or otherwise unverifiable. RequireCapability maps it to 401.
var ErrUnauthorized = errors.New("unauthorized")

// Verifier verifies a bearer token and returns the Capability it encodes.
//
// This is a port. The concrete PASETO v4 implementation lives in
// packages/go/auth/ and lands with the first auth-touching service ticket
// (T-008, ADR-0046 §Hand-off). Middleware depends only on this interface so
// the github.com/aidantwoods/go-paseto dependency stays isolated to
// packages/go/auth/ (the T-011 depguard rule), and so the capability edge can
// be wired at T-004 before token issuance exists.
type Verifier interface {
	Verify(ctx context.Context, bearer string) (Capability, error)
}
