package auth

import (
	"context"

	mw "aireos.local/packages/go/middleware"
)

// asMiddleware adapts a *Verifier to the middleware.Verifier port (defined in
// packages/go/middleware, T-004a) so it can be passed to RequireCapability.
//
// ADR-0046 §Verification phrases this as "RequireCapability imports
// packages/go/auth/"; we invert the dependency instead — middleware stays
// dependency-free (it shipped before auth, and every service imports it) and
// auth provides this adapter. The net behavior is identical: RequireCapability
// verifies via the PASETO Verifier and extracts edge + scope.
// tokenVerifier is the shared verify behavior of *Verifier, *AsymmetricVerifier,
// and *DualModeVerifier — anything that turns a bearer into typed claims. The
// adapter takes this interface so any of the three wraps into RequireCapability
// (ADR-0060: a migrating service may be symmetric-only, asymmetric-only, or
// dual-mode on a given edge).
type tokenVerifier interface {
	Verify(ctx context.Context, bearer string) (CapabilityToken, error)
}

type asMiddleware struct{ v tokenVerifier }

func (a asMiddleware) Verify(ctx context.Context, bearer string) (mw.Capability, error) {
	ct, err := a.v.Verify(ctx, bearer)
	if err != nil {
		return mw.Capability{}, err
	}
	return mw.Capability{
		Subject: ct.Subject,
		Edge:    ct.Edge,
		Scope: mw.Scope{
			Resource:   ct.Scope.Resource,
			Operations: ct.Scope.Operations,
			LeaseID:    ct.Scope.LeaseID,
			Name:       ct.Scope.Name,
		},
		// ExpiresAt + Audience let services/api do sliding-refresh (ADR-0047 §6)
		// without re-parsing the bearer. Adapters that don't expose these (e.g.
		// the dev allow-all in services/api/main.go) leave them zero-valued.
		ExpiresAt: ct.Expiry,
		Audience:  ct.Audience,
		// TokenID (jti) is the paired-session tier's per-request liveness
		// lookup key (ADR-0068 §D4); every other edge simply carries it
		// unused.
		TokenID: ct.TokenID,
	}, nil
}

// AsMiddlewareVerifier wraps any token verifier (symmetric *Verifier, asymmetric
// *AsymmetricVerifier, or *DualModeVerifier) so it satisfies middleware.Verifier.
func AsMiddlewareVerifier(v tokenVerifier) mw.Verifier { return asMiddleware{v} }
