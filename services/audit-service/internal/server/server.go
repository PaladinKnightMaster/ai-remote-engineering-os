// Package server wires the audit-service HTTP surface (ADR-0043). It is hand-
// authored with plain chi routes because oapi-codegen could not run in the
// authoring environment (toolchain gap, see oapi-codegen.yaml). Once `task
// codegen` is run, these handlers should be re-seated on the generated
// ServerInterface; the request/response shapes already match the contract.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/store"
)

// Appender is the narrow slice of *store.Store the handlers need, so tests can
// substitute an in-memory fake without a database.
type Appender interface {
	Append(ctx context.Context, in audit.Ingest) (audit.Event, bool, error)
	Get(ctx context.Context, id string) (audit.Event, error)
	List(ctx context.Context, afterSeq int64, limit int) ([]audit.Event, int64, error)
}

// New returns the audit-service router. The POST/GET /v1/events surface is
// gated by RequireCapability over the service-service edge (ADR-0045: producers
// fan in over that edge); v is the capability Verifier port (the real PASETO
// verifier lands in T-008).
func New(s Appender, v mw.Verifier) http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireCapability(v, "service-service", "human-agent"))
		r.Post("/v1/events", appendEvent(s))
		r.Get("/v1/events", listEvents(s))
		r.Get("/v1/events/{id}", getEvent(s))
	})
	return r
}

func appendEvent(s Appender) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		dec := json.NewDecoder(req.Body)
		dec.DisallowUnknownFields() // rejects client-supplied id/prevHash/chainHash → 400
		var in audit.Ingest
		if err := dec.Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}
		if err := in.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		e, _, err := s.Append(req.Context(), in)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "append failed")
			return
		}
		// 200 for both fresh append and dedup replay (idempotent, ADR-0045).
		writeJSON(w, http.StatusOK, e)
	}
}

type eventPage struct {
	Items []audit.Event `json:"items"`
	Page  pageMeta      `json:"page"`
}

type pageMeta struct {
	NextCursor string `json:"nextCursor,omitempty"`
}

func listEvents(s Appender) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		limit := 50
		if v := req.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 200 {
				writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer in [1,200]")
				return
			}
			limit = n
		}
		var afterSeq int64
		if c := req.URL.Query().Get("cursor"); c != "" {
			n, err := strconv.ParseInt(c, 10, 64)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "invalid cursor")
				return
			}
			afterSeq = n
		}
		items, last, err := s.List(req.Context(), afterSeq, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "list failed")
			return
		}
		page := eventPage{Items: items}
		if len(items) == limit { // a full page implies there may be more
			page.Page.NextCursor = strconv.FormatInt(last, 10)
		}
		writeJSON(w, http.StatusOK, page)
	}
}

func getEvent(s Appender) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		e, err := s.Get(req.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "audit event not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "get failed")
			return
		}
		writeJSON(w, http.StatusOK, e)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}
