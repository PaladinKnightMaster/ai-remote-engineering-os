// Package server is the session-broker HTTP surface (ADR-0043). It exposes an
// unauthenticated readiness endpoint (served over HTTP/2 via h2c by main) and a
// capability-gated WebSocket channel that bridges the operator's browser to an
// SSH PTY on the target (ADR-0048 data plane). Session-lifecycle REST lives on
// services/api (T-016b); WebSocket does not model cleanly in OpenAPI.
//
// Channel flow (T-017b, T-030): authorize the caller's capability against the
// session's operator (operator-scope on the data plane, ADR-0048 §5), load the
// target coordinates + lease the decrypted credential from connector-service,
// accept the WS upgrade, dial SSH with a pinned host key, mark the session active
// and emit session.open, pipe + record to an asciinema cast, then on teardown
// mark it closed and emit session.close anchoring the cast SHA256.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/session-broker/internal/cast"
	"aireos.local/services/session-broker/internal/connector"
	"aireos.local/services/session-broker/internal/sshpty"
	"aireos.local/services/session-broker/internal/store"
)

// SessionStore is the narrow slice of *store.Store the channel handler needs.
type SessionStore interface {
	LoadSession(ctx context.Context, id string) (store.Session, error)
	LoadTargetCoords(ctx context.Context, resourceID string) (store.Target, error)
	Activate(ctx context.Context, sessionID, operatorID string) error
	Close(ctx context.Context, sessionID, operatorID, reason, castPath, castSHA string) error
	RecordInputLine(ctx context.Context, sessionID, operatorID, lineSHA string) error
}

// CredentialLeaser leases the decrypted SSH credential from connector-service
// (T-030). The broker no longer holds AIREOS_RESOURCE_SECRET_KEY.
type CredentialLeaser interface {
	Lease(ctx context.Context, resourceID, sessionID, operatorSubject string) (connector.Material, error)
}

// Config wires the broker handler's dependencies.
type Config struct {
	Verifier    mw.Verifier
	Store       SessionStore
	Leaser      CredentialLeaser // connector-service credential lease (T-030)
	CastDir     string           // directory for <sessionID>.cast files
	IdleTimeout time.Duration    // input-inactivity close (ADR-0048 §8; default 30m)
}

type broker struct {
	store   SessionStore
	leaser  CredentialLeaser
	castDir string
	idle    time.Duration
}

// New returns the session-broker router. The WebSocket channel is gated by
// RequireCapability over the agent-remote-session edge (the cockpit's remote
// session capability) plus human-agent; /healthz is public. Same shared
// middleware chain as every other service (ADR-0043 §Verification item 5).
func New(cfg Config) http.Handler {
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = 30 * time.Minute
	}
	b := &broker{store: cfg.Store, leaser: cfg.Leaser, castDir: cfg.CastDir, idle: idle}
	r := chi.NewRouter()
	r.Get("/healthz", b.healthz)
	r.Group(func(r chi.Router) {
		// The browser WS upgrade carries only the HttpOnly aireos_session cookie
		// (it cannot set Authorization); translate it to a bearer before the
		// capability check (T-018a, shared with services/api).
		r.Use(mw.CookieToAuthHeader(mw.SessionCookieName))
		r.Use(mw.RequireCapability(cfg.Verifier, "agent-remote-session", "human-agent"))
		r.Get("/v1/sessions/{id}/channel", b.channel)
	})
	return r
}

func (b *broker) healthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

// channel bridges the WebSocket to an SSH PTY for one session.
func (b *broker) channel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	cap, ok := mw.CapabilityFromContext(ctx)
	if !ok || cap.Subject == "" {
		http.Error(w, "no capability", http.StatusUnauthorized)
		return
	}
	operator := cap.Subject

	// --- pre-upgrade checks (proper HTTP status before the WS handshake) ------
	sess, err := b.store.LoadSession(ctx, id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.OperatorID != operator {
		// Operator-scope on the data plane: another operator's session is 404,
		// not 403 — its existence is not disclosed (ADR-0048 §5).
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if sess.Status != "pending" && sess.Status != "active" {
		http.Error(w, "session is not open", http.StatusConflict)
		return
	}
	target, err := b.store.LoadTargetCoords(ctx, sess.ResourceID)
	if err != nil {
		// Missing/incomplete target coordinates — the session cannot be brokered.
		// 424 Failed Dependency.
		http.Error(w, "target unavailable: "+err.Error(), http.StatusFailedDependency)
		return
	}
	// Lease the decrypted credential from connector-service (T-030). The broker
	// holds no key. Distinguish the failure modes so an operator can tell an
	// unprovisioned resource from a connector outage (T-030a fixup):
	//   - no credential stored      → 424 Failed Dependency (genuinely unprovisioned)
	//   - connector down / transport → 502 Bad Gateway (upstream unavailable; same
	//     convention as the api audit read-proxy)
	mat, err := b.leaser.Lease(ctx, sess.ResourceID, id, operator)
	if errors.Is(err, connector.ErrNoCredential) {
		http.Error(w, "no credential provisioned for resource", http.StatusFailedDependency)
		return
	}
	if err != nil {
		http.Error(w, "credential service unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	// A leased-but-empty private key cannot dial — reject pre-upgrade rather than
	// fail late at the SSH dial (restores the non-empty-key contract LoadTarget
	// enforced before the coords/lease split, T-030a fixup).
	if mat.PrivateKey == "" {
		http.Error(w, "leased credential incomplete", http.StatusFailedDependency)
		return
	}
	target.PrivateKey = mat.PrivateKey
	target.Passphrase = mat.Passphrase

	// --- WS upgrade + cast recording -----------------------------------------
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer c.CloseNow()

	castPath := filepath.Join(b.castDir, id+".cast")
	rec, err := cast.NewWriter(castPath, 80, 24)
	if err != nil {
		// Recording is a compliance requirement: fail the session closed rather
		// than broker an unrecorded session (O_EXCL also rejects a duplicate
		// channel for the same session at v0.1.0).
		_ = c.Close(websocket.StatusInternalError, "recording unavailable")
		log.Printf("session %s: cast open failed: %v", id, err)
		return
	}

	// --- SSH dial (pinned host key) -------------------------------------------
	conn, err := sshpty.Dial(sshpty.Config{
		Host: target.Host, Port: target.Port, User: target.User,
		HostKey: target.HostKey, PrivateKey: target.PrivateKey, Passphrase: target.Passphrase,
		Cols: 80, Rows: 24,
	})
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "ssh dial failed")
		log.Printf("session %s: ssh dial failed: %v", id, err)
		if sha, cerr := rec.Close(); cerr == nil {
			// Record the failed open as a closed session (broker_crash) so it is
			// not left dangling.
			_ = b.store.Close(context.Background(), id, operator, "broker_crash", castPath, sha)
		}
		return
	}
	defer func() { _ = conn.Close() }()

	// Mark active + emit session.open (only when transitioning from pending; a
	// reconnect to an already-active session skips re-emission).
	if sess.Status == "pending" {
		if err := b.store.Activate(ctx, id, operator); err != nil {
			_ = c.Close(websocket.StatusInternalError, "activate failed")
			log.Printf("session %s: activate failed: %v", id, err)
			return
		}
	}

	// --- pipe until close, then record session.close --------------------------
	// Per-line audit (ADR-0048 §6): each Enter-delimited line is hashed into the
	// chain (terminal.input.line); the raw bytes stay in the cast (T-019a). Fires
	// once per Enter, so the in-line outbox write is off the keystroke path.
	onInputLine := func(line []byte) {
		sum := sha256.Sum256(line)
		if err := b.store.RecordInputLine(context.Background(), id, operator, hex.EncodeToString(sum[:])); err != nil {
			log.Printf("session %s: input-line audit failed: %v", id, err)
		}
	}
	reason := conn.Pipe(ctx, c, castRecorder{rec}, b.idle, onInputLine)
	sha, _ := rec.Close()
	// Use a fresh context: r.Context() is canceled once the handler returns /
	// the client disconnects, but the close evidence must still be written.
	if err := b.store.Close(context.Background(), id, operator, reason, castPath, sha); err != nil {
		log.Printf("session %s: close bookkeeping failed: %v", id, err)
	}
	_ = c.Close(websocket.StatusNormalClosure, reason)
}

// castRecorder adapts *cast.Writer to sshpty.Recorder.
type castRecorder struct{ w *cast.Writer }

func (c castRecorder) Record(input bool, data []byte) error {
	s := cast.Output
	if input {
		s = cast.Input
	}
	return c.w.Record(s, data)
}
