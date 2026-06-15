package server_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/session-broker/internal/connector"
	"aireos.local/services/session-broker/internal/server"
	"aireos.local/services/session-broker/internal/sshpty"
	"aireos.local/services/session-broker/internal/store"
)

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "tester", Edge: "agent-remote-session"}, nil
}

// allowAllService stands in for the gateway's service-service token (audience
// session-broker) on the exec plane.
type allowAllService struct{}

func (allowAllService) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "svc:gateway", Edge: "service-service"}, nil
}

// fakeStore drives the handlers' authz/validation branches without a database or
// a live SSH target. It captures the last RecordExec for the exec tests.
type fakeStore struct {
	sess      store.Session
	sessErr   error
	target    store.Target
	targetErr error

	execCalled     bool
	execInvocation string
	execActor      string
	execOutcome    string
	execSHA        string
}

func (f *fakeStore) LoadSession(_ context.Context, _ string) (store.Session, error) {
	return f.sess, f.sessErr
}
func (f *fakeStore) LoadTargetCoords(_ context.Context, _ string) (store.Target, error) {
	return f.target, f.targetErr
}
func (f *fakeStore) Activate(_ context.Context, _, _ string) error           { return nil }
func (f *fakeStore) Close(_ context.Context, _, _, _, _, _ string) error     { return nil }
func (f *fakeStore) RecordInputLine(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeStore) RecordExec(_ context.Context, inv, actor, outcome, sha string) error {
	f.execCalled = true
	f.execInvocation, f.execActor, f.execOutcome, f.execSHA = inv, actor, outcome, sha
	return nil
}

// fakeLeaser stands in for connector-service. The default returns ErrNoCredential
// so an unconfigured test stops at 424 rather than attempting a live SSH dial.
type fakeLeaser struct {
	mat connector.Material
	err error
}

func (f *fakeLeaser) Lease(_ context.Context, _, _, _ string) (connector.Material, error) {
	return f.mat, f.err
}

func newHandler(t *testing.T, cfg server.Config) http.Handler {
	t.Helper()
	if cfg.Verifier == nil {
		cfg.Verifier = allowAll{}
	}
	if cfg.Leaser == nil {
		cfg.Leaser = &fakeLeaser{err: connector.ErrNoCredential}
	}
	if cfg.CastDir == "" {
		cfg.CastDir = t.TempDir()
	}
	return server.New(cfg)
}

func wsURL(httpURL, path string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + path
}

// dialStatus dials the channel and returns the HTTP status of a non-101 response
// (the pre-upgrade gates answer with a plain HTTP status, no WS upgrade).
func dialStatus(t *testing.T, h http.Handler, bearer bool) int {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{}
	if bearer {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer test"}}
	}
	c, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/v1/sessions/abc/channel"), opts)
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("expected a non-101 response from a gated/invalid channel dial")
	}
	if resp == nil {
		t.Fatalf("no HTTP response: %v", err)
	}
	return resp.StatusCode
}

func TestChannelRequiresCapability(t *testing.T) {
	h := newHandler(t, server.Config{Store: &fakeStore{}})
	if got := dialStatus(t, h, false); got != http.StatusUnauthorized {
		t.Fatalf("no bearer: want 401, got %d", got)
	}
}

func TestChannelUnknownSession(t *testing.T) {
	h := newHandler(t, server.Config{Store: &fakeStore{sessErr: store.ErrSessionNotFound}})
	if got := dialStatus(t, h, true); got != http.StatusNotFound {
		t.Fatalf("unknown session: want 404, got %d", got)
	}
}

func TestChannelForeignOperatorIsNotFound(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{sess: store.Session{ID: "abc", OperatorID: "someone-else", Status: "pending"}},
	})
	if got := dialStatus(t, h, true); got != http.StatusNotFound {
		t.Fatalf("foreign operator: want 404 (not disclosed), got %d", got)
	}
}

func TestChannelNotOpen(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{sess: store.Session{ID: "abc", OperatorID: "tester", Status: "closed"}},
	})
	if got := dialStatus(t, h, true); got != http.StatusConflict {
		t.Fatalf("closed session: want 409, got %d", got)
	}
}

func TestChannelTargetUnavailable(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{
			sess:      store.Session{ID: "abc", OperatorID: "tester", Status: "pending"},
			targetErr: store.ErrTargetIncomplete,
		},
	})
	if got := dialStatus(t, h, true); got != http.StatusFailedDependency {
		t.Fatalf("target coords unavailable: want 424, got %d", got)
	}
}

// TestChannelCredentialUnavailable — coords load fine, but connector-service has
// no credential for the resource (lease 404 → ErrNoCredential). The session
// cannot be brokered: 424 (the 503-when-key-unset now lives on connector, T-030).
func TestChannelCredentialUnavailable(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{
			sess:   store.Session{ID: "abc", OperatorID: "tester", Status: "pending"},
			target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"},
		},
		Leaser: &fakeLeaser{err: connector.ErrNoCredential},
	})
	if got := dialStatus(t, h, true); got != http.StatusFailedDependency {
		t.Fatalf("credential unavailable: want 424, got %d", got)
	}
}

// TestChannelConnectorUnavailable — coords load fine, but the lease RPC fails for
// a non-404 reason (connector down / transport error). That is an upstream
// outage, distinct from an unprovisioned resource: 502 Bad Gateway (T-030a fixup;
// same convention as the api audit read-proxy).
func TestChannelConnectorUnavailable(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{
			sess:   store.Session{ID: "abc", OperatorID: "tester", Status: "pending"},
			target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"},
		},
		Leaser: &fakeLeaser{err: errors.New("dial tcp connector:8083: connection refused")},
	})
	if got := dialStatus(t, h, true); got != http.StatusBadGateway {
		t.Fatalf("connector down: want 502, got %d", got)
	}
}

// TestChannelLeasedKeyEmpty — connector returns a credential with an empty private
// key. It cannot dial, so reject pre-upgrade (424) rather than fail late at the
// SSH dial (T-030a fixup restores the non-empty-key contract).
func TestChannelLeasedKeyEmpty(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{
			sess:   store.Session{ID: "abc", OperatorID: "tester", Status: "pending"},
			target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"},
		},
		Leaser: &fakeLeaser{mat: connector.Material{PrivateKey: ""}},
	})
	if got := dialStatus(t, h, true); got != http.StatusFailedDependency {
		t.Fatalf("empty leased key: want 424, got %d", got)
	}
}

// TestChannelCookieAuthenticates — the browser WS-upgrade case: only the
// aireos_session cookie is sent (no Authorization header, which the WebSocket
// API cannot set). CookieToAuthHeader must translate it so RequireCapability
// passes; reaching the target-unavailable branch (424) proves auth succeeded.
func TestChannelCookieAuthenticates(t *testing.T) {
	h := newHandler(t, server.Config{
		Store: &fakeStore{
			sess:      store.Session{ID: "abc", OperatorID: "tester", Status: "pending"},
			targetErr: store.ErrTargetIncomplete,
		},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/v1/sessions/abc/channel"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{mw.SessionCookieName + "=tok"}},
	})
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("expected a non-101 response (target unavailable), got an upgrade")
	}
	if resp == nil || resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("cookie-only auth: want 424 (authenticated, target unavailable), got %v", resp)
	}
}

// /healthz is served over HTTP/2 (cleartext via h2c), per ADR-0043 §Verification.
func TestHTTP2Healthz(t *testing.T) {
	h := h2c.NewHandler(newHandler(t, server.Config{Store: &fakeStore{}}), &http2.Server{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}}
	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("want HTTP/2 (ProtoMajor 2), got %d", resp.ProtoMajor)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// --- exec plane (T-038 / ADR-0058) ------------------------------------------

func execHandler(t *testing.T, st *fakeStore, leaser server.CredentialLeaser, fn server.ExecFunc, dir string) http.Handler {
	t.Helper()
	if leaser == nil {
		leaser = &fakeLeaser{mat: connector.Material{PrivateKey: "k"}}
	}
	return server.New(server.Config{
		Verifier:     allowAll{},
		ExecVerifier: allowAllService{},
		Store:        st,
		Leaser:       leaser,
		CastDir:      dir,
		ExecFn:       fn,
	})
}

func postExec(t *testing.T, h http.Handler, body string) *http.Response {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/exec", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer svc")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post exec: %v", err)
	}
	return resp
}

func TestExecHappyPathAuditsAndRetains(t *testing.T) {
	dir := t.TempDir()
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	fn := func(_ sshpty.Config, _ string, _ int, _ time.Duration) (sshpty.ExecResult, error) {
		return sshpty.ExecResult{Stdout: []byte("hello\n"), ExitCode: 0}, nil
	}
	resp := postExec(t, execHandler(t, st, nil, fn, dir),
		`{"resourceId":"r","command":"echo hello","invocationId":"inv-1","subject":"agent:wedge"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var out struct {
		Stdout    string `json:"stdout"`
		ExitCode  int    `json:"exitCode"`
		Truncated bool   `json:"truncated"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out.Stdout != "hello\n" || out.ExitCode != 0 || out.Truncated {
		t.Fatalf("response = %+v", out)
	}
	if !st.execCalled || st.execInvocation != "inv-1" || st.execActor != "agent:wedge" || st.execOutcome != "remote.exec" {
		t.Fatalf("audit fields wrong: %+v", st)
	}
	sum := sha256.Sum256([]byte("hello\n"))
	if st.execSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("audit SHA = %q, want stdout sha", st.execSHA)
	}
	b, err := os.ReadFile(filepath.Join(dir, "inv-1.exec"))
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("artifact = %q err=%v", b, err)
	}
}

func TestExecNonZeroExitOutcome(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "ssh-ed25519 AAAA"}}
	fn := func(_ sshpty.Config, _ string, _ int, _ time.Duration) (sshpty.ExecResult, error) {
		return sshpty.ExecResult{Stdout: []byte("nope"), ExitCode: 2}, nil
	}
	resp := postExec(t, execHandler(t, st, nil, fn, t.TempDir()),
		`{"resourceId":"r","command":"false","invocationId":"inv-2","subject":"agent:x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if st.execOutcome != "remote.exec:error" {
		t.Fatalf("outcome = %q, want remote.exec:error", st.execOutcome)
	}
}

func TestExecValidation(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
	fn := func(_ sshpty.Config, _ string, _ int, _ time.Duration) (sshpty.ExecResult, error) {
		return sshpty.ExecResult{}, nil
	}
	resp := postExec(t, execHandler(t, st, nil, fn, t.TempDir()), `{"resourceId":"r","command":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("validation: status = %d, want 400", resp.StatusCode)
	}
}

func TestExecUnprovisionedIs424(t *testing.T) {
	st := &fakeStore{target: store.Target{Host: "h", User: "u", HostKey: "k"}}
	fn := func(_ sshpty.Config, _ string, _ int, _ time.Duration) (sshpty.ExecResult, error) {
		return sshpty.ExecResult{}, nil
	}
	resp := postExec(t, execHandler(t, st, &fakeLeaser{err: connector.ErrNoCredential}, fn, t.TempDir()),
		`{"resourceId":"r","command":"x","invocationId":"i","subject":"a"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFailedDependency {
		t.Fatalf("unprovisioned: status = %d, want 424", resp.StatusCode)
	}
	if st.execCalled {
		t.Fatal("no exec event should be recorded when the lease fails")
	}
}

func TestExecRouteAbsentWithoutVerifier(t *testing.T) {
	h := server.New(server.Config{Verifier: allowAll{}, Store: &fakeStore{}, CastDir: t.TempDir()})
	resp := postExec(t, h, `{"resourceId":"r","command":"x","invocationId":"i","subject":"a"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("no exec verifier: status = %d, want 404", resp.StatusCode)
	}
}
