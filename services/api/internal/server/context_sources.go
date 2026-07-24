package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"aireos.local/services/api/internal/ctxindex"
)

// contextSourceView is the wire shape for a context source (ContextSource; ADR-0066
// §1, T-100) — the memoryView precedent: PermissionScope (the owner) never rides the
// wire (implicit — every route is owner-scoped server-side).
type contextSourceView struct {
	ID            string     `json:"id"`
	RootPath      string     `json:"rootPath"`
	ScopeType     string     `json:"scopeType"`
	ScopeID       string     `json:"scopeId"`
	Status        string     `json:"status"`
	LastIndexedAt *time.Time `json:"lastIndexedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

func toContextSourceView(src ctxindex.Source) contextSourceView {
	return contextSourceView{
		ID: src.ID, RootPath: src.RootPath, ScopeType: src.ScopeType, ScopeID: src.ScopeID,
		Status: src.Status, LastIndexedAt: src.LastIndexedAt, CreatedAt: src.CreatedAt, UpdatedAt: src.UpdatedAt,
	}
}

// ListContextSources (GET /v1/context-sources) returns the caller's owner-scoped
// context sources (ADR-0066 §1), optionally narrowed to one charter via workstreamId.
// Deliberately NO scope-existence check on the filter (the GOV-7 memories precedent —
// reads are permission_scope-filtered, so a foreign/unknown workstreamId simply
// returns no rows, leaking nothing).
func (a *apiHandlers) ListContextSources(w http.ResponseWriter, r *http.Request, params ListContextSourcesParams) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.contextIndex == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the context-index store is not configured")
		return
	}
	scopeType, scopeID := "", ""
	if params.WorkstreamId != nil && *params.WorkstreamId != "" {
		scopeType, scopeID = ctxindex.ScopeWorkstream, *params.WorkstreamId
	}
	items, err := a.contextIndex.ListSources(r.Context(), operator, scopeType, scopeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "list context sources failed")
		return
	}
	views := make([]contextSourceView, 0, len(items))
	for _, src := range items {
		views = append(views, toContextSourceView(src))
	}
	writeJSON(w, http.StatusOK, views)
}

// createContextSourceBody is the register request (RegisterContextSourceRequest;
// ADR-0066 §1). Decoded by hand — the createMemoryBody precedent — with
// DisallowUnknownFields.
type createContextSourceBody struct {
	RootPath     string `json:"rootPath"`
	WorkstreamID string `json:"workstreamId"`
}

// CreateContextSource (POST /v1/context-sources) registers a charter-bound context
// source: human-agent + CSRF via the shared gate; the caller is ALWAYS the owner
// (server-side — SEC-31/GOV-7, never a body field). The target workstream must exist
// and be owned by the caller (ownedWorkstream, the existence-hiding 404 pattern) — a
// source can never be bound to someone else's charter. Registration is data-only:
// the filesystem root is deliberately NEVER stat'd/validated here — an unreadable root
// is marked errored on the indexer's next sweep (CTX-5), not rejected at registration.
func (a *apiHandlers) CreateContextSource(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.contextIndex == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the context-index store is not configured")
		return
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var body createContextSourceBody
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
		return
	}
	if body.RootPath == "" {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "rootPath is required")
		return
	}
	if body.WorkstreamID == "" {
		writeError(w, http.StatusUnprocessableEntity, "unprocessable", "workstreamId is required")
		return
	}

	// The target charter must exist and be owned by the caller — a foreign/unknown
	// workstream answers 404 (existence-hiding) BEFORE the source is ever created.
	if _, ok := a.ownedWorkstream(w, r, body.WorkstreamID); !ok {
		return
	}

	created, err := a.contextIndex.CreateSource(r.Context(), ctxindex.CreateSourceInput{
		Owner: operator, RootPath: body.RootPath, ScopeType: ctxindex.ScopeWorkstream, ScopeID: body.WorkstreamID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "register context source failed")
		return
	}
	writeJSON(w, http.StatusCreated, toContextSourceView(created))
}

// DeleteContextSource (DELETE /v1/context-sources/{id}) removes an owner-scoped
// context source: human-agent + CSRF via the shared gate. Its chunks cascade-delete in
// the same statement (CTX-5). 404 when the id is unknown or belongs to a different
// operator (the owner-404 house pattern, SEC-31/N3) — GOV-11: removal stops future
// agent-plane reads but cannot recall past ones.
func (a *apiHandlers) DeleteContextSource(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.contextIndex == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "the context-index store is not configured")
		return
	}
	if err := a.contextIndex.RemoveSource(r.Context(), operator, id); err != nil {
		if errors.Is(err, ctxindex.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "context source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", "remove context source failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
