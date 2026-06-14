package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aireos.local/packages/go/auth"
	"aireos.local/services/mcp-gateway/internal/mcp"
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
