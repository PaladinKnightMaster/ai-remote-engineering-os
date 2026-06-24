package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
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
	f.gotCommand, f.gotEdge = in.Command, in.Edge
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
		ResourceID: "r1", Command: f.gotCommand}, nil
}
func (f *fakeStore) ClaimForExec(_ context.Context, id string) (store.Invocation, bool, error) {
	if !f.claimOK {
		return store.Invocation{}, false, nil
	}
	return store.Invocation{ID: id, Status: "running", ResourceID: "r1", Command: f.gotCommand}, true, nil
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

func governedHandler(t *testing.T, st catalog.Store, pol catalog.PolicyDecider, brk catalog.BrokerExecer) (http.Handler, *auth.Signer) {
	t.Helper()
	key, _ := auth.GenerateKey()
	v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "mcp-gateway"))
	// Fast block-and-wait clocks so the approval tests stay sub-second.
	cat := catalog.New(catalog.Config{
		Store: st, Policy: pol, Broker: brk, MaxInFlight: 4,
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
