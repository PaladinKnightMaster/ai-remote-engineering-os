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
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/mcp"
)

// Config wires the gateway handler. Verifier gates BOTH planes — the agent plane
// (edge agent-tool) and the internal operator-proxy plane (edge service-service) —
// over the same audience "mcp-gateway"; the edge check is the boundary (ADR-0056).
// Catalog is the agent governance seam; Resolver is the operator resolve/list seam
// (nil → the internal routes are not mounted, mirroring the broker's nil-disables-exec).
type Config struct {
	Verifier   mw.Verifier
	Catalog    mcp.Catalog
	Resolver   mcp.ResolverPort
	StepRunner mcp.StepRunner
}

// New builds the gateway HTTP handler.
func New(cfg Config) http.Handler {
	h := &handler{catalog: cfg.Catalog, resolver: cfg.Resolver, stepRunner: cfg.StepRunner}
	r := chi.NewRouter()
	r.Get("/healthz", health)
	// The MCP endpoint: agent-tool edge only (ADR-0056). A missing/forged token
	// → 401; a valid token on the wrong edge (e.g. service-service) → 403.
	r.With(mw.RequireCapability(cfg.Verifier, "agent-tool")).Post("/mcp", h.serveMCP)
	// Internal service-service plane (ADR-0057 §4 / ADR-0061): services/api proxies the
	// operator's resolve/list here, and the operator Run engine (also api) drives a
	// governed step here. Both are subject-pinned to api (onlyAPI). The gateway has NO
	// browser surface — cookie/CSRF stay on api.
	if cfg.Resolver != nil || cfg.StepRunner != nil {
		r.Group(func(ir chi.Router) {
			ir.Use(mw.RequireCapability(cfg.Verifier, "service-service"))
			if cfg.Resolver != nil {
				ir.Get("/v1/invocations", h.listInvocations)
				ir.Post("/v1/invocations/{id}/resolve", h.resolveInvocation)
			}
			if cfg.StepRunner != nil {
				ir.Post("/v1/steps", h.runStep)
			}
		})
	}
	return r
}

// Request-body caps (defense in depth on the authenticated planes): the agent
// tools/call is small, the internal resolve body is tiny. MaxBytesReader bounds an
// authenticated-but-misbehaving caller's memory footprint.
const (
	maxMCPRequestBytes      = 1 << 20 // 1 MiB
	maxInternalRequestBytes = 1 << 16 // 64 KiB
)

func health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// onlyAPI restricts an internal operator-proxy call to services/api's service token
// (subject "api") — defense in depth on top of the service-service edge gate: only
// api legitimately proxies the operator's resolve/list here (ADR-0057 §4), so a
// different (or compromised) workload's audience-matched token cannot drive approvals.
func onlyAPI(w http.ResponseWriter, r *http.Request) bool {
	if capb, ok := mw.CapabilityFromContext(r.Context()); ok && capb.Subject == "api" {
		return true
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "the invocation plane is api-only"})
	return false
}

type handler struct {
	catalog    mcp.Catalog
	resolver   mcp.ResolverPort
	stepRunner mcp.StepRunner
}

// runStep (POST /v1/steps) drives ONE operator-initiated governed step (ADR-0061,
// T-055b). Service-service + api-only (the operator Run engine in services/api is the
// sole caller); body {subject, resourceId, command, idempotencyKey}. On success it
// returns the governed step's MCP ToolResult (200; isError marks a governance refusal
// the Run engine handles — denied/timeout); an infrastructure failure is 502
// (fail-closed — the step did not run).
func (h *handler) runStep(w http.ResponseWriter, r *http.Request) {
	if !onlyAPI(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxInternalRequestBytes)
	var body mcp.StepRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Subject == "" || body.ResourceID == "" || body.Command == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "subject, resourceId and command are required"})
		return
	}
	invID, res, err := h.stepRunner.RunStep(r.Context(), body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the governed step could not be driven"})
		return
	}
	// Return the invocation id alongside the ToolResult so the Run engine records the
	// run→invocation correlation (AUD-15, T-058a).
	writeJSON(w, http.StatusOK, stepResponse{InvocationID: invID, Content: res.Content, IsError: res.IsError})
}

// stepResponse is the /v1/steps reply: the governed step's invocation id + its MCP
// ToolResult (content + isError).
type stepResponse struct {
	InvocationID string        `json:"invocationId"`
	Content      []mcp.Content `json:"content"`
	IsError      bool          `json:"isError"`
}

// listInvocations (GET /v1/invocations) returns the parked invocations awaiting an
// operator decision (ADR-0057 §4). Service-service only; api proxies it to the
// operator console.
func (h *handler) listInvocations(w http.ResponseWriter, r *http.Request) {
	if !onlyAPI(w, r) {
		return
	}
	views, err := h.resolver.ListPending(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not list invocations"})
		return
	}
	if views == nil {
		views = []mcp.InvocationView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invocations": views})
}

// resolveInvocation (POST /v1/invocations/{id}/resolve) records the operator's
// grant/deny on a parked invocation (AP-2; ADR-0057 §4 / ADR-0059). Body
// {decision: "granted"|"denied", resolvedBy}. The gateway owns the reconcile: 404 for
// an unknown id, 409 if it is no longer pending (already resolved / policy timeout),
// 502 on a policy outage (the invocation stays parked).
func (h *handler) resolveInvocation(w http.ResponseWriter, r *http.Request) {
	if !onlyAPI(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	r.Body = http.MaxBytesReader(w, r.Body, maxInternalRequestBytes)
	var body struct {
		Decision   string `json:"decision"`
		ResolvedBy string `json:"resolvedBy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if body.Decision != mcp.DecisionGranted && body.Decision != mcp.DecisionDenied {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "decision must be granted or denied"})
		return
	}
	err := h.resolver.Resolve(r.Context(), id, body.Decision, body.ResolvedBy)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
	case errors.Is(err, mcp.ErrInvocationNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invocation not found"})
	case errors.Is(err, mcp.ErrApprovalNotPending):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "invocation is not awaiting approval"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "the policy decision point is unavailable"})
	}
}

// serveMCP decodes one JSON-RPC request and dispatches the MCP method. Requests
// carry an id and get a JSON-RPC response (HTTP 200, error-in-body per spec);
// notifications (no id) get 202 Accepted with no body.
func (h *handler) serveMCP(w http.ResponseWriter, r *http.Request) {
	// Carry the client's Idempotency-Key (if any) so the governed Invoke can dedup
	// retries (PERF-2) without widening the Catalog interface.
	if key := r.Header.Get("Idempotency-Key"); key != "" {
		r = r.WithContext(mcp.WithIdempotencyKey(r.Context(), key))
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMCPRequestBytes)
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
