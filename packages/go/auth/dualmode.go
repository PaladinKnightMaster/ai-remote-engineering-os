package auth

// dualmode.go composes the symmetric and asymmetric verifiers into one verifier
// that accepts BOTH token types on a single edge (ADR-0060 §4). This is what lets
// a service be migrated to asymmetric incrementally: policy-service and
// audit-service keep verifying api's v4.local tokens while also accepting the
// gateway's v4.public tokens, with no flag day. Dispatch is on the PASETO header
// (the version.purpose prefix), which TestTokenTypeIsolation (asymmetric_test.go)
// proves is unforgeable across types.

import (
	"context"
	"fmt"
	"strings"
)

// DualModeVerifier dispatches on the PASETO token type: "v4.local." → the
// symmetric Verifier (the cluster key); "v4.public." → the AsymmetricVerifier (a
// registered workload public key). The asymmetric half is optional — a service
// not yet issued a public key configures only the symmetric half, and a v4.public
// token then fails closed (ErrInvalidToken), never panics. Each half enforces its
// own audience/edge/expiry rules; the dispatch only selects the key.
type DualModeVerifier struct {
	local  *Verifier
	public *AsymmetricVerifier // optional; nil → v4.public tokens are rejected
}

// NewDualModeVerifier composes a required symmetric verifier with an optional
// asymmetric one (nil until the service is given a workload public key).
func NewDualModeVerifier(local *Verifier, public *AsymmetricVerifier) *DualModeVerifier {
	return &DualModeVerifier{local: local, public: public}
}

// Verify selects the verifier by token type and returns the typed claims.
func (d *DualModeVerifier) Verify(ctx context.Context, bearer string) (CapabilityToken, error) {
	switch {
	case strings.HasPrefix(bearer, "v4.public."):
		if d.public == nil {
			return CapabilityToken{}, fmt.Errorf("%w: v4.public token but no asymmetric verifier configured", ErrInvalidToken)
		}
		return d.public.Verify(ctx, bearer)
	case strings.HasPrefix(bearer, "v4.local."):
		return d.local.Verify(ctx, bearer)
	default:
		return CapabilityToken{}, fmt.Errorf("%w: unrecognized token type", ErrInvalidToken)
	}
}
