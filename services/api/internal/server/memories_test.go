package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/memory"
)

// fakeMemoryStore captures the last List/CreateAuthored call so tests can assert what
// the handler binds (owner, scope, limit, validated fields) without Postgres — the
// fakeWorkstreamStore shape.
type fakeMemoryStore struct {
	items []memory.Memory

	lastListOwner, lastListScopeType, lastListScopeID string
	lastListLimit                                     int

	lastCreate *memory.CreateInput
}

func (f *fakeMemoryStore) List(_ context.Context, owner, scopeType, scopeID string, limit int) ([]memory.Memory, error) {
	f.lastListOwner, f.lastListScopeType, f.lastListScopeID, f.lastListLimit = owner, scopeType, scopeID, limit
	return f.items, nil
}

func (f *fakeMemoryStore) CreateAuthored(_ context.Context, in memory.CreateInput) (memory.Memory, error) {
	cp := in
	f.lastCreate = &cp
	confidence := float32(1.0)
	if in.Confidence != nil {
		confidence = *in.Confidence
	}
	return memory.Memory{
		ID: "mem-00001", ScopeType: in.ScopeType, ScopeID: in.ScopeID, Key: in.Key,
		Content: in.Content, HasContent: true, SourceTrace: in.SourceTrace,
		Confidence: confidence, ExpiresAt: in.ExpiresAt, PermissionScope: in.Owner,
		CreatedAt: time.Now(),
	}, nil
}

func newMemoryHandler(fake *fakeMemoryStore) http.Handler {
	key, _ := auth.GenerateKey()
	return New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithMemoryStore(fake))
}

func getMemories(t *testing.T, h http.Handler, query string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/v1/memories?"+query, nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer x")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func postMemory(t *testing.T, h http.Handler, body string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/memories", strings.NewReader(body))
	if withAuth {
		r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	}
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func createBody(mut func(map[string]any)) string {
	m := map[string]any{
		"scopeType": "operator", "scopeId": "op-1", "content": "the nightly job flakes on Tuesdays",
	}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// TestListMemoriesHappy — a valid scope query answers 200 with the store's rows
// rendered onto the wire; the handler binds the caller as owner (never a param).
func TestListMemoriesHappy(t *testing.T) {
	now := time.Now()
	fake := &fakeMemoryStore{items: []memory.Memory{
		{ID: "m1", ScopeType: memory.ScopeRun, ScopeID: "run-1", Ref: "run:run-1", SourceTrace: "run:run-1:completed", Confidence: 1, CreatedAt: now},
		{ID: "m2", ScopeType: memory.ScopeRun, ScopeID: "run-1", HasContent: true, Content: "a note", SourceTrace: "operator", Confidence: 1, CreatedAt: now},
	}}
	h := newMemoryHandler(fake)
	rec := getMemories(t, h, "scopeType=run&scopeId=run-1", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got []memoryView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("items = %d, want 2", len(got))
	}
	if got[0].Content != nil {
		t.Fatalf("content-free row must not carry content, got %v", got[0].Content)
	}
	if got[1].Content == nil || *got[1].Content != "a note" {
		t.Fatalf("authored row must carry its content, got %v", got[1].Content)
	}
}

// TestListMemories400Matrix — an unknown scopeType is rejected before the store call;
// a missing required query param is rejected by the generated router wrapper itself
// (before ListMemories runs at all).
func TestListMemories400Matrix(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"unknown scopeType", "scopeType=bogus&scopeId=x"},
		{"missing scopeId (generated wrapper 400s first)", "scopeType=operator"},
		{"missing scopeType (generated wrapper 400s first)", "scopeId=x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeMemoryStore{}
			h := newMemoryHandler(fake)
			rec := getMemories(t, h, c.query, true)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestListMemories401Unauthenticated — no Authorization header → 401 before the
// handler is ever reached.
func TestListMemories401Unauthenticated(t *testing.T) {
	fake := &fakeMemoryStore{}
	h := newMemoryHandler(fake)
	rec := getMemories(t, h, "scopeType=operator&scopeId=op-1", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestCreateMemoryHappy — a valid authored write answers 201, echoing hasContent=true
// and the content back (the operator-only read round-trip); the owner is bound from
// the caller's capability, never accepted from the body.
func TestCreateMemoryHappy(t *testing.T) {
	fake := &fakeMemoryStore{}
	h := newMemoryHandler(fake)
	rec := postMemory(t, h, createBody(nil), true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastCreate == nil {
		t.Fatal("store never called")
	}
	if fake.lastCreate.Owner != "operator" {
		t.Fatalf("owner bound wrong: %q", fake.lastCreate.Owner)
	}
	var got memoryView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.HasContent || got.Content == nil || *got.Content != "the nightly job flakes on Tuesdays" {
		t.Fatalf("view wrong: %+v", got)
	}
}

// TestCreateMemory422Matrix — the D2 validation matrix: an unknown scopeType, empty
// content, out-of-range confidence, and a past expiresAt all answer 422 before the
// store is ever called.
func TestCreateMemory422Matrix(t *testing.T) {
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	cases := []struct {
		name string
		mut  func(map[string]any)
	}{
		{"unknown scopeType", func(m map[string]any) { m["scopeType"] = "bogus" }},
		{"empty scopeId", func(m map[string]any) { m["scopeId"] = "" }},
		{"empty content", func(m map[string]any) { m["content"] = "" }},
		{"confidence below 0", func(m map[string]any) { m["confidence"] = -0.1 }},
		{"confidence above 1", func(m map[string]any) { m["confidence"] = 1.1 }},
		{"expiresAt in the past", func(m map[string]any) { m["expiresAt"] = past }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeMemoryStore{}
			h := newMemoryHandler(fake)
			rec := postMemory(t, h, createBody(c.mut), true)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body=%s)", rec.Code, rec.Body.String())
			}
			if fake.lastCreate != nil {
				t.Fatal("store must not be called on a rejected write")
			}
		})
	}
}

// TestCreateMemory413Oversize — a body over the 64 KiB cap answers 413 (PERF-9), not a
// generic 400 decode error.
func TestCreateMemory413Oversize(t *testing.T) {
	fake := &fakeMemoryStore{}
	h := newMemoryHandler(fake)
	big := strings.Repeat("x", 65*1024)
	rec := postMemory(t, h, createBody(func(m map[string]any) { m["content"] = big }), true)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body=%s)", rec.Code, rec.Body.String())
	}
	if fake.lastCreate != nil {
		t.Fatal("store must not be called on an oversize body")
	}
}

// TestCreateMemory401Unauthenticated — no Authorization header → 401.
func TestCreateMemory401Unauthenticated(t *testing.T) {
	fake := &fakeMemoryStore{}
	h := newMemoryHandler(fake)
	rec := postMemory(t, h, createBody(nil), false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestMemoryRoutesUnconfigured — no WithMemoryStore → the routes exist but answer 503
// (dependency absent), not 404.
func TestMemoryRoutesUnconfigured(t *testing.T) {
	key, _ := auth.GenerateKey()
	h := New(newFake(), nil, auth.NewSigner(key, ""), allowAll{}, "", "")
	if rec := getMemories(t, h, "scopeType=operator&scopeId=op-1", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET: status=%d, want 503", rec.Code)
	}
	if rec := postMemory(t, h, createBody(nil), true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("POST: status=%d, want 503", rec.Code)
	}
}
