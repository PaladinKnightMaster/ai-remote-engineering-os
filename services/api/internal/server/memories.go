package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"aireos.local/services/api/internal/memory"
)

// maxMemoryContentBytes bounds the authored-write body (PERF-9: 64 KiB) — the
// T-041h MaxBytesReader precedent (services/mcp-gateway/internal/server/server.go).
// Unlike that precedent, an oversize body here maps to 413, not a generic 400 decode
// error (below): the cap is a documented API contract (openapi.v3.fragment.yaml),
// not just defense-in-depth.
const maxMemoryContentBytes = 64 << 10

// memoryView is the wire shape for a memory row (MemoryView; ADR-0064 §7) — the FULL
// v0.8.0 field set now, so the forgetting verbs (T-081) and the UI (T-082) land with
// zero further codegen churn.
type memoryView struct {
	ID          string     `json:"id"`
	ScopeType   string     `json:"scopeType"`
	ScopeID     string     `json:"scopeId"`
	Key         string     `json:"key,omitempty"`
	Ref         string     `json:"ref"`
	HasContent  bool       `json:"hasContent"`
	Content     *string    `json:"content,omitempty"`
	SourceTrace string     `json:"sourceTrace"`
	Confidence  float32    `json:"confidence"`
	Pinned      bool       `json:"pinned"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	RedactedAt  *time.Time `json:"redactedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// toMemoryView renders one store row onto the wire. content rides the wire ONLY when
// the row actually carries it (HasContent) AND it has not been tombstoned — redact
// (T-081) erases content from the pointer skeleton, so RedactedAt is checked
// independently rather than trusting HasContent alone once redaction lands.
func toMemoryView(m memory.Memory) memoryView {
	v := memoryView{
		ID: m.ID, ScopeType: m.ScopeType, ScopeID: m.ScopeID, Key: m.Key, Ref: m.Ref,
		HasContent: m.HasContent, SourceTrace: m.SourceTrace, Confidence: m.Confidence,
		Pinned: m.Pinned, ExpiresAt: m.ExpiresAt, RedactedAt: m.RedactedAt, CreatedAt: m.CreatedAt,
	}
	if m.HasContent && m.RedactedAt == nil {
		content := m.Content
		v.Content = &content
	}
	return v
}

// validMemoryScopeType reports whether s is one of the bounded scope set (ADR-0064
// §6, ratified R3): {workstream, run, operator}.
func validMemoryScopeType(s string) bool {
	switch s {
	case memory.ScopeWorkstream, memory.ScopeRun, memory.ScopeOperator:
		return true
	}
	return false
}

// ListMemories (GET /v1/memories) returns the caller's memories for one scope,
// owner-scoped (ADR-0064 §7). Deliberately NO scope-existence check (GOV-7): reads
// are permission_scope-filtered, so a foreign scope_id simply returns no rows,
// leaking nothing — the single-bootstrap-operator posture.
func (a *apiHandlers) ListMemories(w http.ResponseWriter, r *http.Request, params ListMemoriesParams) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the memory store is not configured")
		return
	}
	scopeType := string(params.ScopeType)
	if !validMemoryScopeType(scopeType) {
		writeError(w, http.StatusBadRequest, "bad_request", "scopeType must be one of workstream, run, operator")
		return
	}
	if params.ScopeId == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "scopeId is required")
		return
	}
	var limit int
	if params.Limit != nil {
		limit = *params.Limit // the store clamps to [1,200], defaulting to 50 for <=0 (D3)
	}
	items, err := a.memories.List(r.Context(), operator, scopeType, params.ScopeId, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list memories failed")
		return
	}
	views := make([]memoryView, 0, len(items))
	for _, m := range items {
		views = append(views, toMemoryView(m))
	}
	writeJSON(w, http.StatusOK, views)
}

// createMemoryBody is the authored-write request (ADR-0064 §2 secondary path).
// Decoded by hand — like CreateWorkstream, the generated MemoryCreate body model is
// not used (DisallowUnknownFields + a *float32 Confidence so an explicit 0 is
// distinguishable from "omitted").
type createMemoryBody struct {
	ScopeType   string     `json:"scopeType"`
	ScopeID     string     `json:"scopeId"`
	Key         string     `json:"key"`
	Content     string     `json:"content"`
	SourceTrace string     `json:"sourceTrace"`
	Confidence  *float32   `json:"confidence"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

// CreateMemory (POST /v1/memories) authors a free-text memory: human-agent + CSRF via
// the shared gate; the caller is ALWAYS the owner (server-side — SEC-31/GOV-7, never
// a body field). The body is capped at 64 KiB BEFORE decode (PERF-9); MaxBytesReader
// tripping is mapped to 413, distinct from an ordinary malformed-JSON 400. The 422
// matrix (D2) rejects an unknown scopeType, empty content, out-of-range confidence, or
// a non-future expiresAt — deliberately NO scope-existence check (GOV-7).
func (a *apiHandlers) CreateMemory(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the memory store is not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxMemoryContentBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body createMemoryBody
	if err := dec.Decode(&body); err != nil {
		var mbErr *http.MaxBytesError
		if errors.As(err, &mbErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "content exceeds the 64 KiB body cap")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}

	if !validMemoryScopeType(body.ScopeType) {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "scopeType must be one of workstream, run, operator")
		return
	}
	if body.ScopeID == "" {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "scopeId is required")
		return
	}
	if body.Content == "" {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "content is required")
		return
	}
	if body.Confidence != nil && (*body.Confidence < 0 || *body.Confidence > 1) {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "confidence must be in [0,1]")
		return
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(time.Now()) {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "expiresAt must be in the future")
		return
	}

	sourceTrace := body.SourceTrace
	if sourceTrace == "" {
		sourceTrace = "operator"
	}
	created, err := a.memories.CreateAuthored(r.Context(), memory.CreateInput{
		Owner: operator, ScopeType: body.ScopeType, ScopeID: body.ScopeID,
		Key: body.Key, Content: body.Content, SourceTrace: sourceTrace,
		Confidence: body.Confidence, ExpiresAt: body.ExpiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "create memory failed")
		return
	}
	writeJSON(w, http.StatusCreated, toMemoryView(created))
}

// ownedMemory gates the three T-081 forgetting verbs: unauthenticated → 401; the
// memory store unconfigured → 503; owner-scoped Get-then-flip (ADR-0064 §7) — the id
// unknown or belonging to a different operator → 404 (the house owner-404, SEC-31/N3)
// BEFORE the verb ever runs. Returns the caller's operator subject on success.
func (a *apiHandlers) ownedMemory(w http.ResponseWriter, r *http.Request, id string) (string, bool) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return "", false
	}
	if a.memories == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the memory store is not configured")
		return "", false
	}
	if _, err := a.memories.Get(r.Context(), operator, id); err != nil {
		if errors.Is(err, memory.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "memory not found")
		} else {
			writeError(w, http.StatusInternalServerError, "internal", "get memory failed")
		}
		return "", false
	}
	return operator, true
}

// RedactMemory (POST /v1/memories/{id}/redact) tombstones a memory: content is
// erased, chained as memory.redacted (content-free) — never the audit chain, casts, or
// prior exports (the R2 forgetting boundary, ADR-0064 §3). Idempotent 200 on a
// re-redact (D1, ratified): same desired end state, no duplicate event — NOT 409.
func (a *apiHandlers) RedactMemory(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := a.ownedMemory(w, r, id)
	if !ok {
		return
	}
	m, err := a.memories.Redact(r.Context(), operator, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "redact memory failed")
		return
	}
	writeJSON(w, http.StatusOK, toMemoryView(m))
}

// PinMemory (POST /v1/memories/{id}/pin) exempts a memory from the expiry sweep
// (ADR-0064 §3). Idempotent 200 when already pinned (D1) — NOT 409.
func (a *apiHandlers) PinMemory(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := a.ownedMemory(w, r, id)
	if !ok {
		return
	}
	m, err := a.memories.SetPinned(r.Context(), operator, id, true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "pin memory failed")
		return
	}
	writeJSON(w, http.StatusOK, toMemoryView(m))
}

// UnpinMemory (POST /v1/memories/{id}/unpin) makes a memory eligible for the expiry
// sweep again (ADR-0064 §3). Idempotent 200 when already unpinned (D1) — NOT 409.
func (a *apiHandlers) UnpinMemory(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := a.ownedMemory(w, r, id)
	if !ok {
		return
	}
	m, err := a.memories.SetPinned(r.Context(), operator, id, false)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "unpin memory failed")
		return
	}
	writeJSON(w, http.StatusOK, toMemoryView(m))
}
