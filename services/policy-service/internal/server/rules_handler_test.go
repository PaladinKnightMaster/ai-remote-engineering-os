package server_test

// rules_handler_test.go unit-tests the T-151a /v1/rules routes against the
// DB-free fakeStore (server_test.go): every route, the capability gate, and
// the typed-error → status mapping (400/404/409/500).

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"aireos.local/services/policy-service/internal/server"
	"aireos.local/services/policy-service/internal/store"
)

func ruleReqBody(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func doRules(t *testing.T, h http.Handler, method, path string, body []byte, withBearer bool) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if withBearer {
		req.Header.Set("Authorization", "Bearer t")
	}
	h.ServeHTTP(rec, req)
	return rec
}

// TestRulesRequireCapability — every /v1/rules route rejects a request with
// no bearer (401) before touching the store, same as /v1/decisions.
func TestRulesRequireCapability(t *testing.T) {
	h := server.New(server.Config{Verifier: allowVerifier{}})
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/rules"},
		{http.MethodGet, "/v1/rules"},
		{http.MethodGet, "/v1/rules/rule-1"},
		{http.MethodPatch, "/v1/rules/rule-1"},
		{http.MethodDelete, "/v1/rules/rule-1"},
	}
	for _, c := range cases {
		rec := doRules(t, h, c.method, c.path, []byte(`{}`), false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: no bearer: want 401, got %d", c.method, c.path, rec.Code)
		}
	}
}

func newRulesHandler() (http.Handler, *fakeStore) {
	fs := &fakeStore{}
	return server.New(server.Config{Verifier: allowVerifier{}, Store: fs}), fs
}

func createRuleBody(outcome string) []byte {
	return []byte(`{"label":"l","subject":"s","edge":"e","action":"a","target":"t","outcome":"` + outcome + `"}`)
}

// TestCreateRuleOK — 201 with the created rule, enabled by default.
func TestCreateRuleOK(t *testing.T) {
	h, _ := newRulesHandler()
	rec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeDeny), true)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rule: want 201, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ID      string `json:"id"`
		Outcome string `json:"outcome"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Outcome != store.RuleOutcomeDeny || !got.Enabled {
		t.Fatalf("unexpected create response: %+v", got)
	}
}

// TestCreateRuleInvalidMapsTo400 — ErrInvalidRule from the store maps to 400,
// with the store's reason in the body, not a generic message.
func TestCreateRuleInvalidMapsTo400(t *testing.T) {
	h, fs := newRulesHandler()
	fs.createRuleErr = store.ErrInvalidRule{Reason: "outcome bad"}
	rec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody("bogus"), true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid rule: want 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("outcome bad")) {
		t.Fatalf("400 body must carry the store's reason, got %q", rec.Body.String())
	}
}

// TestCreateRuleDuplicateMapsTo409 — a second create for the same tuple maps
// ErrDuplicateRule to 409. This is the mapping mutation-proven below.
func TestCreateRuleDuplicateMapsTo409(t *testing.T) {
	h, _ := newRulesHandler()
	doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeAllow), true)
	rec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeAllow), true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate rule: want 409, got %d (%s)", rec.Code, rec.Body.String())
	}
}

// TestCreateRuleBadBody — malformed JSON is 400 before the store is called.
func TestCreateRuleBadBody(t *testing.T) {
	h, fs := newRulesHandler()
	rec := doRules(t, h, http.MethodPost, "/v1/rules", []byte("not json"), true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body: want 400, got %d", rec.Code)
	}
	if len(fs.rules) != 0 {
		t.Fatalf("a malformed body must never reach the store")
	}
}

// TestListAndGetRule — list returns what was created; get returns one by id;
// get on an unknown id is 404 (ErrRuleNotFound mapping).
func TestListAndGetRule(t *testing.T) {
	h, _ := newRulesHandler()
	createRec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeAllow), true)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	listRec := doRules(t, h, http.MethodGet, "/v1/rules", nil, true)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", listRec.Code)
	}
	var list []map[string]any
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 rule listed, got %d", len(list))
	}

	getRec := doRules(t, h, http.MethodGet, "/v1/rules/"+created.ID, nil, true)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d (%s)", getRec.Code, getRec.Body.String())
	}

	missRec := doRules(t, h, http.MethodGet, "/v1/rules/does-not-exist", nil, true)
	if missRec.Code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", missRec.Code)
	}
}

// TestPatchRuleEnabled — PATCH flips enabled and returns the updated rule;
// a missing "enabled" field is 400; an unknown id is 404.
func TestPatchRuleEnabled(t *testing.T) {
	h, _ := newRulesHandler()
	createRec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeAllow), true)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	rec := doRules(t, h, http.MethodPatch, "/v1/rules/"+created.ID, ruleReqBody(t, map[string]bool{"enabled": false}), true)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Enabled {
		t.Fatalf("patch enabled=false: want disabled, got enabled=true")
	}

	missingFieldRec := doRules(t, h, http.MethodPatch, "/v1/rules/"+created.ID, []byte(`{}`), true)
	if missingFieldRec.Code != http.StatusBadRequest {
		t.Fatalf("patch with no enabled field: want 400, got %d", missingFieldRec.Code)
	}

	notFoundRec := doRules(t, h, http.MethodPatch, "/v1/rules/does-not-exist", ruleReqBody(t, map[string]bool{"enabled": true}), true)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("patch missing rule: want 404, got %d", notFoundRec.Code)
	}
}

// TestDeleteRule — 204 on success, 404 on an unknown id, and the rule is
// actually gone from the store afterward.
func TestDeleteRule(t *testing.T) {
	h, fs := newRulesHandler()
	createRec := doRules(t, h, http.MethodPost, "/v1/rules", createRuleBody(store.RuleOutcomeAllow), true)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(createRec.Body.Bytes(), &created)

	rec := doRules(t, h, http.MethodDelete, "/v1/rules/"+created.ID, nil, true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := fs.rules[created.ID]; ok {
		t.Fatalf("deleted rule still present in store")
	}

	notFoundRec := doRules(t, h, http.MethodDelete, "/v1/rules/"+created.ID, nil, true)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("delete already-deleted rule: want 404, got %d", notFoundRec.Code)
	}
}

// TestListRulesUnmappedErrorIs500 — an error that is none of the three typed
// store errors maps to 500, never leaking the raw error string to the caller.
func TestListRulesUnmappedErrorIs500(t *testing.T) {
	h, fs := newRulesHandler()
	fs.listRulesErr = errors.New("pq: connection reset by peer")
	rec := doRules(t, h, http.MethodGet, "/v1/rules", nil, true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unmapped store error: want 500, got %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("pq:")) {
		t.Fatalf("500 body must not leak the raw store error, got %q", rec.Body.String())
	}
}
