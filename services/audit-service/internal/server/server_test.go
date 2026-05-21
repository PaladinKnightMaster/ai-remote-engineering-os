package server

import (
	"context"
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

func (f *fakeStore) List(_ context.Context, afterSeq int64, limit int) ([]audit.Event, int64, error) {
	var out []audit.Event
	var last int64
	for _, e := range f.order {
		if e.Seq > afterSeq {
			out = append(out, e)
			last = e.Seq
			if len(out) == limit {
				break
			}
		}
	}
	return out, last, nil
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
