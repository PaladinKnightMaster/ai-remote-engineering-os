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
	setStatus        []string
	finished         []string
}

func (f *fakeStore) Create(_ context.Context, in store.CreateInput) (store.Invocation, bool, error) {
	return store.Invocation{ID: "inv-1", Status: "pending_approval", AgentSubject: in.AgentSubject, ResourceID: in.ResourceID, Tool: in.Tool}, !f.idempotentHit, nil
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

type fakePolicy struct {
	decision string
	err      error
}

func (f *fakePolicy) Decide(context.Context, string, string, string, string, string, bool) (string, error) {
	return f.decision, f.err
}

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
	return New(Config{Verifier: v, Catalog: catalog.New(st, pol, brk, 4)}), auth.NewSigner(key, "")
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

func TestInvokeRequiresApprovalParks(t *testing.T) {
	st := &fakeStore{requiresApproval: true}
	brk := &fakeBroker{}
	h, s := governedHandler(t, st, &fakePolicy{decision: "requires_approval"}, brk)
	resp := callRemoteExec(t, h, scopedToken(t, s, "r1"), "r1", "echo hi")
	isErr, text := toolResult(t, resp)
	if !isErr || !strings.Contains(text, "approval required") {
		t.Fatalf("requires approval: isError=%v text=%q", isErr, text)
	}
	if brk.called {
		t.Fatal("broker must not run before approval")
	}
	if len(st.setStatus) == 0 || st.setStatus[len(st.setStatus)-1] != "pending_approval" {
		t.Fatalf("setStatus = %v, want it to park pending_approval", st.setStatus)
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
