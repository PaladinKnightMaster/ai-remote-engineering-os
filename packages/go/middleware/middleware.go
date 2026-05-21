// Package middleware holds the shared capability-edge middleware mounted
// identically by every P0 Go service (ADR-0043). Every middleware is a plain
// func(http.Handler) http.Handler so the chain stays net/http-compatible and
// framework-agnostic; no custom request-context type leaks across the handler
// signature.
package middleware

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey int

const capabilityCtxKey ctxKey = iota

// CapabilityFromContext returns the verified Capability stashed by
// RequireCapability, and whether one was present.
func CapabilityFromContext(ctx context.Context) (Capability, bool) {
	c, ok := ctx.Value(capabilityCtxKey).(Capability)
	return c, ok
}

// RequireCapability verifies the bearer token via v and enforces that the
// token's edge is one of allowedEdges before passing the request through. The
// verified Capability is stashed on the request context for handlers.
//
// allowedEdges is the set of capability edges (docs/security/agent-permission-model.md
// plus service-service) permitted on the guarded route. For POST /v1/events the
// audit-service passes "service-service" — producers fan in over that edge
// (ADR-0045). An empty allowedEdges accepts any edge the Verifier returns.
func RequireCapability(v Verifier, allowedEdges ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(allowedEdges))
	for _, e := range allowedEdges {
		allowed[e] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bearer, ok := bearerToken(r)
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing or malformed Authorization header")
				return
			}
			c, err := v.Verify(r.Context(), bearer)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid capability token")
				return
			}
			if len(allowed) > 0 {
				if _, ok := allowed[c.Edge]; !ok {
					writeError(w, http.StatusForbidden, "forbidden", "capability edge does not permit this operation")
					return
				}
			}
			ctx := context.WithValue(r.Context(), capabilityCtxKey, c)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// writeError emits the cross-cutting Error envelope from the OpenAPI contract
// (components/schemas/Error: {code, message}).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message})
}
