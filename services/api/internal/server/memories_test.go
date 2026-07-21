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

// fakeMemoryStore captures the last List/CreateAuthored/Get/Redact/SetPinned call so
// tests can assert what the handler binds (owner, scope, limit, validated fields)
// without Postgres — the fakeWorkstreamStore shape. rows keys by id so Get/Redact/
// SetPinned can simulate the store's owner-scoped guarded-flip behavior in memory.
type fakeMemoryStore struct {
	items []memory.Memory
	rows  map[string]memory.Memory // id -> row, seeded via seed() for the verb tests

	lastListOwner, lastListScopeType, lastListScopeID string
	lastListLimit                                     int

	lastCreate *memory.CreateInput

	lastGetOwner, lastGetID             string
	redactCalls, pinCalls, shareCalls   int // counts events would-be-enqueued (idempotency assertions)
	lastSetPinnedOwner, lastSetID       string
	lastSetSharedOwner, lastSetSharedID string

	// T-090 fixtures: memory.read's store-side need (ReadMemory, adaptive_internal.go).
	readItems                         []memory.Memory
	readErr                           error
	lastReadAgent, lastReadWorkstream string
	lastReadLimit                     int
}

func (f *fakeMemoryStore) ReadForAgent(_ context.Context, agentSubject, workstreamID string, limit int) ([]memory.Memory, error) {
	f.lastReadAgent, f.lastReadWorkstream, f.lastReadLimit = agentSubject, workstreamID, limit
	return f.readItems, f.readErr
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

// seed installs a row directly into rows (bypassing List/CreateAuthored) so Get/
// Redact/SetPinned tests can start from a known owner-scoped state.
func (f *fakeMemoryStore) seed(m memory.Memory) {
	if f.rows == nil {
		f.rows = map[string]memory.Memory{}
	}
	f.rows[m.ID] = m
}

// Get mirrors the store's owner-scoped Get (SEC-31/N3): ErrNotFound when the id is
// unknown OR belongs to a different owner.
func (f *fakeMemoryStore) Get(_ context.Context, owner, id string) (memory.Memory, error) {
	f.lastGetOwner, f.lastGetID = owner, id
	m, ok := f.rows[id]
	if !ok || m.PermissionScope != owner {
		return memory.Memory{}, memory.ErrNotFound
	}
	return m, nil
}

// Redact mirrors the store's guarded, idempotent tombstone flip: a re-redact is a
// no-op (redactCalls only increments on an actual flip) but still returns 200 with
// the current (already-tombstoned) view — not an error.
func (f *fakeMemoryStore) Redact(_ context.Context, owner, id string) (memory.Memory, error) {
	m, ok := f.rows[id]
	if !ok || m.PermissionScope != owner {
		return memory.Memory{}, memory.ErrNotFound
	}
	if m.RedactedAt == nil {
		now := time.Now()
		m.RedactedAt = &now
		m.Content = ""
		m.HasContent = false
		f.redactCalls++
	}
	f.rows[id] = m
	return m, nil
}

// SetPinned mirrors the store's guarded, idempotent pin/unpin flip.
func (f *fakeMemoryStore) SetPinned(_ context.Context, owner, id string, pinned bool) (memory.Memory, error) {
	f.lastSetPinnedOwner, f.lastSetID = owner, id
	m, ok := f.rows[id]
	if !ok || m.PermissionScope != owner {
		return memory.Memory{}, memory.ErrNotFound
	}
	if m.Pinned != pinned {
		m.Pinned = pinned
		f.pinCalls++
	}
	f.rows[id] = m
	return m, nil
}

// SetShared mirrors the store's guarded, idempotent share/unshare flip (T-091), incl.
// the redacted_at IS NULL predicate: a tombstoned row's flip is a no-op (shareCalls
// does not increment) — the handler is expected to have already 409'd on RedactedAt
// before ever calling this, so tests exercise that via the handler, not this fake.
func (f *fakeMemoryStore) SetShared(_ context.Context, owner, id string, shared bool) (memory.Memory, error) {
	f.lastSetSharedOwner, f.lastSetSharedID = owner, id
	m, ok := f.rows[id]
	if !ok || m.PermissionScope != owner {
		return memory.Memory{}, memory.ErrNotFound
	}
	if m.RedactedAt == nil && m.AgentVisible != shared {
		m.AgentVisible = shared
		f.shareCalls++
	}
	f.rows[id] = m
	return m, nil
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
	if rec := postMemoryVerb(t, h, "mem-1", "redact", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("REDACT: status=%d, want 503", rec.Code)
	}
	if rec := postMemoryVerb(t, h, "mem-1", "pin", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("PIN: status=%d, want 503", rec.Code)
	}
	if rec := postMemoryVerb(t, h, "mem-1", "unpin", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("UNPIN: status=%d, want 503", rec.Code)
	}
	if rec := postMemoryVerb(t, h, "mem-1", "share", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("SHARE: status=%d, want 503", rec.Code)
	}
	if rec := postMemoryVerb(t, h, "mem-1", "unshare", true); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("UNSHARE: status=%d, want 503", rec.Code)
	}
}

// --- T-081/T-091: the forgetting + sharing verbs (redact / pin / unpin / share / unshare) ---

// postMemoryVerb POSTs /v1/memories/{id}/{verb} — the shared shape for redact/pin/unpin/share/unshare.
func postMemoryVerb(t *testing.T, h http.Handler, id, verb string, withAuth bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/v1/memories/"+id+"/"+verb, nil)
	if withAuth {
		r.Header.Set("Authorization", "Bearer x") // allowAll → operator/human-agent; header auth → no CSRF
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// TestMemoryVerbs401Unauthenticated — no Authorization header → 401 before the
// store is ever reached, for all five verbs.
func TestMemoryVerbs401Unauthenticated(t *testing.T) {
	for _, verb := range []string{"redact", "pin", "unpin", "share", "unshare"} {
		t.Run(verb, func(t *testing.T) {
			fake := &fakeMemoryStore{}
			h := newMemoryHandler(fake)
			rec := postMemoryVerb(t, h, "mem-1", verb, false)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// TestMemoryVerbs404ForeignOwner — a foreign operator's id, or an unknown id,
// answers 404 (the house owner-404, SEC-31/N3) BEFORE the verb ever runs; the store's
// Get is called (existence-check) but Redact/SetPinned/SetShared never are.
func TestMemoryVerbs404ForeignOwner(t *testing.T) {
	for _, verb := range []string{"redact", "pin", "unpin", "share", "unshare"} {
		t.Run(verb+"/foreign owner", func(t *testing.T) {
			fake := &fakeMemoryStore{}
			fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "someone-else"})
			h := newMemoryHandler(fake)
			rec := postMemoryVerb(t, h, "mem-1", verb, true)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
			}
			if fake.redactCalls != 0 || fake.pinCalls != 0 || fake.shareCalls != 0 {
				t.Fatal("the verb must not run when the owner-scoped Get 404s")
			}
		})
		t.Run(verb+"/unknown id", func(t *testing.T) {
			fake := &fakeMemoryStore{}
			h := newMemoryHandler(fake)
			rec := postMemoryVerb(t, h, "does-not-exist", verb, true)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body=%s)", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRedactMemoryHappy — a valid owner-scoped redact answers 200 with the
// tombstoned view: content gone, redactedAt set.
func TestRedactMemoryHappy(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", HasContent: true, Content: "a secret note"})
	h := newMemoryHandler(fake)

	rec := postMemoryVerb(t, h, "mem-1", "redact", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var got memoryView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RedactedAt == nil {
		t.Fatal("redactedAt must be set on the response")
	}
	if got.Content != nil {
		t.Fatalf("content must never ride the wire post-redact, got %v", *got.Content)
	}
	if fake.redactCalls != 1 {
		t.Fatalf("redactCalls = %d, want 1", fake.redactCalls)
	}
}

// TestRedactMemoryIdempotent — a second redact on an already-redacted row still
// answers 200 with the current (tombstoned) view — NOT 409 — and does not enqueue a
// second event (the fake's redactCalls only increments on an actual flip, D1/N1).
func TestRedactMemoryIdempotent(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", HasContent: true, Content: "a secret note"})
	h := newMemoryHandler(fake)

	first := postMemoryVerb(t, h, "mem-1", "redact", true)
	if first.Code != http.StatusOK {
		t.Fatalf("first redact: status = %d, want 200", first.Code)
	}
	second := postMemoryVerb(t, h, "mem-1", "redact", true)
	if second.Code != http.StatusOK {
		t.Fatalf("second (idempotent) redact: status = %d, want 200 (NOT 409)", second.Code)
	}
	if fake.redactCalls != 1 {
		t.Fatalf("redactCalls after two redacts = %d, want 1 (no duplicate event)", fake.redactCalls)
	}
}

// TestPinUnpinMemoryHappy — pin flips pinned=true; unpin flips it back; both answer
// 200 with the current view.
func TestPinUnpinMemoryHappy(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator"})
	h := newMemoryHandler(fake)

	pinRec := postMemoryVerb(t, h, "mem-1", "pin", true)
	if pinRec.Code != http.StatusOK {
		t.Fatalf("pin: status = %d, want 200 (body=%s)", pinRec.Code, pinRec.Body.String())
	}
	var pinned memoryView
	if err := json.Unmarshal(pinRec.Body.Bytes(), &pinned); err != nil {
		t.Fatalf("decode pin: %v", err)
	}
	if !pinned.Pinned {
		t.Fatal("pinned must be true after pin")
	}

	unpinRec := postMemoryVerb(t, h, "mem-1", "unpin", true)
	if unpinRec.Code != http.StatusOK {
		t.Fatalf("unpin: status = %d, want 200 (body=%s)", unpinRec.Code, unpinRec.Body.String())
	}
	var unpinned memoryView
	if err := json.Unmarshal(unpinRec.Body.Bytes(), &unpinned); err != nil {
		t.Fatalf("decode unpin: %v", err)
	}
	if unpinned.Pinned {
		t.Fatal("pinned must be false after unpin")
	}
	if fake.pinCalls != 2 {
		t.Fatalf("pinCalls = %d, want 2 (one flip each direction)", fake.pinCalls)
	}
}

// TestPinMemoryIdempotent — pinning an already-pinned row is a silent no-op (still
// 200, no duplicate event); same for unpinning an already-unpinned row.
func TestPinMemoryIdempotent(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", Pinned: true})
	h := newMemoryHandler(fake)

	rec := postMemoryVerb(t, h, "mem-1", "pin", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-pin: status = %d, want 200 (NOT 409)", rec.Code)
	}
	if fake.pinCalls != 0 {
		t.Fatalf("pinCalls = %d, want 0 (already pinned, no flip)", fake.pinCalls)
	}
}

// --- T-091: the share flip (share / unshare) ---

// TestShareUnshareMemoryHappy — share flips agentVisible=true; unshare flips it back;
// both answer 200 with the current view (MEM-6).
func TestShareUnshareMemoryHappy(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", HasContent: true, Content: "a note"})
	h := newMemoryHandler(fake)

	shareRec := postMemoryVerb(t, h, "mem-1", "share", true)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share: status = %d, want 200 (body=%s)", shareRec.Code, shareRec.Body.String())
	}
	var shared memoryView
	if err := json.Unmarshal(shareRec.Body.Bytes(), &shared); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if !shared.AgentVisible {
		t.Fatal("agentVisible must be true after share")
	}

	unshareRec := postMemoryVerb(t, h, "mem-1", "unshare", true)
	if unshareRec.Code != http.StatusOK {
		t.Fatalf("unshare: status = %d, want 200 (body=%s)", unshareRec.Code, unshareRec.Body.String())
	}
	var unshared memoryView
	if err := json.Unmarshal(unshareRec.Body.Bytes(), &unshared); err != nil {
		t.Fatalf("decode unshare: %v", err)
	}
	if unshared.AgentVisible {
		t.Fatal("agentVisible must be false after unshare")
	}
	if fake.shareCalls != 2 {
		t.Fatalf("shareCalls = %d, want 2 (one flip each direction)", fake.shareCalls)
	}
}

// TestShareMemoryIdempotent — sharing an already-shared row is a silent no-op (still
// 200, no duplicate event); same for unsharing an already-unshared row.
func TestShareMemoryIdempotent(t *testing.T) {
	fake := &fakeMemoryStore{}
	fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", HasContent: true, Content: "a note", AgentVisible: true})
	h := newMemoryHandler(fake)

	rec := postMemoryVerb(t, h, "mem-1", "share", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-share: status = %d, want 200 (NOT 409)", rec.Code)
	}
	if fake.shareCalls != 0 {
		t.Fatalf("shareCalls = %d, want 0 (already shared, no flip)", fake.shareCalls)
	}
}

// TestShareMemoryTombstoned409 — a redacted (tombstoned) row cannot be shared or
// unshared: the handler checks the fetched row's RedactedAt BEFORE ever calling the
// store's flip (D1) and answers 409 for both directions, never touching SetShared.
func TestShareMemoryTombstoned409(t *testing.T) {
	for _, verb := range []string{"share", "unshare"} {
		t.Run(verb, func(t *testing.T) {
			fake := &fakeMemoryStore{}
			now := time.Now()
			fake.seed(memory.Memory{ID: "mem-1", PermissionScope: "operator", RedactedAt: &now})
			h := newMemoryHandler(fake)
			rec := postMemoryVerb(t, h, "mem-1", verb, true)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body=%s)", rec.Code, rec.Body.String())
			}
			if fake.shareCalls != 0 {
				t.Fatal("SetShared must never be called for a tombstoned target")
			}
		})
	}
}
