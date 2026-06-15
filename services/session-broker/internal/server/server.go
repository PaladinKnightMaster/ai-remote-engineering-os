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
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
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

// SessionStore is the narrow slice of *store.Store the handlers need.
type SessionStore interface {
	LoadSession(ctx context.Context, id string) (store.Session, error)
	LoadTargetCoords(ctx context.Context, resourceID string) (store.Target, error)
	Activate(ctx context.Context, sessionID, operatorID string) error
	Close(ctx context.Context, sessionID, operatorID, reason, castPath, castSHA string) error
	RecordInputLine(ctx context.Context, sessionID, operatorID, lineSHA string) error
	// RecordExec audits a non-interactive exec egress (ADR-0058; stateless).
	RecordExec(ctx context.Context, invocationID, actor, outcome, stdoutSHA string) error
}

// CredentialLeaser leases the decrypted SSH credential from connector-service
// (T-030). The broker no longer holds AIREOS_RESOURCE_SECRET_KEY.
type CredentialLeaser interface {
	Lease(ctx context.Context, resourceID, sessionID, operatorSubject string) (connector.Material, error)
}

// ExecFunc is the SSH-exec seam (default sshpty.Exec); injectable so the handler
// is testable without a live target.
type ExecFunc func(cfg sshpty.Config, command string, maxBytes int, timeout time.Duration) (sshpty.ExecResult, error)

// Config wires the broker handler's dependencies.
type Config struct {
	// Verifier gates the operator WS plane (audience "api", ADR-0050); ExecVerifier
	// gates the service exec plane (audience "session-broker", ADR-0058). A nil
	// ExecVerifier disables the /v1/exec route.
	Verifier     mw.Verifier
	ExecVerifier mw.Verifier
	Store        SessionStore
	Leaser       CredentialLeaser // connector-service credential lease (T-030)
	CastDir      string           // directory for <sessionID>.cast + <invocationId>.exec
	IdleTimeout  time.Duration    // input-inactivity close (ADR-0048 §8; default 30m)
	ExecMaxBytes int              // exec output cap (PERF-1; default 1 MiB)
	ExecTimeout  time.Duration    // exec command timeout (PERF-1; default 60s)
	ExecFn       ExecFunc         // SSH-exec seam (default sshpty.Exec)
}

type broker struct {
	store       SessionStore
	leaser      CredentialLeaser
	castDir     string
	idle        time.Duration
	execFn      ExecFunc
	execMax     int
	execTimeout time.Duration
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
	execFn := cfg.ExecFn
	if execFn == nil {
		execFn = sshpty.Exec
	}
	execMax := cfg.ExecMaxBytes
	if execMax <= 0 {
		execMax = 1 << 20 // 1 MiB
	}
	execTimeout := cfg.ExecTimeout
	if execTimeout <= 0 {
		execTimeout = 60 * time.Second
	}
	b := &broker{
		store: cfg.Store, leaser: cfg.Leaser, castDir: cfg.CastDir, idle: idle,
		execFn: execFn, execMax: execMax, execTimeout: execTimeout,
	}
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
	// Service exec plane (T-038 / ADR-0058): the gateway calls POST /v1/exec with a
	// service-service token of audience "session-broker" — a SEPARATE verifier from
	// the operator WS plane's audience "api" (ADR-0050). Bearer only (no cookie/CSRF
	// — not a browser surface). Absent ExecVerifier ⇒ no exec route.
	if cfg.ExecVerifier != nil {
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireCapability(cfg.ExecVerifier, "service-service"))
			r.Post("/v1/exec", b.exec)
		})
	}
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

type execRequest struct {
	ResourceID   string `json:"resourceId"`
	Command      string `json:"command"`
	InvocationID string `json:"invocationId"`
	Subject      string `json:"subject"` // the agent identity (gateway-asserted; audit actor)
}

type execResponse struct {
	Stdout    string `json:"stdout"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

// exec (POST /v1/exec) runs one non-interactive command on a governed resource for
// the gateway's remote.exec tool (ADR-0058). Stateless: load coords → lease the
// credential from connector → run (bounded + timeout) → retain the stdout artifact
// + audit (Target invocation:<id>, Evidence sha256:<stdout>) → respond. No sessions
// row. Failure modes mirror the WS channel: unprovisioned → 424, connector down →
// 502, leased-empty key → 424.
func (b *broker) exec(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req execRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.ResourceID == "" || req.Command == "" || req.InvocationID == "" || req.Subject == "" {
		http.Error(w, "resourceId, command, invocationId and subject are required", http.StatusBadRequest)
		return
	}

	target, err := b.store.LoadTargetCoords(ctx, req.ResourceID)
	if err != nil {
		http.Error(w, "target unavailable: "+err.Error(), http.StatusFailedDependency)
		return
	}
	mat, err := b.leaser.Lease(ctx, req.ResourceID, req.InvocationID, req.Subject)
	if errors.Is(err, connector.ErrNoCredential) {
		http.Error(w, "no credential provisioned for resource", http.StatusFailedDependency)
		return
	}
	if err != nil {
		http.Error(w, "credential service unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	if mat.PrivateKey == "" {
		http.Error(w, "leased credential incomplete", http.StatusFailedDependency)
		return
	}

	res, err := b.execFn(sshpty.Config{
		Host: target.Host, Port: target.Port, User: target.User, HostKey: target.HostKey,
		PrivateKey: mat.PrivateKey, Passphrase: mat.Passphrase,
	}, req.Command, b.execMax, b.execTimeout)
	if err != nil {
		// Dial/transport failure or timeout — no completed run, no artifact.
		http.Error(w, "exec failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Retain the captured stdout as an access-controlled artifact + anchor its
	// SHA256 (ADR-0049 bytes-in-cast parity; ADR-0058).
	sum := sha256.Sum256(res.Stdout)
	sha := hex.EncodeToString(sum[:])
	artifactPath := filepath.Join(b.castDir, req.InvocationID+".exec")
	if werr := os.WriteFile(artifactPath, res.Stdout, 0o600); werr != nil {
		log.Printf("exec %s: artifact write failed: %v", req.InvocationID, werr)
	}
	outcome := "remote.exec"
	if res.ExitCode != 0 {
		outcome = "remote.exec:error"
	}
	if aerr := b.store.RecordExec(ctx, req.InvocationID, req.Subject, outcome, sha); aerr != nil {
		log.Printf("exec %s: audit enqueue failed: %v", req.InvocationID, aerr)
	}

	writeJSON(w, http.StatusOK, execResponse{
		Stdout: string(res.Stdout), ExitCode: res.ExitCode, Truncated: res.Truncated,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
