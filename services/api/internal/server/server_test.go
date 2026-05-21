package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/resource"
	"aireos.local/services/api/internal/store"
)

// fakeStore is an in-memory Store for handler tests.
type fakeStore struct {
	byID map[string]resource.Resource
	ids  []string
	n    int
}

func newFake() *fakeStore { return &fakeStore{byID: map[string]resource.Resource{}} }

func (f *fakeStore) Create(_ context.Context, in resource.Input) (resource.Resource, error) {
	f.n++
	id := fmt.Sprintf("res-%05d", f.n)
	r := resource.Resource{ID: id, Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}
	f.byID[id] = r
	f.ids = append(f.ids, id)
	return r, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (resource.Resource, error) {
	if r, ok := f.byID[id]; ok {
		return r, nil
	}
	return resource.Resource{}, store.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, after string, limit int) ([]resource.Resource, string, error) {
	var out []resource.Resource
	var last string
	for _, id := range f.ids {
		if id <= after {
			continue
		}
		out = append(out, f.byID[id])
		last = id
		if len(out) == limit {
			break
		}
	}
	return out, last, nil
}

func (f *fakeStore) Update(_ context.Context, id string, in resource.Input) (resource.Resource, error) {
	if _, ok := f.byID[id]; !ok {
		return resource.Resource{}, store.ErrNotFound
	}
	r := resource.Resource{ID: id, Type: in.Type, Name: in.Name, SiteID: in.SiteID, Labels: in.Labels}
	f.byID[id] = r
	return r, nil
}

func (f *fakeStore) Delete(_ context.Context, id string) error {
	if _, ok := f.byID[id]; !ok {
		return store.ErrNotFound
	}
	delete(f.byID, id)
	return nil
}

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "operator", Edge: "human-agent"}, nil
}

func req(t *testing.T, h http.Handler, method, path, body string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if auth {
		r.Header.Set("Authorization", "Bearer test")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

const validBody = `{"type":"ssh","name":"edge-01","siteId":"eu","labels":{"env":"prod"}}`

func TestCreateGetUpdateDelete(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{})

	// create
	rec := req(t, h, http.MethodPost, "/v1/resources", validBody, true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"id":"res-00001"`) {
		t.Fatalf("create: expected server-assigned id, got %s", rec.Body)
	}

	// get
	if rec := req(t, h, http.MethodGet, "/v1/resources/res-00001", "", true); rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rec.Code)
	}
	// get missing
	if rec := req(t, h, http.MethodGet, "/v1/resources/nope", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", rec.Code)
	}

	// update
	if rec := req(t, h, http.MethodPut, "/v1/resources/res-00001", `{"type":"vnc","name":"e2"}`, true); rec.Code != http.StatusOK {
		t.Fatalf("update: want 200, got %d", rec.Code)
	}
	// update missing
	if rec := req(t, h, http.MethodPut, "/v1/resources/nope", `{"type":"vnc","name":"e2"}`, true); rec.Code != http.StatusNotFound {
		t.Fatalf("update missing: want 404, got %d", rec.Code)
	}

	// delete
	if rec := req(t, h, http.MethodDelete, "/v1/resources/res-00001", "", true); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rec.Code)
	}
	if rec := req(t, h, http.MethodDelete, "/v1/resources/res-00001", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("delete twice: want 404, got %d", rec.Code)
	}
}

func TestListReturnsItems(t *testing.T) {
	f := newFake()
	h := New(f, nil, nil, allowAll{})
	for i := 0; i < 3; i++ {
		req(t, h, http.MethodPost, "/v1/resources", validBody, true)
	}
	rec := req(t, h, http.MethodGet, "/v1/resources", "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"items"`) || strings.Count(rec.Body.String(), `"id"`) != 3 {
		t.Fatalf("list: expected 3 items, got %s", rec.Body)
	}
}

// Validation: bad type and client-supplied id are both 400.
func TestCreateValidation(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{})
	for _, body := range []string{
		`{"type":"telnet","name":"x"}`,            // type not in enum
		`{"name":"x"}`,                            // missing type
		`{"type":"ssh","name":"x","id":"forced"}`, // client-supplied id rejected (DisallowUnknownFields)
	} {
		if rec := req(t, h, http.MethodPost, "/v1/resources", body, true); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: want 400, got %d", body, rec.Code)
		}
	}
}

func TestUnauthorizedWithoutBearer(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{})
	if rec := req(t, h, http.MethodGet, "/v1/resources", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without bearer, got %d", rec.Code)
	}
}

// End-to-end operator login: real key/signer/account/verifier. Login is public
// (no bearer); a valid login yields a token that GET /v1/auth/me accepts.
func TestOperatorLoginAndMe(t *testing.T) {
	key, _ := auth.GenerateKey()
	const cluster = "cluster-test"
	hash, _ := auth.HashPassword("hunter2")
	op := account.New("admin", hash)
	signer := auth.NewSigner(key, cluster)
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, cluster, "api"))
	h := New(newFake(), op, signer, verifier)

	// login is public (no bearer sent)
	rec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var lr struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lr); err != nil || lr.Token == "" {
		t.Fatalf("login response missing token: %s", rec.Body)
	}

	// the issued token is accepted by GET /v1/auth/me
	meReq := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+lr.Token)
	meRec := httptest.NewRecorder()
	h.ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("me: want 200, got %d: %s", meRec.Code, meRec.Body)
	}
	if !strings.Contains(meRec.Body.String(), `"subject":"operator:admin"`) || !strings.Contains(meRec.Body.String(), `"edge":"human-agent"`) {
		t.Fatalf("me: unexpected identity: %s", meRec.Body)
	}

	// wrong password → 401
	if rec := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"nope"}`, false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: want 401, got %d", rec.Code)
	}
	// /v1/auth/me without a token → 401
	if rec := req(t, h, http.MethodGet, "/v1/auth/me", "", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me without token: want 401, got %d", rec.Code)
	}
}
