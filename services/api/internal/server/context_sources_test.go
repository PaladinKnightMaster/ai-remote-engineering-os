package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/ctxindex"
	"aireos.local/services/api/internal/workstream"
)

// newContextSourceHandler wires the context-index store (fakeContextStore, defined in
// adaptive_internal_test.go) alongside a workstream store, since CreateContextSource
// validates the target charter's ownership via ownedWorkstream (the workstreams_test.go
// fakeWorkstreamStore precedent). ws may be nil (an unconfigured workstream engine —
// CreateContextSource must then 503 before ever reaching the context store).
func newContextSourceHandler(cs *fakeContextStore, ws *fakeWorkstreamStore) http.Handler {
	key, _ := auth.GenerateKey()
	opts := []Option{}
	if cs != nil {
		opts = append(opts, WithContextStore(cs))
	}
	if ws != nil {
		opts = append(opts, WithWorkstreamStore(ws, 0))
	}
	return New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", opts...)
}

func getContextSources(t *testing.T, h http.Handler, query string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1/context-sources"
	if query != "" {
		url += "?" + query
	}
	r := httptest.NewRequest(http.MethodGet, url, nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer x")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func postContextSource(t *testing.T, h http.Handler, body string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/context-sources", strings.NewReader(body))
	if withAuth {
		r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	}
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func deleteContextSource(t *testing.T, h http.Handler, id string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodDelete, "/v1/context-sources/"+id, nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func registerBody(mut func(map[string]any)) string {
	m := map[string]any{"rootPath": "/srv/docs/proj", "workstreamId": "ws-1"}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// ownedWS returns a fakeWorkstreamStore pre-seeded with one charter ("ws-1") owned by
// "operator" (allowAll's subject) — the happy-path fixture every CreateContextSource
// test needing ownership to succeed reuses.
func ownedWS() *fakeWorkstreamStore {
	return &fakeWorkstreamStore{byID: map[string]workstream.Workstream{
		"ws-1": {ID: "ws-1", Principal: "operator"},
	}}
}

// --- GET /v1/context-sources ---------------------------------------------------------

// TestListContextSourcesHappy — an unfiltered list passes an empty scope through to the
// store; a workstreamId filter narrows it to (workstream, id) — the ListMemories
// owner-scoped-filter precedent.
func TestListContextSourcesHappy(t *testing.T) {
	fake := &fakeContextStore{items: []ctxindex.Source{
		{ID: "src-1", RootPath: "/srv/a", ScopeType: ctxindex.ScopeWorkstream, ScopeID: "ws-1", Status: ctxindex.StatusActive},
	}}
	h := newContextSourceHandler(fake, nil)

	rec := getContextSources(t, h, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastListScopeType != "" || fake.lastListScopeID != "" {
		t.Fatalf("unfiltered list must pass an empty scope, got scopeType=%q scopeId=%q", fake.lastListScopeType, fake.lastListScopeID)
	}
	var got []contextSourceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "src-1" {
		t.Fatalf("items = %+v, want one src-1 row", got)
	}

	rec2 := getContextSources(t, h, "workstreamId=ws-1", true)
	if rec2.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, want 200 (body=%s)", rec2.Code, rec2.Body.String())
	}
	if fake.lastListScopeType != ctxindex.ScopeWorkstream || fake.lastListScopeID != "ws-1" {
		t.Fatalf("filtered list scope = (%q, %q), want (workstream, ws-1)", fake.lastListScopeType, fake.lastListScopeID)
	}
	if fake.lastListOwner != "operator" {
		t.Fatalf("owner bound wrong: %q", fake.lastListOwner)
	}
}

// TestListContextSources401Unauthenticated — no Authorization header → 401.
func TestListContextSources401Unauthenticated(t *testing.T) {
	h := newContextSourceHandler(&fakeContextStore{}, nil)
	rec := getContextSources(t, h, "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- POST /v1/context-sources ---------------------------------------------------------

// TestCreateContextSourceHappy — a valid register against an owned charter answers 201;
// the owner is bound from the caller's capability (never the body), and the store is
// called with the charter-bound scope.
func TestCreateContextSourceHappy(t *testing.T) {
	fake := &fakeContextStore{}
	h := newContextSourceHandler(fake, ownedWS())

	rec := postContextSource(t, h, registerBody(nil), true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastCreate == nil {
		t.Fatal("store never called")
	}
	if fake.lastCreate.Owner != "operator" {
		t.Fatalf("owner bound wrong: %q", fake.lastCreate.Owner)
	}
	if fake.lastCreate.ScopeType != ctxindex.ScopeWorkstream || fake.lastCreate.ScopeID != "ws-1" {
		t.Fatalf("scope wrong: %+v", fake.lastCreate)
	}
	if fake.lastCreate.RootPath != "/srv/docs/proj" {
		t.Fatalf("rootPath wrong: %q", fake.lastCreate.RootPath)
	}
	var got contextSourceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.ScopeID != "ws-1" {
		t.Fatalf("view wrong: %+v", got)
	}
}

// TestCreateContextSourceForeignOrUnknownWorkstream404 — a workstreamId belonging to a
// different operator, or naming no charter at all, is rejected 404 (existence-hiding)
// BEFORE the context store is ever called — a source can never be bound to someone
// else's charter.
func TestCreateContextSourceForeignOrUnknownWorkstream404(t *testing.T) {
	t.Run("foreign owner", func(t *testing.T) {
		fake := &fakeContextStore{}
		ws := &fakeWorkstreamStore{byID: map[string]workstream.Workstream{
			"ws-1": {ID: "ws-1", Principal: "someone-else"},
		}}
		h := newContextSourceHandler(fake, ws)
		rec := postContextSource(t, h, registerBody(nil), true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if fake.lastCreate != nil {
			t.Fatal("store must not be called when the charter is foreign")
		}
	})
	t.Run("unknown workstream", func(t *testing.T) {
		fake := &fakeContextStore{}
		h := newContextSourceHandler(fake, &fakeWorkstreamStore{byID: map[string]workstream.Workstream{}})
		rec := postContextSource(t, h, registerBody(nil), true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if fake.lastCreate != nil {
			t.Fatal("store must not be called for an unknown charter")
		}
	})
}

// TestCreateContextSource422Matrix — an empty rootPath or workstreamId is rejected
// BEFORE the ownership check / store call.
func TestCreateContextSource422Matrix(t *testing.T) {
	cases := []struct {
		name string
		mut  func(map[string]any)
	}{
		{"empty rootPath", func(m map[string]any) { m["rootPath"] = "" }},
		{"empty workstreamId", func(m map[string]any) { m["workstreamId"] = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeContextStore{}
			h := newContextSourceHandler(fake, ownedWS())
			rec := postContextSource(t, h, registerBody(c.mut), true)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
			}
			if fake.lastCreate != nil {
				t.Fatal("store must not be called on a rejected write")
			}
		})
	}
}

// TestCreateContextSource401Unauthenticated — no Authorization header → 401.
func TestCreateContextSource401Unauthenticated(t *testing.T) {
	h := newContextSourceHandler(&fakeContextStore{}, ownedWS())
	rec := postContextSource(t, h, registerBody(nil), false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestCreateContextSourceCSRFRequired — a mutating request authenticated via a session
// cookie (the browser path) must carry a matching X-CSRF-Token (the memories/workstreams
// posture, gateByDeclaredSecurity); allowAll accepts any token value, so a bare session
// cookie is sufficient to exercise the CSRF gate without a real login round-trip.
func TestCreateContextSourceCSRFRequired(t *testing.T) {
	h := newContextSourceHandler(&fakeContextStore{}, ownedWS())
	r := httptest.NewRequest(http.MethodPost, "/v1/context-sources", strings.NewReader(registerBody(nil)))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "any-session-value"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (csrf missing, body=%s)", rec.Code, rec.Body.String())
	}
}

// TestContextSourceRoutesUnconfigured — no WithContextStore → the routes exist but
// answer 503 (dependency absent), not 404 — the memories-unconfigured precedent.
func TestContextSourceRoutesUnconfigured(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithWorkstreamStore(ownedWS(), 0))
	if rec := getContextSources(t, h, "", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET: status=%d, want 503", rec.Code)
	}
	if rec := postContextSource(t, h, registerBody(nil), true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST: status=%d, want 503", rec.Code)
	}
	if rec := deleteContextSource(t, h, "src-1", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("DELETE: status=%d, want 503", rec.Code)
	}
}

// --- DELETE /v1/context-sources/{id} ---------------------------------------------------

// TestDeleteContextSourceHappy — an owner-scoped remove answers 204.
func TestDeleteContextSourceHappy(t *testing.T) {
	fake := &fakeContextStore{}
	fake.seed(ctxindex.Source{ID: "src-1", PermissionScope: "operator"})
	h := newContextSourceHandler(fake, nil)

	rec := deleteContextSource(t, h, "src-1", true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.removeCalls != 1 {
		t.Fatalf("removeCalls = %d, want 1", fake.removeCalls)
	}
	if fake.lastRemoveOwner != "operator" || fake.lastRemoveID != "src-1" {
		t.Fatalf("remove called wrong: owner=%q id=%q", fake.lastRemoveOwner, fake.lastRemoveID)
	}
}

// TestDeleteContextSourceForeignOrUnknown404 — a foreign operator's id, or an unknown
// id, answers 404 (the house owner-404, SEC-31/N3); removeCalls stays 0 for the unknown
// case (never touched the store's delete path) and stays 0 for the foreign case too
// (the fake's guarded delete never flips on a mismatch).
func TestDeleteContextSourceForeignOrUnknown404(t *testing.T) {
	t.Run("foreign owner", func(t *testing.T) {
		fake := &fakeContextStore{}
		fake.seed(ctxindex.Source{ID: "src-1", PermissionScope: "someone-else"})
		h := newContextSourceHandler(fake, nil)
		rec := deleteContextSource(t, h, "src-1", true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
		if fake.removeCalls != 0 {
			t.Fatal("a foreign owner's delete must not flip the row")
		}
	})
	t.Run("unknown id", func(t *testing.T) {
		fake := &fakeContextStore{}
		h := newContextSourceHandler(fake, nil)
		rec := deleteContextSource(t, h, "does-not-exist", true)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// TestDeleteContextSource401Unauthenticated — no Authorization header → 401.
func TestDeleteContextSource401Unauthenticated(t *testing.T) {
	h := newContextSourceHandler(&fakeContextStore{}, nil)
	rec := deleteContextSource(t, h, "src-1", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestDeleteContextSourceCSRFRequired — the DELETE verb's CSRF gate mirrors the POST's
// (TestCreateContextSourceCSRFRequired).
func TestDeleteContextSourceCSRFRequired(t *testing.T) {
	fake := &fakeContextStore{}
	fake.seed(ctxindex.Source{ID: "src-1", PermissionScope: "operator"})
	h := newContextSourceHandler(fake, nil)
	r := httptest.NewRequest(http.MethodDelete, "/v1/context-sources/src-1", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "any-session-value"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (csrf missing, body=%s)", rec.Code, rec.Body.String())
	}
	if fake.removeCalls != 0 {
		t.Fatal("the CSRF-rejected request must never reach the store")
	}
}
