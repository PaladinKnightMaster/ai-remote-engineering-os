package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/pairing"
)

// pairing.go — the four ServerInterface methods for QR device pairing +
// revocable paired operator sessions (ADR-0068 §D4-§D6/§D10-§D11; v0.12
// "Access & Onboarding", E-02), PLUS the SEC-34/SEC-35/SEC-36 enforcement
// logic gateByDeclaredSecurity (server.go) calls into. T-114a landed the
// contract with every handler answering 404 unconditionally; T-114b (this
// file) is the real implementation:
//   - the four handlers, backed by internal/pairing.Store
//   - the per-request liveness check on the paired session's jti (SEC-34)
//   - approvals@1 route-level enforcement, deny-by-default (SEC-35/SEC-36)
//   - cookie minting on redeem (mirrors /v1/auth/login)
//
// The three pairing.* audit events (AUD-32) are chained inside
// internal/pairing/store.go's own CreateInTx/RedeemInTx/RevokeInTx — this file
// never writes an outbox event directly, mirroring the grant/workstream
// packages' own-store-owns-its-audit convention.

// pairingDisabled answers true (and writes the 404) whenever pairing is off
// (SEC-41's shipped default, AIREOS_PAIRING_ENABLED unset). SEC-41 requires
// that a disabled capability not advertise its own existence, and
// http.NotFound is byte-identical to chi's own default NotFoundHandler for a
// route that was never registered at all — so probing any of these four
// routes, enabled or not, looks exactly like probing a path that does not
// exist. No log line, no audit event, no body variation by route (no oracle,
// the ADR-0065/0066 precedent).
func (a *apiHandlers) pairingDisabled(w http.ResponseWriter, r *http.Request) bool {
	if !a.pairingEnabled {
		http.NotFound(w, r)
		return true
	}
	return false
}

// CreatePairingRequest — POST /v1/pairing-requests (desktop operator; denied
// to a live paired session itself — "creating further pairings" is one of
// ADR-0068 §D5's named refusals, enforced by pairingRouteActions simply never
// mapping this route).
func (a *apiHandlers) CreatePairingRequest(w http.ResponseWriter, r *http.Request) {
	if a.pairingDisabled(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.pairings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "pairing is not configured")
		return
	}
	var body struct {
		Label *string `json:"label"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	label := ""
	if body.Label != nil {
		label = *body.Label
	}
	req, secret, err := a.pairings.Create(r.Context(), pairing.CreateInput{Operator: operator, Label: label})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "create pairing request failed")
		return
	}
	// The plaintext secret is returned EXACTLY ONCE, here, base64url-encoded
	// for the QR fragment (ADR-0068 §D6: "https://<site>/pair#<id>.<secret>" —
	// RawURLEncoding is URL-fragment-safe with no padding). It is never
	// logged and never appears in any other response (redeem/list/revoke all
	// carry only the hash's absence).
	writeJSON(w, http.StatusCreated, PairingRequestCreated{
		Id: req.ID, ExpiresAt: req.ExpiresAt, Secret: base64.RawURLEncoding.EncodeToString(secret),
	})
}

// RedeemPairingRequest — POST /v1/pairing-requests/{id}/redeem (the device
// being paired; security: [] in the contract — the redeeming device holds
// only the fragment-carried secret, never a bearer token, so it never reaches
// gateByDeclaredSecurity's enforced branch at all). Every refusal — unknown
// id, malformed id, malformed secret, wrong secret, expired, already consumed,
// or pairing disabled after this point is reached — answers the identical
// 404 (no oracle, mirroring SEC-34's per-request liveness posture).
func (a *apiHandlers) RedeemPairingRequest(w http.ResponseWriter, r *http.Request, id string) {
	if a.pairingDisabled(w, r) {
		return
	}
	if a.pairings == nil || a.signer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "pairing is not configured")
		return
	}
	var body struct {
		Secret string `json:"secret"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Secret == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "secret is required")
		return
	}
	secret, err := base64.RawURLEncoding.DecodeString(body.Secret)
	if err != nil {
		// A malformed secret is refused IDENTICALLY to a well-formed-but-wrong
		// one — the same no-oracle posture RedeemInTx's uuid.Parse-before-query
		// guard applies to the id half of this same request.
		writeError(w, http.StatusNotFound, "not_found", "pairing request not found or expired")
		return
	}
	sess, ok, err := a.pairings.Redeem(r.Context(), id, secret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "redeem failed")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "pairing request not found or expired")
		return
	}
	// jti = sess.ID (the paired_sessions row id) — the substrate SEC-34's
	// per-request liveness check looks up on every subsequent request
	// (gateByDeclaredSecurity → pairingSessionAllowed below).
	token, err := a.signer.IssuePaired(sess.Operator, "api", sess.ID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "issue token failed")
		return
	}
	// setSessionCookies verbatim (server.go, used by Login): PairedTTL and
	// OperatorTTL are deliberately the SAME 8h value (ADR-0068 §D4), so its
	// hardcoded MaxAge is correct for a paired session too.
	if err := setSessionCookies(w, token); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "set cookies failed")
		return
	}
	writeJSON(w, http.StatusOK, PairingRedeemResponse{ExpiresAt: sess.ExpiresAt})
}

// ListPairedSessions — GET /v1/paired-sessions (desktop operator; owner-scoped,
// live and revoked sessions alike — the desk's revoke list needs history too).
// Denied to a live paired session itself (§D6: pairing management stays
// desk-only) — enforced the same way as CreatePairingRequest, by omission from
// pairingRouteActions.
func (a *apiHandlers) ListPairedSessions(w http.ResponseWriter, r *http.Request, params ListPairedSessionsParams) {
	if a.pairingDisabled(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.pairings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "pairing is not configured")
		return
	}
	limit := 50
	if params.Limit != nil {
		n := int(*params.Limit)
		if n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer in [1,200]")
			return
		}
		limit = n
	}
	var after string
	if params.Cursor != nil {
		after = string(*params.Cursor)
	}
	sessions, last, err := a.pairings.ListForOperator(r.Context(), operator, after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list paired sessions failed")
		return
	}
	page := pairedSessionsPage{Items: make([]PairedSession, 0, len(sessions))}
	for _, sess := range sessions {
		page.Items = append(page.Items, toPairedSessionView(sess))
	}
	if len(sessions) == limit {
		page.Page.NextCursor = &last
	}
	writeJSON(w, http.StatusOK, page)
}

// RevokePairedSession — POST /v1/paired-sessions/{id}/revoke (desktop operator;
// owner-scoped; the audited terminal flip, ADR-0068 §D4). Idempotent per the
// contract: revoking an already-revoked session the caller owns still answers
// 200 (Get, not GetLive, backs the ownership check — GetLive would make the
// caller's OWN already-revoked session look unknown to them). Denied to a live
// paired session itself, same as ListPairedSessions.
func (a *apiHandlers) RevokePairedSession(w http.ResponseWriter, r *http.Request, id string) {
	if a.pairingDisabled(w, r) {
		return
	}
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.pairings == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "pairing is not configured")
		return
	}
	sess, err := a.pairings.Get(r.Context(), id)
	if errors.Is(err, pairing.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "paired session not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "revoke paired session failed")
		return
	}
	if sess.Operator != operator {
		writeError(w, http.StatusNotFound, "not_found", "paired session not found") // owner-scoped existence-hiding
		return
	}
	if _, err := a.pairings.Revoke(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "revoke paired session failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

// pairedSessionsPage is ListPairedSessions' response envelope — the contract's
// own PairingSessionsPage component (ADR-0068 §D11: NOT the shared pageMeta/
// PageMeta used by seven other list routes; CON-3).
type pairedSessionsPage struct {
	Items []PairedSession     `json:"items"`
	Page  PairingSessionsPage `json:"page"`
}

func toPairedSessionView(sess pairing.Session) PairedSession {
	label := sess.Label
	return PairedSession{
		Id: sess.ID, Label: &label, Scope: sess.Scope,
		ExpiresAt: sess.ExpiresAt, RevokedAt: sess.RevokedAt, CreatedAt: sess.CreatedAt,
	}
}

// pairingStore is the slice of *pairing.Store the four handlers above AND the
// SEC-34 enforcement gate need (WithPairingStore, server.go).
type pairingStore interface {
	Create(ctx context.Context, in pairing.CreateInput) (pairing.Request, []byte, error)
	Redeem(ctx context.Context, id string, secret []byte) (pairing.Session, bool, error)
	GetLive(ctx context.Context, id string) (pairing.Session, error)
	Get(ctx context.Context, id string) (pairing.Session, error)
	ListForOperator(ctx context.Context, operator, after string, limit int) ([]pairing.Session, string, error)
	Revoke(ctx context.Context, id string) (bool, error)
}

// pairingLiveChecker is the narrow slice gateByDeclaredSecurity (server.go)
// needs for SEC-34's per-request liveness check — GetLive only, so the
// enforcement chokepoint depends on nothing beyond what it actually calls.
type pairingLiveChecker interface {
	GetLive(ctx context.Context, id string) (pairing.Session, error)
}

// pairingSessionAllowed is SEC-34+SEC-35's enforcement point, called from
// gateByDeclaredSecurity (server.go) AFTER RequireCapability has populated the
// capability and CSRF has already cleared. PERF-12: an ordinary desktop token
// (Scope.Name == "") takes NO extra read at all — the discriminator is the
// signed claim, not a database probe.
func pairingSessionAllowed(w http.ResponseWriter, r *http.Request, pairings pairingLiveChecker) bool {
	c, ok := mw.CapabilityFromContext(r.Context())
	if !ok || c.Scope.Name != pairing.ApprovalsScopeV1 {
		return true
	}
	if pairings == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "paired session liveness unavailable")
		return false
	}
	if _, err := pairings.GetLive(r.Context(), c.TokenID); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "paired session is not live")
		return false
	}
	actions, mapped := pairingRouteActions[pairingRouteKeyFor(r)]
	if !mapped || !allPermitted(actions) {
		writeError(w, http.StatusForbidden, "forbidden", "not permitted for a paired session")
		return false
	}
	return true
}

// pairingRouteKeyFor reads the CHI-matched route pattern off the request
// context. chi has already matched the route by the time gateByDeclaredSecurity
// runs (it wraps the generated per-operation ServerInterfaceWrapper, which chi
// invokes only after routing), so RoutePattern() is always populated here.
func pairingRouteKeyFor(r *http.Request) pairingRouteKey {
	pattern := ""
	if rc := chi.RouteContext(r.Context()); rc != nil {
		pattern = rc.RoutePattern()
	}
	return pairingRouteKey{Method: r.Method, Pattern: pattern}
}

type pairingRouteKey struct {
	Method  string
	Pattern string
}

// pairingRouteActions is SEC-35/SEC-36's single source of truth: the ONLY
// (method, chi route pattern) pairs a live paired session may reach, each
// mapped onto the approvals@1 action symbol(s) it requires
// (pairing.ApprovalsV1Permit, internal/pairing/pairing.go). Deny-by-default
// (SEC-35): anything registered on the router but absent here is refused
// outright by pairingSessionAllowed, with no further check.
// TestPairingRouteWalkDenyByDefault (pairing_test.go) walks the REAL router
// and empirically drives a live paired session against every route this map
// does not classify — a route added later without an entry here fails that
// test rather than silently widening the surface.
//
// Judgment calls made here, beyond ADR-0068 §D5's literal text (recorded
// honestly, not hidden):
//   - GET /v1/sessions is mapped to read-pending-approvals even though it
//     returns ALL of the operator's sessions, not only pending_approval ones —
//     no filtered route exists, and refusing it entirely would make session
//     approval invisible on the phone (defeating half of D5's collapsed
//     approval surface — SEC-36 names "pending approvals" explicitly).
//   - POST /v1/runs/{id}/approve|deny and /cancel (the adaptive-plan-preview
//     review + cancel) are DENIED: SEC-36's enumeration names "invocations"
//     and "approve/deny" without naming Runs, and D5 separately denies
//     "launching a Run" broadly — reviewing/canceling a Run is Run-engine
//     governance, not the collapsed approvals@1 surface.
//   - GET /v1/paired-sessions and POST /v1/paired-sessions/{id}/revoke are
//     DENIED for a paired caller — pairing management stays desk-only (§D6:
//     "the desktop cockpit lists paired sessions and revokes any of them").
//   - Single-item GETs outside the approval surface (GetSession/GetGrant/
//     GetRun/GetResource/GetWorkstream/...) are DENIED — the list views are
//     what the phone approval surface needs; a detail read is not named
//     anywhere in SEC-36.
var pairingRouteActions = map[pairingRouteKey][]string{
	{Method: http.MethodGet, Pattern: "/v1/auth/me"}:                   {"read-own-session"},
	{Method: http.MethodGet, Pattern: "/v1/audit/events"}:              {"read-audit"},
	{Method: http.MethodGet, Pattern: "/v1/invocations"}:               {"read-pending-invocations"},
	{Method: http.MethodPost, Pattern: "/v1/invocations/{id}/resolve"}: {"approve", "deny"},
	{Method: http.MethodGet, Pattern: "/v1/sessions"}:                  {"read-pending-approvals"},
	{Method: http.MethodPost, Pattern: "/v1/sessions/{id}/resolve"}:    {"approve", "deny"},
}

// pairingPublicRoutes are the three security:[] operations (Login, Logout,
// RedeemPairingRequest) that bypass gateByDeclaredSecurity's enforced branch
// entirely — BearerAuthScopes is never set in their request context (gen.go),
// so pairingSessionAllowed is never even called for them; a live paired
// session reaches them exactly as anyone unauthenticated would. Listed here
// ONLY so TestPairingRouteWalkDenyByDefault can classify every walked route
// deliberately rather than silently skip some of them.
var pairingPublicRoutes = map[pairingRouteKey]bool{
	{Method: http.MethodPost, Pattern: "/v1/auth/login"}:                   true,
	{Method: http.MethodPost, Pattern: "/v1/auth/logout"}:                  true,
	{Method: http.MethodPost, Pattern: "/v1/pairing-requests/{id}/redeem"}: true,
}

var approvalsV1PermitSet = buildApprovalsV1PermitSet()

func buildApprovalsV1PermitSet() map[string]bool {
	m := make(map[string]bool, len(pairing.ApprovalsV1Permit))
	for _, action := range pairing.ApprovalsV1Permit {
		m[action] = true
	}
	return m
}

// allPermitted requires every action symbol a route maps to be a member of
// ApprovalsV1Permit (SEC-36's single-sourced set) — and requires at least one,
// so an accidentally-empty slice is never silently treated as "no
// requirement."
func allPermitted(actions []string) bool {
	if len(actions) == 0 {
		return false
	}
	for _, action := range actions {
		if !approvalsV1PermitSet[action] {
			return false
		}
	}
	return true
}
