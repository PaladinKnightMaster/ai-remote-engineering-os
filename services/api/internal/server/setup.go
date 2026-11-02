package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"

	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/setup"
)

// setup.go — first-run setup over HTTP (ADR-0078; v0.17 FRX-2/3, SEC-64/65). While no
// operator exists api is in SETUP MODE: setupGuard answers 503 `setup_required` for every
// route except GET /v1/setup and POST /v1/setup/complete. Once an operator exists (or when
// one came from the environment, which always wins -- ADR-0078 §D4) those two routes
// answer EXACTLY as an unknown route does (SEC-65), because setupGuard (a router-level
// middleware, so it runs before routing) replies with the same http.NotFound chi uses.

// setupStore is the slice of internal/setup the completion handler needs.
type setupStore interface {
	Complete(ctx context.Context, code, username, password, clientAddr string) (*account.Operator, error)
}

// WithSetup puts api in setup mode (ADR-0078 §D2): st completes setup, holder is the
// Authenticator handed to New, which completion Set()s so login works without a restart.
// Not passing it (an operator from the environment or the operators table already
// exists) means the setup routes are unknown routes from the first request.
func WithSetup(st setupStore, holder *account.Holder) Option {
	return func(h *apiHandlers) {
		h.setup, h.setupHolder = st, holder
		h.setupRequired.Store(true)
	}
}

const (
	setupStatusPath   = "/v1/setup"
	setupCompletePath = "/v1/setup/complete"
	maxSetupBody      = 4 << 10 // code + username + password; bounds an unauthenticated body
)

// setupGuard runs first on every request. It has NO exemptions beyond the two setup routes:
// the SPA is served by Caddy, not api, and install.sh's INS-10 health wait requests the
// SPA at `/` over Caddy, so nothing that must succeed pre-setup reaches this process.
func (a *apiHandlers) setupGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		switch {
		case a.setupRequired.Load():
			isSetupRoute := (r.Method == http.MethodGet && p == setupStatusPath) ||
				(r.Method == http.MethodPost && p == setupCompletePath)
			if !isSetupRoute {
				writeError(w, http.StatusServiceUnavailable, "setup_required", "setup required")
				return
			}
		case p == setupStatusPath || p == setupCompletePath:
			// ANY method: a 405 here would tell a probe the path once meant something.
			http.NotFound(w, r) // what chi answers for a path no route matches
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GetSetupStatus (GET /v1/setup) tells the SPA to show its setup page. Public.
func (a *apiHandlers) GetSetupStatus(w http.ResponseWriter, r *http.Request) {
	if !a.setupRequired.Load() {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"setupRequired": true})
}

// CompleteSetup (POST /v1/setup/complete) creates the first operator from the setup code
// and signs them in. Public: there is no session yet and the code IS the secret, so there
// is no CSRF token to double-submit; instead the body must be application/json, which a
// cross-site HTML form cannot send and a cross-origin fetch cannot send without a
// preflight api never approves. The code and password travel only in the body, and no
// response or log line echoes either.
func (a *apiHandlers) CompleteSetup(w http.ResponseWriter, r *http.Request) {
	if !a.setupRequired.Load() || a.setup == nil || a.signer == nil {
		http.NotFound(w, r)
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody))
	dec.DisallowUnknownFields()
	var body struct {
		Code     string `json:"code"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := dec.Decode(&body); err != nil || body.Code == "" || body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "code, username and password are required")
		return
	}
	op, err := a.setup.Complete(r.Context(), body.Code, body.Username, body.Password, clientAddr(r))
	var inputErr *setup.InputError
	switch {
	case errors.As(err, &inputErr):
		writeError(w, http.StatusBadRequest, "bad_request", inputErr.Reason)
		return
	case errors.Is(err, setup.ErrInvalidCode):
		writeError(w, http.StatusForbidden, "invalid_setup_code", "the setup code is invalid or has expired; run `setup.sh --new-setup-code` on the server for a new one")
		return
	case errors.Is(err, setup.ErrOperatorExists):
		// A concurrent completion won. Its handler installs the operator and drops the
		// flag; this loser must not flip it first (login would find no operator yet).
		http.NotFound(w, r)
		return
	case err != nil:
		log.Printf("setup: completion failed: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "setup failed")
		return
	}
	// Leave setup mode in-process: install the operator BEFORE dropping the flag so a
	// login arriving the instant after already finds it.
	a.setupHolder.Set(op)
	a.setupRequired.Store(false)
	log.Printf("setup: complete; operator %q created", op.Username)
	a.establishSession(w, r, "operator:"+op.Username)
}

// clientAddr is the peer IP for the setup.completed audit event, taken from the socket,
// or from the last X-Forwarded-For entry when the peer is loopback (Caddy, on the same
// host, appends the real client). Only a parseable IP is returned, so a forged header
// cannot inject text into the audit chain.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if fwd := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); fwd != nil {
				return fwd.String()
			}
		}
	}
	if ip == nil {
		return "unknown"
	}
	return ip.String()
}
