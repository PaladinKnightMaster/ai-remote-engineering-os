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
	"encoding/base64"
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

// FileReadFunc / FileWriteFunc / FileListFunc are the SFTP seams (defaults
// sshpty.ReadFile / sshpty.WriteFile / sshpty.ListDir; T-139a), injectable for the
// same reason ExecFunc is: the handler is testable without a live target.
type (
	FileReadFunc  func(cfg sshpty.Config, path string, maxBytes int64, timeout time.Duration) ([]byte, error)
	FileWriteFunc func(cfg sshpty.Config, path string, data []byte, mode os.FileMode, maxBytes int64, timeout time.Duration) error
	FileListFunc  func(cfg sshpty.Config, path string, maxEntries int, timeout time.Duration) ([]sshpty.FileEntry, error)
)

// Config wires the broker handler's dependencies.
type Config struct {
	// Verifier gates the operator WS plane (audience "api", ADR-0050); ExecVerifier
	// gates the service exec plane (audience "session-broker", ADR-0058); FileVerifier
	// gates the service file plane (same audience, ADR-0071 §D4) — a SEPARATE field
	// from ExecVerifier (even though main.go wires the same underlying verifier to
	// both today) so /v1/exec and /v1/file can be enabled independently. A nil
	// ExecVerifier/FileVerifier disables its route.
	Verifier     mw.Verifier
	ExecVerifier mw.Verifier
	FileVerifier mw.Verifier
	Store        SessionStore
	Leaser       CredentialLeaser // connector-service credential lease (T-030)
	CastDir      string           // directory for <sessionID>.cast, <invocationId>.exec, <invocationId>.file
	IdleTimeout  time.Duration    // input-inactivity close (ADR-0048 §8; default 30m)
	ExecMaxBytes int              // exec output cap (PERF-1; default 1 MiB)
	ExecTimeout  time.Duration    // exec command timeout (PERF-1; default 60s)
	ExecFn       ExecFunc         // SSH-exec seam (default sshpty.Exec)

	// File plane (T-139b / ADR-0071 §D4/D5).
	FileMaxBytes int // file content cap, enforced both directions (PERF-17; default 1 MiB)
	// FileTimeout bounds each SFTP operation (PERF-19; default 60s, mirrors ExecTimeout).
	FileTimeout time.Duration
	// FileMaxEntries bounds ListDir (D5's refuse-don't-truncate rule applied to entry
	// count; default 1000 — no ADR-stated number, chosen as a resource-safety bound).
	FileMaxEntries int
	// FileDialTimeout is threaded into sshpty.Config.DialTimeout for every file
	// operation (default 10s, matching sshpty's own internal default — ADR-0071 audit
	// finding: withSFTP dials OUTSIDE FileTimeout, so an unbounded DialTimeout is the
	// only thing standing between an unreachable/hanging target and a stuck request;
	// this makes that bound explicit at the call site rather than relying on it being
	// implicit deep in sshpty.dialClient).
	FileDialTimeout time.Duration
	ReadFileFn      FileReadFunc  // SFTP read seam (default sshpty.ReadFile)
	WriteFileFn     FileWriteFunc // SFTP write seam (default sshpty.WriteFile)
	ListDirFn       FileListFunc  // SFTP list seam (default sshpty.ListDir)
}

type broker struct {
	store       SessionStore
	leaser      CredentialLeaser
	castDir     string
	idle        time.Duration
	execFn      ExecFunc
	execMax     int
	execTimeout time.Duration

	fileMax         int
	fileTimeout     time.Duration
	fileMaxEntries  int
	fileDialTimeout time.Duration
	fileBodyMax     int64
	readFileFn      FileReadFunc
	writeFileFn     FileWriteFunc
	listDirFn       FileListFunc
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
	fileMax := cfg.FileMaxBytes
	if fileMax <= 0 {
		fileMax = 1 << 20 // 1 MiB, ADR-0071 §D5
	}
	fileTimeout := cfg.FileTimeout
	if fileTimeout <= 0 {
		fileTimeout = 60 * time.Second
	}
	fileMaxEntries := cfg.FileMaxEntries
	if fileMaxEntries <= 0 {
		fileMaxEntries = 1000
	}
	fileDialTimeout := cfg.FileDialTimeout
	if fileDialTimeout <= 0 {
		fileDialTimeout = 10 * time.Second
	}
	readFileFn := cfg.ReadFileFn
	if readFileFn == nil {
		readFileFn = sshpty.ReadFile
	}
	writeFileFn := cfg.WriteFileFn
	if writeFileFn == nil {
		writeFileFn = sshpty.WriteFile
	}
	listDirFn := cfg.ListDirFn
	if listDirFn == nil {
		listDirFn = sshpty.ListDir
	}
	b := &broker{
		store: cfg.Store, leaser: cfg.Leaser, castDir: cfg.CastDir, idle: idle,
		execFn: execFn, execMax: execMax, execTimeout: execTimeout,
		fileMax: fileMax, fileTimeout: fileTimeout, fileMaxEntries: fileMaxEntries,
		fileDialTimeout: fileDialTimeout, fileBodyMax: fileBodyLimit(fileMax),
		readFileFn: readFileFn, writeFileFn: writeFileFn, listDirFn: listDirFn,
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
	// Service file plane (T-139b / ADR-0071 §D4): sibling to /v1/exec, same
	// service-service edge and subject pin, gated by its own verifier so it can be
	// enabled independently of exec. Absent FileVerifier ⇒ no file route.
	if cfg.FileVerifier != nil {
		r.Group(func(r chi.Router) {
			r.Use(mw.RequireCapability(cfg.FileVerifier, "service-service"))
			r.Post("/v1/file", b.file)
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
	// R5-ext (PIN-1, ADR-0060): the exec plane is mcp-gateway-only — reject any other
	// workload's audience-matched service token (defense in depth over the
	// service-service edge; mirrors the gateway's onlyAPI pin). The gateway is exec's
	// sole caller (ADR-0058).
	if capb, ok := mw.CapabilityFromContext(ctx); !ok || capb.Subject != "mcp-gateway" {
		http.Error(w, "exec plane is mcp-gateway-only", http.StatusForbidden)
		return
	}
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

// --- file plane (T-139b / ADR-0071 §D4/D5) ----------------------------------

const (
	fileOpRead  = "read"
	fileOpWrite = "write"
	fileOpList  = "list"
)

// fileEnvelopeHeadroom covers everything in the /v1/file JSON envelope besides
// the base64-encoded content field — op, resourceId, path, mode, invocationId,
// subject, JSON punctuation — plus slack so a payload MODESTLY over the content
// cap still reaches the application-level cap check below and gets a clean 413,
// rather than being silently swallowed by the transport limit into a misleading
// 400 (see fileBodyLimit). It is deliberately generous relative to the envelope
// fields' actual size, not tight.
const fileEnvelopeHeadroom = 64 * 1024 // 64 KiB

// fileBodyLimit derives the /v1/file body-decode cap from maxContentBytes rather
// than hard-coding a second literal. Content travels JSON-embedded as a []byte
// field, which encoding/json marshals/unmarshals via base64.StdEncoding — exactly
// the same expansion factor base64.StdEncoding.EncodedLen computes. A file
// exactly at maxContentBytes must decode successfully: the finding this fixes is
// that exec's flat io.LimitReader(r.Body, 1<<20) would reject a file exactly at
// the documented 1 MiB cap (whose base64 form is ~1.37 MiB) with a misleading
// "invalid body" 400 before the real cap check ever ran.
func fileBodyLimit(maxContentBytes int) int64 {
	return int64(base64.StdEncoding.EncodedLen(maxContentBytes)) + fileEnvelopeHeadroom
}

type fileRequest struct {
	// Op selects the operation: "read", "write" or "list" (ADR-0071 D1's three
	// tools, one broker leg).
	Op           string  `json:"op"`
	ResourceID   string  `json:"resourceId"`
	Path         string  `json:"path"`
	Content      []byte  `json:"content,omitempty"` // write only; base64 in JSON
	Mode         *uint32 `json:"mode,omitempty"`    // write only; POSIX perm bits (e.g. 0644); default 0644
	InvocationID string  `json:"invocationId"`
	Subject      string  `json:"subject"` // the agent identity (gateway-asserted; audit actor)
}

type fileResponse struct {
	Content []byte             `json:"content,omitempty"` // read only
	SHA256  string             `json:"sha256,omitempty"`  // read/write: digest of the transferred bytes
	Entries []sshpty.FileEntry `json:"entries,omitempty"` // list only
}

// file (POST /v1/file) performs one bounded file operation on a governed
// resource for the gateway's remote.file.{read,write,list} tools (ADR-0071 §D4).
// Mirrors exec's shape exactly: R5-ext subject pin → decode/validate → load
// coords → lease the credential from connector → perform the bounded SFTP
// operation (T-139a) → retain the artifact + audit (Target invocation:<id>,
// Evidence sha256:<bytes>) → respond. Failure modes mirror exec through the
// lease: unprovisioned → 424, connector down → 502, leased-empty key → 424;
// beyond the lease, sshpty's typed sentinels (T-139a) map to distinct statuses
// via mapFileError so a caller can tell "denied" from "too big" from "timed out".
func (b *broker) file(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// R5-ext (PIN-1, ADR-0060 / ADR-0071 §D4, SEC-51): the file plane is
	// mcp-gateway-only, identical to exec's pin.
	if capb, ok := mw.CapabilityFromContext(ctx); !ok || capb.Subject != "mcp-gateway" {
		http.Error(w, "file plane is mcp-gateway-only", http.StatusForbidden)
		return
	}
	var req fileRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, b.fileBodyMax)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.ResourceID == "" || req.Path == "" || req.InvocationID == "" || req.Subject == "" {
		http.Error(w, "resourceId, path, invocationId and subject are required", http.StatusBadRequest)
		return
	}
	if req.Op != fileOpRead && req.Op != fileOpWrite && req.Op != fileOpList {
		http.Error(w, `op must be "read", "write" or "list"`, http.StatusBadRequest)
		return
	}
	// Application-level cap check (PERF-17/18, ADR-0071 §D5), ahead of the coords
	// load and lease: a write over the configured cap is refused before any
	// credential is leased or any bytes touch the wire to the target, and — the
	// point of fileBodyLimit's headroom above — is distinguishable from a
	// transport-level body-too-large 400.
	if req.Op == fileOpWrite && len(req.Content) > b.fileMax {
		http.Error(w, "content exceeds the configured file size cap", http.StatusRequestEntityTooLarge)
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

	cfg := sshpty.Config{
		Host: target.Host, Port: target.Port, User: target.User, HostKey: target.HostKey,
		PrivateKey: mat.PrivateKey, Passphrase: mat.Passphrase,
		DialTimeout: b.fileDialTimeout,
	}
	mode := os.FileMode(0o644)
	if req.Mode != nil {
		mode = os.FileMode(*req.Mode)
	}

	// evidence is the exact bytes whose sha256 is both the audit digest and the
	// retained artifact — never logged, never placed in the audit chain itself
	// (AUD-42: only the digest crosses into the chain, via RecordExec's
	// EvidencePacketID).
	var (
		respContent []byte
		entries     []sshpty.FileEntry
		evidence    []byte
		opErr       error
	)
	switch req.Op {
	case fileOpRead:
		respContent, opErr = b.readFileFn(cfg, req.Path, int64(b.fileMax), b.fileTimeout)
		evidence = respContent
	case fileOpWrite:
		opErr = b.writeFileFn(cfg, req.Path, req.Content, mode, int64(b.fileMax), b.fileTimeout)
		evidence = req.Content
	case fileOpList:
		entries, opErr = b.listDirFn(cfg, req.Path, b.fileMaxEntries, b.fileTimeout)
		if opErr == nil {
			// A listing has no "file bytes" of its own; its canonical JSON encoding
			// stands in as the transferred content an evidence digest binds to, so
			// AUD-41's per-invocation evidence anchor applies uniformly to all three
			// operations rather than being read/write-only.
			evidence, _ = json.Marshal(entries)
		}
	}

	outcome := "remote.file." + req.Op
	var sha string
	if opErr == nil {
		sum := sha256.Sum256(evidence)
		sha = hex.EncodeToString(sum[:])
		artifactPath := filepath.Join(b.castDir, req.InvocationID+".file")
		if werr := os.WriteFile(artifactPath, evidence, 0o600); werr != nil {
			log.Printf("file %s: artifact write failed: %v", req.InvocationID, werr)
		}
	} else {
		outcome += ":error"
	}
	if aerr := b.store.RecordExec(ctx, req.InvocationID, req.Subject, outcome, sha); aerr != nil {
		log.Printf("file %s: audit enqueue failed: %v", req.InvocationID, aerr)
	}

	if opErr != nil {
		http.Error(w, "file operation failed: "+opErr.Error(), mapFileError(opErr))
		return
	}
	writeJSON(w, http.StatusOK, fileResponse{Content: respContent, SHA256: sha, Entries: entries})
}

// mapFileError maps sshpty's typed sentinels (T-139a) to distinct HTTP statuses
// so a caller can act on WHY an operation failed, not just that it did
// (ADR-0071 §D4/D5). Order matters only in that each case is a distinct
// sentinel; errors.Is unwraps mapSFTPError's/readCapped's %w wrapping.
func mapFileError(err error) int {
	switch {
	case errors.Is(err, sshpty.ErrFileNotFound):
		return http.StatusNotFound
	case errors.Is(err, sshpty.ErrFileDenied):
		return http.StatusForbidden
	case errors.Is(err, sshpty.ErrFileOverCap):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, sshpty.ErrFileTimeout):
		return http.StatusGatewayTimeout
	case errors.Is(err, sshpty.ErrFileIsADir), errors.Is(err, sshpty.ErrFileNotADir):
		return http.StatusUnprocessableEntity
	case errors.Is(err, sshpty.ErrFileUnsupportedRename):
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
