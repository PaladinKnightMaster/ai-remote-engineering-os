package server

import "net/http"

// pairing.go — the four generated ServerInterface methods for QR device
// pairing + revocable paired operator sessions (ADR-0068 §D4-§D6/§D10-§D11;
// v0.12 "Access & Onboarding", E-02, T-114a). This ticket lands the CONTRACT
// only: every handler below answers 404, unconditionally, on every stack —
// pairing ships disabled by default (SEC-41), and T-114a does not implement
// the enabled path either (see pairingDisabled's doc comment for why both
// branches produce the identical response). T-114b fills in:
//   - the real request bodies (services/api/internal/pairing.Store calls)
//   - the per-request liveness check on the paired session's jti (SEC-34)
//   - approvals@1 route-level enforcement (SEC-35/SEC-36)
//   - the three pairing.* audit events (pairing.requested / pairing.redeemed
//     / pairing.revoked)
//   - cookie minting on redeem (mirrors /v1/auth/login)
//
// It deliberately does NOT touch slidingRefresh (server.go): a paired session
// carries the human-agent edge like any operator session, so once T-114b
// mints one, slidingRefresh's `cap.Edge == "human-agent"` check would also
// refresh it — directly violating SEC-39 ("no sliding refresh on the paired
// plane; it expires hard and must be re-paired"). T-114b must guard that
// check by the paired jti/scope; nothing about it changes here.

// pairingDisabled answers true (and writes the 404) whenever this handler
// must NOT proceed — either because pairing is off (SEC-41's shipped
// default, AIREOS_PAIRING_ENABLED unset) or, in T-114a, unconditionally,
// because no handler body exists yet. Both cases call http.NotFound
// identically and on purpose: SEC-41 requires that a disabled capability not
// advertise its own existence, and http.NotFound is byte-identical to chi's
// own default NotFoundHandler for a route that was never registered at all
// (go-chi/chi/v5 mux.go: NotFoundHandler() falls back to the stdlib
// http.NotFound when none is set, and this router sets none) — so probing
// any of these four routes, enabled or not, looks exactly like probing a
// path that does not exist. No log line, no audit event, no body variation
// by route (no oracle, the ADR-0065/0066 precedent).
func (a *apiHandlers) pairingDisabled(w http.ResponseWriter, r *http.Request) bool {
	if !a.pairingEnabled {
		http.NotFound(w, r)
		return true
	}
	return false
}

// CreatePairingRequest — POST /v1/pairing-requests (desktop operator).
func (a *apiHandlers) CreatePairingRequest(w http.ResponseWriter, r *http.Request) {
	if a.pairingDisabled(w, r) {
		return
	}
	// Reachable only when AIREOS_PAIRING_ENABLED=1. T-114b fills in the real
	// body here: internal/pairing.Store.Create, the one-time secret in the
	// response, pairing.requested. Until T-114b lands, an enabled stack also
	// answers 404 — deliberately, so "off" and "not yet built" are
	// indistinguishable to any caller (this ticket's honest state).
	http.NotFound(w, r)
}

// RedeemPairingRequest — POST /v1/pairing-requests/{id}/redeem (the device
// being paired; unauthenticated — security: [] in the contract, since the
// redeeming device holds only the fragment-carried secret, never a bearer
// token).
func (a *apiHandlers) RedeemPairingRequest(w http.ResponseWriter, r *http.Request, id string) {
	if a.pairingDisabled(w, r) {
		return
	}
	// T-114b fills in: internal/pairing.Store.Redeem, session-cookie minting
	// via auth.Signer.IssuePaired, pairing.redeemed. See CreatePairingRequest
	// for why an enabled-but-unbuilt stack also 404s here in this ticket.
	_ = id
	http.NotFound(w, r)
}

// ListPairedSessions — GET /v1/paired-sessions (desktop operator; owner-scoped).
func (a *apiHandlers) ListPairedSessions(w http.ResponseWriter, r *http.Request, params ListPairedSessionsParams) {
	if a.pairingDisabled(w, r) {
		return
	}
	// T-114b fills in: internal/pairing.Store.ListForOperator, paged the same
	// way grants/workstreams are. See CreatePairingRequest for why an
	// enabled-but-unbuilt stack also 404s here in this ticket.
	_ = params
	http.NotFound(w, r)
}

// RevokePairedSession — POST /v1/paired-sessions/{id}/revoke (desktop
// operator; owner-scoped; the audited terminal flip, ADR-0068 §D4).
func (a *apiHandlers) RevokePairedSession(w http.ResponseWriter, r *http.Request, id string) {
	if a.pairingDisabled(w, r) {
		return
	}
	// T-114b fills in: internal/pairing.Store.Revoke, pairing.revoked. See
	// CreatePairingRequest for why an enabled-but-unbuilt stack also 404s
	// here in this ticket.
	_ = id
	http.NotFound(w, r)
}
