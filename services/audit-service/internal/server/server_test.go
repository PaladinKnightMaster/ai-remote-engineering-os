package server

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/store"
)

// fakeStore is an in-memory Appender that mimics dedup + chain extension so the
// handler tests run without a database.
type fakeStore struct {
	byKey  map[string]audit.Event
	byID   map[string]audit.Event
	order  []audit.Event
	prev   string
	nextID int
}

func newFake() *fakeStore {
	return &fakeStore{byKey: map[string]audit.Event{}, byID: map[string]audit.Event{}, prev: chain.Genesis}
}

func (f *fakeStore) Append(_ context.Context, in audit.Ingest) (audit.Event, bool, error) {
	if e, ok := f.byKey[in.IdempotencyKey]; ok {
		return e, true, nil
	}
	f.nextID++
	e := audit.Event{
		Seq: int64(f.nextID), ID: "id-" + in.IdempotencyKey, Timestamp: in.Timestamp,
		Actor: in.Actor, Edge: in.Edge, Target: in.Target, Outcome: in.Outcome,
		EvidencePacketID: in.EvidencePacketID, IdempotencyKey: in.IdempotencyKey, PrevHash: f.prev,
	}
	e.ChainHash = chain.Compute(f.prev, e)
	f.prev = e.ChainHash
	f.byKey[in.IdempotencyKey] = e
	f.byID[e.ID] = e
	f.order = append(f.order, e)
	return e, false, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (audit.Event, error) {
	if e, ok := f.byID[id]; ok {
		return e, nil
	}
	return audit.Event{}, store.ErrNotFound
}

// List honors direction, the three equality filters, and the preset (T-106) —
// enough for handler-level tests of param mapping and the cursor guard. It is
// deliberately a simple linear scan, not a index-aware query planner; the
// real predicate composition and index-friendliness are proven against live
// Postgres in internal/store's integration suite (T-105), which this test
// double does not replace.
func (f *fakeStore) List(_ context.Context, q store.ListQuery) ([]audit.Event, int64, error) {
	matches := func(e audit.Event) bool {
		if q.Actor != nil && e.Actor != *q.Actor {
			return false
		}
		if q.Outcome != nil && e.Outcome != *q.Outcome {
			return false
		}
		if q.Edge != nil && e.Edge != *q.Edge {
			return false
		}
		if q.Preset && (e.Outcome == "plan.requested" || e.Outcome == "plan.expired") {
			return false
		}
		return true
	}
	var out []audit.Event
	var last int64
	if q.Direction == store.Desc {
		cursor := q.Cursor
		if cursor == 0 {
			cursor = math.MaxInt64
		}
		for i := len(f.order) - 1; i >= 0; i-- {
			e := f.order[i]
			if e.Seq < cursor && matches(e) {
				out = append(out, e)
				last = e.Seq
				if len(out) == q.Limit {
					break
				}
			}
		}
		return out, last, nil
	}
	for _, e := range f.order {
		if e.Seq > q.Cursor && matches(e) {
			out = append(out, e)
			last = e.Seq
			if len(out) == q.Limit {
				break
			}
		}
	}
	return out, last, nil
}

// ChainHead mirrors store.Store.ChainHead: MAX(seq) over the in-memory
// order, or 0 for an empty chain (T-106, AUD-29).
func (f *fakeStore) ChainHead(_ context.Context) (int64, error) {
	if len(f.order) == 0 {
		return 0, nil
	}
	return f.order[len(f.order)-1].Seq, nil
}

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "test", Edge: "service-service"}, nil
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const validBody = `{"timestamp":"2026-05-20T00:00:00Z","actor":"svc:policy-service","edge":"service-service","target":"decision:1","idempotencyKey":"018f5e2a-0000-7000-8000-000000000010"}`

func TestAppendAssignsChainFields(t *testing.T) {
	h := New(newFake(), allowAll{})
	rec := do(t, h, http.MethodPost, "/v1/events", validBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"prevHash":"`+chain.Genesis+`"`) {
		t.Fatalf("expected genesis prevHash assigned by server, got %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"chainHash":"`) {
		t.Fatal("expected server-assigned chainHash")
	}
}

// A client-supplied chain field must be rejected with 400 — a forged-chain
// attempt cannot enter the pipeline (DisallowUnknownFields).
func TestRejectClientSuppliedChainFields(t *testing.T) {
	h := New(newFake(), allowAll{})
	for _, forged := range []string{
		`{"timestamp":"2026-05-20T00:00:00Z","actor":"a","edge":"service-service","target":"t","idempotencyKey":"k","chainHash":"deadbeef"}`,
		`{"timestamp":"2026-05-20T00:00:00Z","actor":"a","edge":"service-service","target":"t","idempotencyKey":"k","prevHash":"00"}`,
		`{"timestamp":"2026-05-20T00:00:00Z","actor":"a","edge":"service-service","target":"t","idempotencyKey":"k","id":"forced"}`,
	} {
		rec := do(t, h, http.MethodPost, "/v1/events", forged)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("forged body %q: want 400, got %d", forged, rec.Code)
		}
	}
}

// Replaying an idempotencyKey returns the same event and does not grow the chain.
func TestDedupOnReplay(t *testing.T) {
	f := newFake()
	h := New(f, allowAll{})
	first := do(t, h, http.MethodPost, "/v1/events", validBody)
	second := do(t, h, http.MethodPost, "/v1/events", validBody)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("want 200/200, got %d/%d", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay returned a different event:\n%s\n%s", first.Body, second.Body)
	}
	if len(f.order) != 1 {
		t.Fatalf("dedup should not extend the chain; got %d events", len(f.order))
	}
}

// End-to-end with a real PASETO verifier (ADR-0046 §Verification item 4): a
// service-service token minted for this service's audience passes
// RequireCapability and reaches the handler; a token for a different audience is
// rejected with 401.
func TestRealPasetoServiceToken(t *testing.T) {
	key, _ := auth.GenerateKey()
	const cluster = "cluster-test"
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, cluster, "audit-service"))
	h := New(newFake(), verifier)
	signer := auth.NewSigner(key, cluster)

	good, _ := signer.IssueService("svc:policy-service", "audit-service")
	req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(validBody))
	req.Header.Set("Authorization", "Bearer "+good)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid service token: want 200, got %d: %s", rec.Code, rec.Body)
	}

	wrongAud, _ := signer.IssueService("svc:policy-service", "policy-service")
	req2 := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req2.Header.Set("Authorization", "Bearer "+wrongAud)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-audience token: want 401, got %d", rec2.Code)
	}
}

func TestUnauthorizedWithoutBearer(t *testing.T) {
	h := New(newFake(), allowAll{})
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil) // no Authorization
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without bearer, got %d", rec.Code)
	}
}

// mustAppend appends one event with the given actor/edge/outcome directly on
// the fake, bypassing the HTTP layer, so list tests can seed a chain with a
// controlled shape (mixed actors/outcomes) cheaply.
func mustAppend(t *testing.T, f *fakeStore, actor, edge, target, outcome string) audit.Event {
	t.Helper()
	e, _, err := f.Append(context.Background(), audit.Ingest{
		Timestamp:      "2026-05-20T00:00:00Z",
		Actor:          actor,
		Edge:           edge,
		Target:         target,
		Outcome:        outcome,
		IdempotencyKey: fmt.Sprintf("k-%d", f.nextID+1),
	})
	if err != nil {
		t.Fatalf("seed append: %v", err)
	}
	return e
}

// TestListEventsParamMapping exercises T-106's param plumbing end to end
// through the real handler (New + ServeHTTP), not buildListQuery directly:
// direction, actor/outcome/edge filters, and the preset all reach
// store.ListQuery, and chainHead is always present in the envelope.
func TestListEventsParamMapping(t *testing.T) {
	f := newFake()
	mustAppend(t, f, "agent:composer", "human-agent", "t1", "plan.requested")
	mustAppend(t, f, "svc:policy-service", "service-service", "t2", "decision.granted")
	mustAppend(t, f, "agent:composer", "agent-tool", "t3", "plan.expired")
	h := New(f, allowAll{})

	// Default direction is asc (ADR-0067 §D2) and chainHead is always present.
	rec := do(t, h, http.MethodGet, "/v1/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"chainHead":3`) {
		t.Fatalf("expected chainHead:3 always populated, got %s", body)
	}
	if idx1, idx3 := strings.Index(body, `"id":"id-k-1"`), strings.Index(body, `"id":"id-k-3"`); idx1 == -1 || idx3 == -1 || idx1 > idx3 {
		t.Fatalf("default direction should be asc (k-1 before k-3), got %s", body)
	}

	// direction=desc walks newest-first.
	rec = do(t, h, http.MethodGet, "/v1/events?direction=desc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("desc: want 200, got %d: %s", rec.Code, rec.Body)
	}
	items := rec.Body.String()
	if idx1, idx3 := strings.Index(items, `"id":"id-k-1"`), strings.Index(items, `"id":"id-k-3"`); idx3 == -1 || idx1 == -1 || idx3 > idx1 {
		t.Fatalf("desc should list k-3 before k-1, got %s", items)
	}

	// actor filter.
	rec = do(t, h, http.MethodGet, "/v1/events?actor=agent%3Acomposer", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "k-2") {
		t.Fatalf("actor filter should exclude svc:policy-service's event, got %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"actor":"agent:composer"`) {
		t.Fatalf("disclosure envelope should echo the applied actor filter, got %s", rec.Body)
	}

	// edge filter.
	rec = do(t, h, http.MethodGet, "/v1/events?edge=agent-tool", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "k-3") || strings.Contains(rec.Body.String(), `"id":"id-k-1"`) {
		t.Fatalf("edge filter should return only the agent-tool event, got %d %s", rec.Code, rec.Body)
	}

	// preset excludes plan.requested/plan.expired and reports its name.
	rec = do(t, h, http.MethodGet, "/v1/events?preset=governed-actions%401", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("preset: want 200, got %d: %s", rec.Code, rec.Body)
	}
	presetBody := rec.Body.String()
	if strings.Contains(presetBody, "k-1") || strings.Contains(presetBody, `"id":"id-k-3"`) {
		t.Fatalf("preset should exclude plan.requested/plan.expired outcomes, got %s", presetBody)
	}
	if !strings.Contains(presetBody, `"preset":"governed-actions@1"`) {
		t.Fatalf("disclosure envelope should echo the applied preset name, got %s", presetBody)
	}
}

// TestListEventsBadDirectionAndPreset asserts the 400 shape for an unknown
// direction or preset value, matching how limit/cursor already validate.
func TestListEventsBadDirectionAndPreset(t *testing.T) {
	h := New(newFake(), allowAll{})
	for _, path := range []string{
		"/v1/events?direction=sideways",
		"/v1/events?preset=unknown-lens@1",
	} {
		rec := do(t, h, http.MethodGet, path, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d: %s", path, rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"bad_request"`) {
			t.Fatalf("%s: expected bad_request error code, got %s", path, rec.Body)
		}
	}
}

// TestListEventsCursorGuardBothDirections proves the full-page nextCursor
// guard (kept verbatim from before T-106) holds in BOTH directions: a
// partial page and an empty page must never emit nextCursor. This matters
// more after T-106 than before it, because store.List returns cursor 0 for
// an empty page, and in Desc that value is the "no cursor" sentinel — fed
// back it would restart the walk from the head instead of terminating.
func TestListEventsCursorGuardBothDirections(t *testing.T) {
	f := newFake()
	mustAppend(t, f, "a", "service-service", "t1", "")
	mustAppend(t, f, "a", "service-service", "t2", "")
	h := New(f, allowAll{})

	// Ascending: a partial page (2 events, limit 50) must carry no cursor.
	rec := do(t, h, http.MethodGet, "/v1/events?limit=50", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "nextCursor") {
		t.Fatalf("asc partial page must not emit nextCursor, got %d %s", rec.Code, rec.Body)
	}

	// Descending: an empty page (cursor before the oldest event) must carry
	// no cursor either — this is the exact case the guard exists to prevent
	// from looping back to the head.
	rec = do(t, h, http.MethodGet, "/v1/events?direction=desc&cursor=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("desc empty page: want 200, got %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "nextCursor") {
		t.Fatalf("desc empty page must not emit nextCursor, got %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"items":null`) && !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Fatalf("expected an empty items page, got %s", rec.Body)
	}

	// Descending: a full page (limit=1) DOES emit a cursor.
	rec = do(t, h, http.MethodGet, "/v1/events?direction=desc&limit=1", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"nextCursor":"2"`) {
		t.Fatalf("desc full page should emit nextCursor:2, got %d %s", rec.Code, rec.Body)
	}
}
