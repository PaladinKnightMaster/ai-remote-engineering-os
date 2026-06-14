// Package server implements the MCP Gateway HTTP surface (ADR-0056): an ungated
// /healthz and the agent-tool-gated MCP endpoint. The endpoint hand-rolls the
// synchronous streamable-HTTP subset of MCP — `initialize`, `tools/list`,
// `tools/call` over JSON-RPC 2.0 — and dispatches tool calls to a mcp.Catalog,
// the protocol-agnostic governance seam (the ToolTransport adapter boundary of
// ADR-0056). Swapping the wire implementation later stays behind this boundary.
//
// Auth: the MCP endpoint is gated by RequireCapability over the `agent-tool` edge
// (ADR-0046); agents present a PASETO Bearer token (audience `mcp-gateway`), never
// the browser cookie path. Operator-facing routes live on services/api (proxy),
// not here (ADR-0057) — the gateway has no browser surface.
package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/mcp"
)

// Config wires the gateway handler. Verifier gates the agent plane; Catalog is the
// governance seam (zero tools at T-035).
type Config struct {
	Verifier mw.Verifier
	Catalog  mcp.Catalog
}

// New builds the gateway HTTP handler.
func New(cfg Config) http.Handler {
	h := &handler{catalog: cfg.Catalog}
	r := chi.NewRouter()
	r.Get("/healthz", health)
	// The MCP endpoint: agent-tool edge only (ADR-0056). A missing/forged token
	// → 401; a valid token on the wrong edge (e.g. service-service) → 403.
	r.With(mw.RequireCapability(cfg.Verifier, "agent-tool")).Post("/mcp", h.serveMCP)
	return r
}

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type handler struct{ catalog mcp.Catalog }

// serveMCP decodes one JSON-RPC request and dispatches the MCP method. Requests
// carry an id and get a JSON-RPC response (HTTP 200, error-in-body per spec);
// notifications (no id) get 202 Accepted with no body.
func (h *handler) serveMCP(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, codeParseError, "parse error")
		return
	}
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, mcp.InitializeResult())
	case "tools/list":
		tools := h.catalog.List()
		if tools == nil {
			tools = []mcp.Tool{}
		}
		writeRPCResult(w, req.ID, map[string]any{"tools": tools})
	case "tools/call":
		h.toolsCall(w, r, req)
	default:
		writeRPCError(w, req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (h *handler) toolsCall(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
		writeRPCError(w, req.ID, codeInvalidParams, "tools/call requires a tool name")
		return
	}
	res, err := h.catalog.Invoke(r.Context(), p.Name, p.Arguments)
	if err != nil {
		// No tools are governed yet (T-035 skeleton); the governed Invoke +
		// fail-closed mapping land in T-039.
		writeRPCError(w, req.ID, codeInvalidParams, err.Error())
		return
	}
	writeRPCResult(w, req.ID, res)
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: jsonrpcVersion, ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
