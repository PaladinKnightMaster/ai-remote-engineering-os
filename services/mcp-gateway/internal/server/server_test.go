package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/mcp-gateway/internal/brokerfile"
	"aireos.local/services/mcp-gateway/internal/catalog"
	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/store"
)

// emptyCatalog mirrors the main package's T-035 skeleton catalog: zero tools, an
// unimplemented Invoke. The governed catalog lands in T-037/T-039.
type emptyCatalog struct{}

func (emptyCatalog) List() []mcp.Tool { return nil }
func (emptyCatalog) Invoke(context.Context, string, json.RawMessage) (mcp.ToolResult, error) {
	return mcp.ToolResult{}, errors.New("no tools available yet")
}

func newTestHandler(t *testing.T) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	return New(Config{Verifier: v, Catalog: emptyCatalog{}}), auth.NewSigner(key, "")
}

func agentToken(t *testing.T, s *auth.Signer) string {
	t.Helper()
	tok, err := s.Issue(auth.IssueParams{Subject: "agent:test", Edge: "agent-tool", Audience: "mcp-gateway", TTL: auth.AgentTTL})
	if err != nil {
		t.Fatalf("mint agent token: %v", err)
	}
	return tok
}

func postMCP(t *testing.T, h http.Handler, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) rpcResponse {
	t.Helper()
	var resp rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rpc response: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

func TestHealthz(t *testing.T) {
	h, _ := newTestHandler(t)
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
}

func TestMCPRequiresAgentToolEdge(t *testing.T) {
	h, s := newTestHandler(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	// No token → 401.
	if rec := postMCP(t, h, body, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	// Valid token, wrong edge (service-service) → 403.
	svcTok, _ := s.IssueService("svc", "mcp-gateway")
	if rec := postMCP(t, h, body, svcTok); rec.Code != http.StatusForbidden {
		t.Fatalf("service-service token: status = %d, want 403", rec.Code)
	}
	// agent-tool token but wrong audience → 401 (verifier rejects).
	wrongAud, _ := s.Issue(auth.IssueParams{Subject: "a", Edge: "agent-tool", Audience: "api", TTL: auth.AgentTTL})
	if rec := postMCP(t, h, body, wrongAud); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-audience token: status = %d, want 401", rec.Code)
	}
}

func TestInitializeHandshake(t *testing.T) {
	h, s := newTestHandler(t)
	rec := postMCP(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, agentToken(t, s))
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", rec.Code)
	}
	resp := decodeRPC(t, rec)
	result, _ := resp.Result.(map[string]any)
	if result["protocolVersion"] != mcp.ProtocolVersion {
		t.Fatalf("initialize protocolVersion = %v, want %q", result["protocolVersion"], mcp.ProtocolVersion)
	}
}

func TestToolsListEmpty(t *testing.T) {
	h, s := newTestHandler(t)
	rec := postMCP(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, agentToken(t, s))
	resp := decodeRPC(t, rec)
	result, _ := resp.Result.(map[string]any)
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) != 0 {
		t.Fatalf("tools/list = %v, want empty array", result["tools"])
	}
}

func TestToolsCallUnknownToolErrors(t *testing.T) {
	h, s := newTestHandler(t)
	rec := postMCP(t, h, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"remote.exec","arguments":{}}}`, agentToken(t, s))
	resp := decodeRPC(t, rec)
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("tools/call (no governed tools yet) = %+v, want invalidParams error", resp.Error)
	}
}

// An oversized request body is rejected (MaxBytesReader) rather than buffered whole.
func TestMCPRequestBodyCapped(t *testing.T) {
	h, s := newTestHandler(t)
	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":"` + strings.Repeat("A", (1<<20)+64) + `"}`
	resp := decodeRPC(t, postMCP(t, h, huge, agentToken(t, s)))
	if resp.Error == nil {
		t.Fatal("an oversized /mcp body must produce a JSON-RPC error (body cap)")
	}
}

func TestUnknownMethod(t *testing.T) {
	h, s := newTestHandler(t)
	rec := postMCP(t, h, `{"jsonrpc":"2.0","id":4,"method":"does/not/exist"}`, agentToken(t, s))
	resp := decodeRPC(t, rec)
	if resp.Error == nil || resp.Error.Code != codeMethodNotFound {
		t.Fatalf("unknown method = %+v, want methodNotFound error", resp.Error)
	}
}

func TestNotificationGets202(t *testing.T) {
	h, s := newTestHandler(t)
	rec := postMCP(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, agentToken(t, s))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("notification body = %q, want empty", rec.Body.String())
	}
}

// guard: emptyCatalog satisfies mcp.Catalog (compile-time).
var _ mcp.Catalog = emptyCatalog{}

// --- governed Invoke (T-039b) -----------------------------------------------
// Driven through the real verifier so the gate stashes the agent capability +
// scope (which Invoke reads); policy/broker/store are faked.

type fakeStore struct {
	inFlight         int
	requiresApproval bool
	requiresErr      error
	idempotentHit    bool
	createStatus     string // status of the (existing or new) invocation; "" → pending_approval
	getStatus        string // status Get returns during the block-and-wait
	getDecision      string // decision Get returns during the block-and-wait
	claimOK          bool   // ClaimForExec wins (a granted invocation)
	gotCommand       string // the command persisted at Create (ADR-0059)
	gotEdge          string // the bracket edge persisted at Create (RUN-5)
	gotTool          string // the tool persisted at Create — echoed by Get/ClaimForExec like the real store's row (ADR-0071 §D2)
	setStatus        []string
	finished         []string
}

func (f *fakeStore) status() string {
	if f.createStatus != "" {
		return f.createStatus
	}
	return "pending_approval"
}

func (f *fakeStore) Create(_ context.Context, in store.CreateInput) (store.Invocation, bool, error) {
	f.gotCommand, f.gotEdge, f.gotTool = in.Command, in.Edge, in.Tool
	return store.Invocation{ID: "inv-1", Status: f.status(), AgentSubject: in.AgentSubject,
		ResourceID: in.ResourceID, Tool: in.Tool, Command: in.Command}, !f.idempotentHit, nil
}
func (f *fakeStore) CountInFlight(context.Context, string) (int, error) { return f.inFlight, nil }
func (f *fakeStore) SetStatus(_ context.Context, _, status, _ string) error {
	f.setStatus = append(f.setStatus, status)
	return nil
}
func (f *fakeStore) Finish(_ context.Context, _, status, outcome string) error {
	f.finished = append(f.finished, status+":"+outcome)
	return nil
}
func (f *fakeStore) RequiresApproval(context.Context, string) (bool, error) {
	return f.requiresApproval, f.requiresErr
}
func (f *fakeStore) Get(_ context.Context, id string) (store.Invocation, error) {
	return store.Invocation{ID: id, Status: f.getStatus, Decision: f.getDecision,
		ResourceID: "r1", Command: f.gotCommand, Tool: f.gotTool}, nil
}
func (f *fakeStore) ClaimForExec(_ context.Context, id string) (store.Invocation, bool, error) {
	if !f.claimOK {
		return store.Invocation{}, false, nil
	}
	return store.Invocation{ID: id, Status: "running", ResourceID: "r1", Command: f.gotCommand, Tool: f.gotTool}, true, nil
}

type fakePolicy struct {
	decision   string
	err        error
	resolveErr error
}

func (f *fakePolicy) Decide(context.Context, string, string, string, string, string, bool) (string, error) {
	return f.decision, f.err
}
func (f *fakePolicy) Resolve(context.Context, string, string, string) error { return f.resolveErr }

type fakeBroker struct {
	stdout      string
	exit        int
	truncated   bool
	err         error
	called      bool
	gotResource string
	gotCommand  string
}

func (f *fakeBroker) Exec(_ context.Context, _, resource, command, _ string) (string, int, bool, error) {
	f.called = true
	f.gotResource, f.gotCommand = resource, command
	return f.stdout, f.exit, f.truncated, f.err
}

// fakeBrokerFile fakes catalog.BrokerFiler for the T-140b remote.file.read, T-141
// remote.file.write, and T-142 remote.file.list tests. Read/Write/List fields are
// kept separate (readCalled vs writeCalled vs listCalled) so a test can assert one
// leg ran and the others did not — e.g. SEC-47 (only write mutates).
type fakeBrokerFile struct {
	content     []byte
	sha         string
	err         error
	called      bool
	gotResource string
	gotPath     string

	writeSHA        string
	writeErr        error
	writeCalled     bool
	gotWritePath    string
	gotWriteContent []byte
	gotWriteMode    uint32

	listEntries []brokerfile.Entry
	listErr     error
	listCalled  bool
	gotListPath string
}

func (f *fakeBrokerFile) Read(_ context.Context, _, resource, path, _ string) ([]byte, string, error) {
	f.called = true
	f.gotResource, f.gotPath = resource, path
	return f.content, f.sha, f.err
}

func (f *fakeBrokerFile) Write(_ context.Context, _, resource, path string, content []byte, mode uint32, _ string) (string, error) {
	f.writeCalled = true
	f.gotResource, f.gotWritePath, f.gotWriteContent, f.gotWriteMode = resource, path, content, mode
	if f.writeErr != nil {
		return "", f.writeErr
	}
	return f.writeSHA, nil
}

func (f *fakeBrokerFile) List(_ context.Context, _, resource, path, _ string) ([]brokerfile.Entry, error) {
	f.listCalled = true
	f.gotResource, f.gotListPath = resource, path
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listEntries, nil
}

func governedHandler(t *testing.T, st catalog.Store, pol catalog.PolicyDecider, brk catalog.BrokerExecer) (http.Handler, *auth.Signer) {
	t.Helper()
	return governedHandlerWithFile(t, st, pol, brk, nil)
}

// governedHandlerWithFile is governedHandler plus a wired (or nil, T-140b's
// nil-able-BrokerFile pattern) file client — the SEC-48/SEC-50 D7 tests and the
// remote.file.read wiring proofs need to reach the broker file leg; the existing
// exec-only tests keep using governedHandler unchanged (brokerFile stays nil there
// too, but they never dispatch remote.file.read so it never matters).
func governedHandlerWithFile(t *testing.T, st catalog.Store, pol catalog.PolicyDecider, brk catalog.BrokerExecer, brokerFile catalog.BrokerFiler) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	// Fast block-and-wait clocks so the approval tests stay sub-second.
	cat := catalog.New(catalog.Config{
		Store: st, Policy: pol, Broker: brk, BrokerFile: brokerFile, MaxInFlight: 4,
		WaitTimeout: 200 * time.Millisecond, PollInterval: 5 * time.Millisecond,
	})
	return New(Config{Verifier: v, Catalog: cat}), auth.NewSigner(key, "")
}

func scopedToken(t *testing.T, s *auth.Signer, resource string) string {
	t.Helper()
	tok, err := s.Issue(auth.IssueParams{
		Subject: "agent:test", Edge: "agent-tool", Audience: "mcp-gateway",
		Scope: auth.Scope{Resource: resource}, TTL: auth.AgentTTL,
	})
	if err != nil {
		t.Fatalf("mint scoped token: %v", err)
	}
	return tok
}

func callRemoteExec(t *testing.T, h http.Handler, tok, resourceID, command string) rpcResponse {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.exec","arguments":` +
		`{"resourceId":"` + resourceID + `","command":"` + command + `"}}}`
	return decodeRPC(t, postMCP(t, h, body, tok))
}

func callRemoteFileRead(t *testing.T, h http.Handler, tok, resourceID, path string) rpcResponse {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.file.read","arguments":` +
		`{"resourceId":"` + resourceID + `","path":"` + path + `"}}}`
	return decodeRPC(t, postMCP(t, h, body, tok))
}

// callRemoteFileWrite issues a remote.file.write tools/call with contentB64 (base64,
// caller-encoded) as the "content" arg — mirroring the real wire shape (ADR-0071 §D1).
func callRemoteFileWrite(t *testing.T, h http.Handler, tok, resourceID, path, contentB64 string) rpcResponse {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.file.write","arguments":` +
		`{"resourceId":"` + resourceID + `","path":"` + path + `","content":"` + contentB64 + `"}}}`
	return decodeRPC(t, postMCP(t, h, body, tok))
}

func callRemoteFileList(t *testing.T, h http.Handler, tok, resourceID, path string) rpcResponse {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.file.list","arguments":` +
		`{"resourceId":"` + resourceID + `","path":"` + path + `"}}}`
	return decodeRPC(t, postMCP(t, h, body, tok))
}

func toolResult(t *testing.T, resp rpcResponse) (isError bool, text string) {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("expected a tool result, got rpc error %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %T", resp.Result)
	}
	isError, _ = m["isError"].(bool)
	if content, ok := m["content"].([]any); ok && len(content) > 0 {
		if c0, ok := content[0].(map[string]any); ok {
			text, _ = c0["text"].(string)
		}
	}
	return isError, text
}

func TestInvokeAllowPath(t *testing.T) {
	st := &fakeStore{}
	brk := &fakeBroker{stdout: "hi\n", exit: 0}
	h, s := governedHandler(t, st, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if isErr || text != "hi\n" {
		t.Fatalf("allow path: isError=%v text=%q", isErr, text)
	}
	if !brk.called || brk.gotResource != "r1" || brk.gotCommand != "echo hi" {
		t.Fatalf("broker exec not called correctly: %+v", brk)
	}
	if st.gotCommand != "echo hi" {
		t.Fatalf("command not persisted on the invocation (ADR-0059): %q", st.gotCommand)
	}
	if len(st.finished) != 1 || st.finished[0] != "completed:tool.result" {
		t.Fatalf("finish = %v, want [completed:tool.result]", st.finished)
	}
}

func TestInvokeScopeViolation(t *testing.T) {
	brk := &fakeBroker{}
	h, s := governedHandler(t, &fakeStore{}, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r2", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "scope") {
		t.Fatalf("scope violation: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run for an out-of-scope resource")
	}
}

func scopedTokenOps(t *testing.T, s *auth.Signer, resource string, ops []string) string {
	t.Helper()
	tok, err := s.Issue(auth.IssueParams{
		Subject: "agent:test", Edge: "agent-tool", Audience: "mcp-gateway",
		Scope: auth.Scope{Resource: resource, Operations: ops}, TTL: auth.AgentTTL,
	})
	if err != nil {
		t.Fatalf("mint scoped token: %v", err)
	}
	return tok
}

// SEC-16: a token whose operation scope excludes the tool's operation is refused.
func TestInvokeOperationScopeViolation(t *testing.T) {
	brk := &fakeBroker{}
	h, s := governedHandler(t, &fakeStore{}, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedTokenOps(t, s, "r1", []string{"read"}), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "operation") {
		t.Fatalf("operation scope: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run for an out-of-scope operation")
	}
}

// A token scoping the matching operation ("exec") proceeds normally.
func TestInvokeOperationScopeAllowed(t *testing.T) {
	brk := &fakeBroker{stdout: "ok\n"}
	h, s := governedHandler(t, &fakeStore{}, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedTokenOps(t, s, "r1", []string{"exec"}), "r1", "echo hi")
	if isErr, _ := toolResult(t, resp); isErr || !brk.called {
		t.Fatalf("the exec operation should be in scope: isError=%v called=%v", isErr, brk.called)
	}
}

// TestSEC48EmptyOperationsRejectsFileReadAllowsExec is SEC-48 (ADR-0071 §D7), the
// ticket's block gate: a token with an EMPTY operations list must be REJECTED for
// remote.file.read and ACCEPTED for remote.exec — proven on the SAME token in the SAME
// test, so the distinction between "exec's back-compat allowance" and "every other verb
// requires explicit scope" is what is actually under test. An implementation that
// always allows on an empty list, or always denies on one, fails exactly one half of
// this test and not the other.
func TestSEC48EmptyOperationsRejectsFileReadAllowsExec(t *testing.T) {
	fileBrk := &fakeBrokerFile{content: []byte("server {}"), sha: "deadbeef"}
	execBrk := &fakeBroker{stdout: "hi\n"}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, execBrk, fileBrk)
	tok := scopedToken(t, s, "r1") // Operations left empty — the back-compat scope shape

	// Direction A: remote.file.read must be REFUSED for an empty-ops token.
	fileResp := callRemoteFileRead(t, h, tok, "r1", "/etc/nginx/nginx.conf")
	isErr, text := toolResult(t, fileResp)
	if !isErr || !strings.Contains(text, "operation") {
		t.Fatalf("empty-ops token on remote.file.read: isError=%v text=%q, want an operation-scope refusal (D7)", isErr, text)
	}
	if fileBrk.called {
		t.Fatal("broker file Read must not run for an empty-ops token (D7 fail-closed)")
	}

	// Direction B: the SAME token must still be ACCEPTED for remote.exec — the
	// exec-only back-compat allowance is unchanged.
	execResp := callRemoteExec(t, h, tok, "r1", "echo hi")
	if isErr, _ := toolResult(t, execResp); isErr || !execBrk.called {
		t.Fatalf("empty-ops token on remote.exec must still be allowed (D7 exec back-compat unchanged): isError=%v called=%v", isErr, execBrk.called)
	}
}

// TestSEC50FileReadScopedTokenRejectsRemoteExec is SEC-50 (partial): a token scoped to
// exactly ["file.read"] — the narrowest possible file grant — must NOT also unlock
// remote.exec. Operation scope is per-verb, never additive.
func TestSEC50FileReadScopedTokenRejectsRemoteExec(t *testing.T) {
	execBrk := &fakeBroker{}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, execBrk, &fakeBrokerFile{})
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteExec(t, h, tok, "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "operation") {
		t.Fatalf("file.read-scoped token on remote.exec: isError=%v text=%q, want an operation-scope refusal", isErr, text)
	}
	if execBrk.called {
		t.Fatal("broker exec must not run for a file.read-scoped token")
	}
}

// TestInvokeFileReadAllowPath proves remote.file.read's end-to-end wiring (T-140b): a
// token explicitly scoped for file.read reaches the broker file client via the SAME
// governed pipeline as remote.exec (concurrency cap, invocation create, policy decide,
// terminal dispatch) and returns the file's content plus its digest to the agent.
func TestInvokeFileReadAllowPath(t *testing.T) {
	fileBrk := &fakeBrokerFile{content: []byte("server {}"), sha: "9f2c00a41b"}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileRead(t, h, tok, "r1", "/etc/nginx/nginx.conf")
	isErr, text := toolResult(t, resp)
	if isErr || !strings.Contains(text, "server {}") || !strings.Contains(text, fileBrk.sha) {
		t.Fatalf("file read allow path: isError=%v text=%q", isErr, text)
	}
	if !fileBrk.called || fileBrk.gotResource != "r1" || fileBrk.gotPath != "/etc/nginx/nginx.conf" {
		t.Fatalf("broker file Read not called correctly: %+v", fileBrk)
	}
}

// TestInvokeFileReadNilBrokerFileAnswersToolError proves the nil-able pattern (Part 3 of
// the brief): an unwired BrokerFile answers a tool error, never a panic — mirroring
// Forwarder/PollForwarder/etc.
func TestInvokeFileReadNilBrokerFileAnswersToolError(t *testing.T) {
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, nil)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileRead(t, h, tok, "r1", "/etc/nginx/nginx.conf")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "not available") {
		t.Fatalf("nil BrokerFile: isError=%v text=%q, want a tool-level refusal, not a panic", isErr, text)
	}
}

// TestInvokeFileWriteAllowPath proves remote.file.write's end-to-end wiring (T-141): a
// token explicitly scoped for file.write reaches the broker file client's Write leg via
// the SAME governed pipeline as remote.exec/remote.file.read — including the SEC-49
// digest check, which passes here because content and descriptor come from the SAME
// call (Invoke computes both from the same args).
func TestInvokeFileWriteAllowPath(t *testing.T) {
	fileBrk := &fakeBrokerFile{writeSHA: "9f2c00a41b"}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.write"})
	content := "server { listen 80; }"
	resp := callRemoteFileWrite(t, h, tok, "r1", "/etc/nginx/nginx.conf", base64.StdEncoding.EncodeToString([]byte(content)))
	isErr, text := toolResult(t, resp)
	if isErr || !strings.Contains(text, fileBrk.writeSHA) {
		t.Fatalf("file write allow path: isError=%v text=%q", isErr, text)
	}
	if !fileBrk.writeCalled || fileBrk.gotWritePath != "/etc/nginx/nginx.conf" ||
		string(fileBrk.gotWriteContent) != content || fileBrk.gotWriteMode != 0o644 {
		t.Fatalf("broker file Write not called correctly: %+v", fileBrk)
	}
}

// TestInvokeFileWriteNilBrokerFileAnswersToolError mirrors the read side's nil-able
// pattern for the write leg.
func TestInvokeFileWriteNilBrokerFileAnswersToolError(t *testing.T) {
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, nil)
	tok := scopedTokenOps(t, s, "r1", []string{"file.write"})
	resp := callRemoteFileWrite(t, h, tok, "r1", "/etc/nginx/nginx.conf", base64.StdEncoding.EncodeToString([]byte("x")))
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "not available") {
		t.Fatalf("nil BrokerFile: isError=%v text=%q, want a tool-level refusal, not a panic", isErr, text)
	}
}

// TestSEC47FileReadScopedTokenRejectsRemoteFileWrite is SEC-47/SEC-50's write-side
// half: a token scoped to exactly ["file.read"] must NOT also unlock
// remote.file.write — remote.file.write is the ONLY family member that mutates a
// target, and operation scope is per-verb, never additive.
func TestSEC47FileReadScopedTokenRejectsRemoteFileWrite(t *testing.T) {
	fileBrk := &fakeBrokerFile{}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileWrite(t, h, tok, "r1", "/etc/nginx/nginx.conf", base64.StdEncoding.EncodeToString([]byte("x")))
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "operation") {
		t.Fatalf("file.read-scoped token on remote.file.write: isError=%v text=%q, want an operation-scope refusal", isErr, text)
	}
	if fileBrk.writeCalled {
		t.Fatal("broker file Write must not run for a file.read-scoped token")
	}
}

// TestInvokeFileListAllowPath proves remote.file.list's end-to-end wiring (T-142): a
// token scoped ["file.read"] — the SAME scope remote.file.read uses, per ADR-0071
// §D1's deliberate choice not to mint a third verb — reaches the broker file client's
// List leg via the SAME governed pipeline as remote.exec/remote.file.read, and the
// entries come back rendered for the agent.
func TestInvokeFileListAllowPath(t *testing.T) {
	fileBrk := &fakeBrokerFile{listEntries: []brokerfile.Entry{
		{Name: "nginx.conf", Size: 1234, Mode: 0o644, ModTime: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)},
	}}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileList(t, h, tok, "r1", "/etc/nginx")
	isErr, text := toolResult(t, resp)
	if isErr || !strings.Contains(text, "nginx.conf") {
		t.Fatalf("file list allow path: isError=%v text=%q", isErr, text)
	}
	if !fileBrk.listCalled || fileBrk.gotResource != "r1" || fileBrk.gotListPath != "/etc/nginx" {
		t.Fatalf("broker file List not called correctly: %+v", fileBrk)
	}
}

// TestInvokeFileListNilBrokerFileAnswersToolError mirrors the read/write nil-able
// pattern for the list leg.
func TestInvokeFileListNilBrokerFileAnswersToolError(t *testing.T) {
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, nil)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileList(t, h, tok, "r1", "/etc")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "not available") {
		t.Fatalf("nil BrokerFile: isError=%v text=%q, want a tool-level refusal, not a panic", isErr, text)
	}
}

// TestSEC50FileReadScopedTokenRejectsRemoteFileList's INVERSE would be wrong per
// ADR-0071 §D1 (list deliberately shares file.read's verb) — so this instead proves
// the D1 sharing itself: a token scoped to exactly ["file.write"] (NOT file.read) must
// be REJECTED for remote.file.list, since file.write and file.read remain distinct
// verbs (SEC-50) even though list piggybacks on read rather than minting its own.
func TestFileWriteScopedTokenRejectsRemoteFileList(t *testing.T) {
	fileBrk := &fakeBrokerFile{}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.write"})
	resp := callRemoteFileList(t, h, tok, "r1", "/etc")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "operation") {
		t.Fatalf("file.write-scoped token on remote.file.list: isError=%v text=%q, want an operation-scope refusal", isErr, text)
	}
	if fileBrk.listCalled {
		t.Fatal("broker file List must not run for a file.write-scoped token")
	}
}

// TestSEC47FileListNeverMutates is SEC-47's file.list half: no argument shape reaches
// the broker's Write leg through remote.file.list — the tool has no content extractor
// at all (ToolDef.content is nil for it, same as read), so Invoke's Content() call
// always yields nil regardless of what the caller's raw arguments contain.
func TestSEC47FileListNeverMutates(t *testing.T) {
	fileBrk := &fakeBrokerFile{listEntries: []brokerfile.Entry{{Name: "x"}}}
	h, s := governedHandlerWithFile(t, &fakeStore{}, &fakePolicy{decision: "allow"}, &fakeBroker{}, fileBrk)
	tok := scopedTokenOps(t, s, "r1", []string{"file.read"})
	resp := callRemoteFileList(t, h, tok, "r1", "/etc")
	if isErr, _ := toolResult(t, resp); isErr {
		t.Fatalf("file list allow path unexpectedly errored: isError=%v", isErr)
	}
	if fileBrk.writeCalled {
		t.Fatal("SEC-47 FAILED: broker file Write was called by remote.file.list — list must never mutate")
	}
}

func TestInvokeConcurrencyCap(t *testing.T) {
	brk := &fakeBroker{}
	h, s := governedHandler(t, &fakeStore{inFlight: 4}, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "concurrent") {
		t.Fatalf("concurrency cap: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run when the concurrency cap is hit")
	}
}

// requires_approval now BLOCKS (block-and-wait, T-040). A grant (the row's decision
// flips to allow) is atomically claimed and executed — the approved command runs.
func TestInvokeApprovalGrantedExecs(t *testing.T) {
	st := &fakeStore{requiresApproval: true, getStatus: "pending_approval", getDecision: "allow", claimOK: true}
	brk := &fakeBroker{stdout: "ok\n", exit: 0}
	h, s := governedHandler(t, st, &fakePolicy{decision: "requires_approval"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if isErr || text != "ok\n" {
		t.Fatalf("granted path: isError=%v text=%q", isErr, text)
	}
	if !brk.called || brk.gotCommand != "echo hi" {
		t.Fatalf("broker must run the approved command, got %+v", brk)
	}
	if len(st.setStatus) == 0 || st.setStatus[len(st.setStatus)-1] != "pending_approval" {
		t.Fatalf("setStatus = %v, want it to park pending_approval first", st.setStatus)
	}
	if len(st.finished) != 1 || st.finished[0] != "completed:tool.result" {
		t.Fatalf("finish = %v, want [completed:tool.result]", st.finished)
	}
}

// A deny (the row's status flips to denied) blocks the call with a typed error; no egress.
func TestInvokeApprovalDeniedBlocks(t *testing.T) {
	st := &fakeStore{requiresApproval: true, getStatus: "denied"}
	brk := &fakeBroker{}
	h, s := governedHandler(t, st, &fakePolicy{decision: "requires_approval"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "denied") {
		t.Fatalf("denied path: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run for a denied invocation")
	}
}

// No operator response → the gateway's block-and-wait deadline finalizes the
// invocation failed and returns a typed timeout (its leg of the fail-closed timeout).
func TestInvokeApprovalTimesOut(t *testing.T) {
	st := &fakeStore{requiresApproval: true, getStatus: "pending_approval", getDecision: "requires_approval"}
	brk := &fakeBroker{}
	h, s := governedHandler(t, st, &fakePolicy{decision: "requires_approval"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "timed out") {
		t.Fatalf("timeout path: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run on an approval timeout")
	}
	if len(st.finished) != 1 || st.finished[0] != "failed:tool.result:error" {
		t.Fatalf("finish = %v, want [failed:tool.result:error]", st.finished)
	}
}

// An idempotent retry of a STILL-LIVE invocation re-attaches to the wait rather than
// erroring — a dropped long-poll the agent retries resumes (PERF-2 + ADR-0059).
func TestInvokeIdempotentReattachGrant(t *testing.T) {
	st := &fakeStore{idempotentHit: true, requiresApproval: true,
		getStatus: "pending_approval", getDecision: "allow", claimOK: true}
	brk := &fakeBroker{stdout: "again\n"}
	h, s := governedHandler(t, st, &fakePolicy{decision: "requires_approval"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if isErr || text != "again\n" {
		t.Fatalf("re-attach grant: isError=%v text=%q", isErr, text)
	}
	if !brk.called {
		t.Fatal("a re-attach must wait then exec, not error")
	}
}

// An idempotent retry of a TERMINAL invocation cannot replay (stdout not stored,
// ADR-0058) — it returns a typed marker without re-executing.
func TestInvokeIdempotentTerminalNoReplay(t *testing.T) {
	st := &fakeStore{idempotentHit: true, createStatus: "completed"}
	brk := &fakeBroker{}
	h, s := governedHandler(t, st, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "duplicate") {
		t.Fatalf("terminal re-attach: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("a terminal idempotent hit must not re-execute")
	}
}

// --- operator resolve/list plane (T-040a2) ----------------------------------

type fakeResolver struct {
	pending     []mcp.InvocationView
	listErr     error
	resolveErr  error
	gotID       string
	gotDecision string
	gotResolved string
}

func (f *fakeResolver) ListPending(context.Context) ([]mcp.InvocationView, error) {
	return f.pending, f.listErr
}
func (f *fakeResolver) Resolve(_ context.Context, id, decision, resolvedBy string) error {
	f.gotID, f.gotDecision, f.gotResolved = id, decision, resolvedBy
	return f.resolveErr
}

func resolverHandler(t *testing.T, rsv mcp.ResolverPort) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	return New(Config{Verifier: v, Catalog: emptyCatalog{}, Resolver: rsv}), auth.NewSigner(key, "")
}

func serviceToken(t *testing.T, s *auth.Signer) string {
	t.Helper()
	tok, err := s.IssueService("api", "mcp-gateway")
	if err != nil {
		t.Fatalf("mint service token: %v", err)
	}
	return tok
}

func doReq(t *testing.T, h http.Handler, method, path, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The internal resolve/list plane is service-service only (ADR-0056 two planes): an
// agent-tool token (the agent plane) must not reach it, and no token is 401.
func TestInternalPlaneRequiresServiceEdge(t *testing.T) {
	h, s := resolverHandler(t, &fakeResolver{})
	if rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
	if rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", agentToken(t, s)); rec.Code != http.StatusForbidden {
		t.Fatalf("agent-tool token on internal plane: status = %d, want 403", rec.Code)
	}
}

// Defense in depth (R5): a valid service-service token whose subject is NOT api is
// refused on the internal plane — only services/api proxies the operator here.
func TestInternalPlaneRequiresApiSubject(t *testing.T) {
	h, s := resolverHandler(t, &fakeResolver{})
	tok, err := s.IssueService("broker", "mcp-gateway") // service-service edge, wrong subject
	if err != nil {
		t.Fatalf("mint service token: %v", err)
	}
	if rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", tok); rec.Code != http.StatusForbidden {
		t.Fatalf("non-api service token: status = %d, want 403", rec.Code)
	}
}

// With no resolver wired the internal routes are not mounted (mirrors the broker's
// nil-disables-exec): a service token gets 404, not 200.
func TestInternalRoutesDisabledWhenNoResolver(t *testing.T) {
	h, s := newTestHandler(t) // nil Resolver
	if rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", serviceToken(t, s)); rec.Code != http.StatusNotFound {
		t.Fatalf("nil resolver: GET /v1/invocations = %d, want 404 (route absent)", rec.Code)
	}
}

func TestListInvocationsReturnsPending(t *testing.T) {
	rsv := &fakeResolver{pending: []mcp.InvocationView{{ID: "inv-1", Agent: "agent:x", ResourceID: "r1", Tool: "remote.exec", Command: "uname -a", Status: "pending_approval"}}}
	h, s := resolverHandler(t, rsv)
	rec := doReq(t, h, http.MethodGet, "/v1/invocations", "", serviceToken(t, s))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"command":"uname -a"`) {
		t.Fatalf("list body missing the command (ADR-0059 operator payload): %s", rec.Body.String())
	}
}

func TestResolveRouteStatusMapping(t *testing.T) {
	tok := func(s *auth.Signer) string { return serviceToken(t, s) }
	cases := []struct {
		name     string
		body     string
		err      error
		want     int
		wantVerb string
	}{
		{"granted", `{"decision":"granted","resolvedBy":"operator:1"}`, nil, http.StatusOK, "granted"},
		{"denied", `{"decision":"denied","resolvedBy":"operator:1"}`, nil, http.StatusOK, "denied"},
		{"bad decision", `{"decision":"maybe"}`, nil, http.StatusBadRequest, ""},
		{"not found", `{"decision":"granted"}`, mcp.ErrInvocationNotFound, http.StatusNotFound, "granted"},
		{"not pending", `{"decision":"granted"}`, mcp.ErrApprovalNotPending, http.StatusConflict, "granted"},
		{"policy outage", `{"decision":"granted"}`, errors.New("boom"), http.StatusBadGateway, "granted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rsv := &fakeResolver{resolveErr: c.err}
			h, s := resolverHandler(t, rsv)
			rec := doReq(t, h, http.MethodPost, "/v1/invocations/inv-9/resolve", c.body, tok(s))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, c.want, rec.Body.String())
			}
			if c.wantVerb != "" && rsv.gotDecision != c.wantVerb {
				t.Fatalf("resolver got decision %q, want %q", rsv.gotDecision, c.wantVerb)
			}
		})
	}
}

// --- operator Run-step plane (T-055b) ---------------------------------------

func stepHandler(t *testing.T, st catalog.Store, pol catalog.PolicyDecider, brk catalog.BrokerExecer) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	cat := catalog.New(catalog.Config{
		Store: st, Policy: pol, Broker: brk, MaxInFlight: 4,
		WaitTimeout: 200 * time.Millisecond, RunWaitTimeout: 200 * time.Millisecond, PollInterval: 5 * time.Millisecond,
	})
	return New(Config{Verifier: v, Catalog: cat, StepRunner: cat}), auth.NewSigner(key, "")
}

// The operator Run engine drives a governed step via /v1/steps: the step records the
// OPERATOR as actor on the human-agent bracket edge (RUN-5) and reuses the agent
// path's governed core (here: allow → broker exec).
func TestRunStepAllowPath(t *testing.T) {
	st := &fakeStore{}
	brk := &fakeBroker{stdout: "RUN-OK\n", exit: 0}
	h, s := stepHandler(t, st, &fakePolicy{decision: "allow"}, brk)
	body := `{"subject":"operator:admin","resourceId":"r1","command":"echo hi","idempotencyKey":"run:1:step:0"}`
	rec := doReq(t, h, http.MethodPost, "/v1/steps", body, serviceToken(t, s))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if !brk.called || brk.gotCommand != "echo hi" {
		t.Fatalf("broker not driven: %+v", brk)
	}
	if st.gotEdge != "human-agent" {
		t.Fatalf("bracket edge = %q, want human-agent (RUN-5)", st.gotEdge)
	}
	if !strings.Contains(rec.Body.String(), "RUN-OK") {
		t.Fatalf("result missing stdout: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"invocationId":"inv-1"`) {
		t.Fatalf("response missing invocationId (AUD-15 correlation): %s", rec.Body.String())
	}
}

// SEC-24: /v1/steps is api-only + field-validated. agent token / non-api service
// token → 403; no token → 401; missing fields → 400; none of these run the broker.
func TestRunStepSubjectPinAndValidation(t *testing.T) {
	st := &fakeStore{}
	brk := &fakeBroker{}
	h, s := stepHandler(t, st, &fakePolicy{decision: "allow"}, brk)
	good := `{"subject":"operator:admin","resourceId":"r1","command":"echo hi"}`

	if rec := doReq(t, h, http.MethodPost, "/v1/steps", good, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d, want 401", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPost, "/v1/steps", good, agentToken(t, s)); rec.Code != http.StatusForbidden {
		t.Fatalf("agent token: %d, want 403", rec.Code)
	}
	nonAPI, _ := s.IssueService("broker", "mcp-gateway")
	if rec := doReq(t, h, http.MethodPost, "/v1/steps", good, nonAPI); rec.Code != http.StatusForbidden {
		t.Fatalf("non-api service token: %d, want 403", rec.Code)
	}
	if rec := doReq(t, h, http.MethodPost, "/v1/steps", `{"subject":"operator:admin"}`, serviceToken(t, s)); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing fields: %d, want 400", rec.Code)
	}
	if brk.called {
		t.Fatal("broker must not run on a rejected step")
	}
}

// With no StepRunner wired the /v1/steps route is not mounted (mirrors the resolver).
func TestRunStepDisabledWhenNoRunner(t *testing.T) {
	h, s := newTestHandler(t) // nil StepRunner
	if rec := doReq(t, h, http.MethodPost, "/v1/steps", `{}`, serviceToken(t, s)); rec.Code != http.StatusNotFound {
		t.Fatalf("nil runner: POST /v1/steps = %d, want 404 (route absent)", rec.Code)
	}
}

func TestInvokePolicyFailClosed(t *testing.T) {
	st := &fakeStore{}
	brk := &fakeBroker{}
	h, s := governedHandler(t, st, &fakePolicy{err: errors.New("policy down")}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	if resp.Error == nil {
		t.Fatal("policy outage must surface as a JSON-RPC error (fail-closed)")
	}
	if brk.called {
		t.Fatal("broker must not run when policy is unavailable")
	}
	if len(st.finished) != 1 || st.finished[0] != "failed:tool.result:error" {
		t.Fatalf("finish = %v, want [failed:tool.result:error]", st.finished)
	}
}

func TestInvokeExecFailClosed(t *testing.T) {
	st := &fakeStore{}
	brk := &fakeBroker{err: errors.New("dial failed")}
	h, s := governedHandler(t, st, &fakePolicy{decision: "allow"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	if resp.Error == nil {
		t.Fatal("exec transport failure must surface as a JSON-RPC error (fail-closed)")
	}
	if len(st.finished) != 1 || st.finished[0] != "failed:tool.result:error" {
		t.Fatalf("finish = %v, want [failed:tool.result:error]", st.finished)
	}
}

// --- v0.9 control-plane tools (T-090, ADR-0065 §1) --------------------------------
// workstream.poll/plan.submit/memory.read follow the run.launch pattern: no target
// resolver, dispatched before remote.exec's SEC-16 path, driven through fakeStore/
// fakePolicy/fakeBroker plus a fake per-verb forwarder (mcp.PollForwarder/
// SubmitForwarder/ReadForwarder).

type fakePollForwarder struct {
	requests []mcp.PlanRequestView
	err      error
}

func (f *fakePollForwarder) Poll(context.Context, string, string) ([]mcp.PlanRequestView, error) {
	return f.requests, f.err
}

type fakeSubmitForwarder struct {
	runID, status, refusal string
	err                    error
}

func (f *fakeSubmitForwarder) Submit(context.Context, string, string, []mcp.LaunchStep) (string, string, string, error) {
	return f.runID, f.status, f.refusal, f.err
}

type fakeReadForwarder struct {
	items []mcp.MemoryRowView
	err   error
}

func (f *fakeReadForwarder) Read(context.Context, string, string) ([]mcp.MemoryRowView, error) {
	return f.items, f.err
}

// fakeSearchForwarder mirrors fakeReadForwarder for the v0.10 context.search tool
// (ADR-0066 §4, T-099).
type fakeSearchForwarder struct {
	chunks []mcp.ContextChunkView
	err    error
}

func (f *fakeSearchForwarder) Search(context.Context, string, string, string) ([]mcp.ContextChunkView, error) {
	return f.chunks, f.err
}

// controlPlaneHandler mirrors governedHandler but additionally wires the v0.9/v0.10
// forwarders (any of which may be nil — the T-090 "answers a tool error" posture).
func controlPlaneHandler(t *testing.T, st catalog.Store, poll mcp.PollForwarder, submit mcp.SubmitForwarder, read mcp.ReadForwarder, search mcp.ContextSearchForwarder) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	cat := catalog.New(catalog.Config{
		Store: st, Policy: &fakePolicy{decision: "allow"}, Broker: &fakeBroker{},
		MaxInFlight: 4, WaitTimeout: 200 * time.Millisecond, PollInterval: 5 * time.Millisecond,
		PollForwarder: poll, SubmitForwarder: submit, ReadForwarder: read, SearchForwarder: search,
	})
	return New(Config{Verifier: v, Catalog: cat}), auth.NewSigner(key, "")
}

func callTool(t *testing.T, h http.Handler, tok, name, argsJSON string) rpcResponse {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + argsJSON + `}}`
	return decodeRPC(t, postMCP(t, h, body, tok))
}

// TestControlPlaneToolsNilForwarderErrors — T-090: with no forwarder wired (the
// unset-API_URL posture, main.go — mirrors run.launch's own nil-forwarder answer,
// ADR-0062 §3), all three v0.9 control-plane tools answer a TOOL error (never an
// rpc-level error, never a panic).
func TestControlPlaneToolsNilForwarderErrors(t *testing.T) {
	h, s := controlPlaneHandler(t, &fakeStore{}, nil, nil, nil, nil)
	tok := agentToken(t, s)
	for _, tc := range []struct{ name, args string }{
		{"workstream.poll", `{}`},
		{"plan.submit", `{}`},
		{"memory.read", `{}`},
		{"context.search", `{"query":"ops runbook"}`},
	} {
		resp := callTool(t, h, tok, tc.name, tc.args)
		isErr, text := toolResult(t, resp)
		if !isErr || text == "" {
			t.Fatalf("%s with nil forwarder: isError=%v text=%q, want a tool error", tc.name, isErr, text)
		}
	}
}

// TestMemoryReadConcurrencyCap — D5/PERF-10(5): memory.read participates in the
// gateway's per-agent maxInFlight cap, the SAME seam TestInvokeConcurrencyCap exercises
// for remote.exec — reads carry no grant-budget bound, so this cap is their only brake.
func TestMemoryReadConcurrencyCap(t *testing.T) {
	st := &fakeStore{inFlight: 4} // == controlPlaneHandler's MaxInFlight: at the cap
	h, s := controlPlaneHandler(t, st, nil, nil, &fakeReadForwarder{}, nil)
	resp := callTool(t, h, agentToken(t, s), "memory.read", `{}`)
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "concurrent") {
		t.Fatalf("memory.read at the concurrency cap: isError=%v text=%q, want a concurrency refusal", isErr, text)
	}
}

// TestContextSearchConcurrencyCap — the memory.read precedent (D5/PERF-10(5))
// extended to context.search (ADR-0066 §4, T-099): a second read verb, same cap.
func TestContextSearchConcurrencyCap(t *testing.T) {
	st := &fakeStore{inFlight: 4} // == controlPlaneHandler's MaxInFlight: at the cap
	h, s := controlPlaneHandler(t, st, nil, nil, nil, &fakeSearchForwarder{})
	resp := callTool(t, h, agentToken(t, s), "context.search", `{"query":"ops runbook"}`)
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "concurrent") {
		t.Fatalf("context.search at the concurrency cap: isError=%v text=%q, want a concurrency refusal", isErr, text)
	}
}

// TestPollSubmitBypassConcurrencyCap — D5/R1: workstream.poll and plan.submit do NOT
// consult the per-agent cap (dispatched before runGovernedStep, the only other
// CountInFlight call site — exactly like run.launch). Proven by wiring a Store whose
// CountInFlight already reports at-cap and showing both tools still reach their
// forwarder rather than being refused.
func TestPollSubmitBypassConcurrencyCap(t *testing.T) {
	st := &fakeStore{inFlight: 4} // at controlPlaneHandler's cap — would refuse memory.read
	poll := &fakePollForwarder{requests: []mcp.PlanRequestView{{RequestID: "req-1"}}}
	submit := &fakeSubmitForwarder{runID: "run-1", status: "proposed"}
	h, s := controlPlaneHandler(t, st, poll, submit, nil, nil)
	tok := agentToken(t, s)

	if isErr, text := toolResult(t, callTool(t, h, tok, "workstream.poll", `{}`)); isErr {
		t.Fatalf("workstream.poll must bypass the concurrency cap: isError=%v text=%q", isErr, text)
	}
	submitArgs := `{"requestId":"req-1","steps":[{"resourceId":"r1","command":"echo hi"}]}`
	if isErr, text := toolResult(t, callTool(t, h, tok, "plan.submit", submitArgs)); isErr {
		t.Fatalf("plan.submit must bypass the concurrency cap: isError=%v text=%q", isErr, text)
	}
}

// TestPollSubmitReadHappyPath — the forwarder's answer rides back as the tool result
// text (no target resolution, no policy/broker involvement — the run.launch shape).
func TestPollSubmitReadHappyPath(t *testing.T) {
	poll := &fakePollForwarder{requests: []mcp.PlanRequestView{{RequestID: "req-1", Objective: "keep it healthy"}}}
	submit := &fakeSubmitForwarder{runID: "run-9", status: "proposed"}
	read := &fakeReadForwarder{items: []mcp.MemoryRowView{{ID: "mem-1"}}}
	search := &fakeSearchForwarder{chunks: []mcp.ContextChunkView{{ChunkID: "chunk-1"}}}
	h, s := controlPlaneHandler(t, &fakeStore{}, poll, submit, read, search)
	tok := agentToken(t, s)

	if isErr, text := toolResult(t, callTool(t, h, tok, "workstream.poll", `{}`)); isErr || !strings.Contains(text, "req-1") {
		t.Fatalf("workstream.poll happy path: isError=%v text=%q", isErr, text)
	}
	submitArgs := `{"requestId":"req-1","steps":[{"resourceId":"r1","command":"echo hi"}]}`
	if isErr, text := toolResult(t, callTool(t, h, tok, "plan.submit", submitArgs)); isErr || !strings.Contains(text, "run-9") {
		t.Fatalf("plan.submit happy path: isError=%v text=%q", isErr, text)
	}
	if isErr, text := toolResult(t, callTool(t, h, tok, "memory.read", `{}`)); isErr || !strings.Contains(text, "mem-1") {
		t.Fatalf("memory.read happy path: isError=%v text=%q", isErr, text)
	}
	if isErr, text := toolResult(t, callTool(t, h, tok, "context.search", `{"query":"ops runbook"}`)); isErr || !strings.Contains(text, "chunk-1") {
		t.Fatalf("context.search happy path: isError=%v text=%q", isErr, text)
	}
}

// TestSubmitPlanRefusalIsToolError — plan.submit's SEC-29 refusal shape (the
// run.launch mapping): a non-empty forwarder refusal is a TOOL error, not an rpc error.
func TestSubmitPlanRefusalIsToolError(t *testing.T) {
	submit := &fakeSubmitForwarder{refusal: "the grant does not authorize this agent"}
	h, s := controlPlaneHandler(t, &fakeStore{}, nil, submit, nil, nil)
	args := `{"requestId":"req-1","steps":[{"resourceId":"r1","command":"echo hi"}]}`
	isErr, text := toolResult(t, callTool(t, h, agentToken(t, s), "plan.submit", args))
	if !isErr || !strings.Contains(text, "does not authorize") {
		t.Fatalf("plan.submit refusal: isError=%v text=%q", isErr, text)
	}
}
