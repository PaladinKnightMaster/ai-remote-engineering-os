package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"aireos.local/packages/go/auth"
	"aireos.local/services/api/internal/pairing"
)

// rulesPolicyServer stands up an httptest server simulating policy-service's
// internal /v1/rules plane (T-151a) closely enough to drive services/api's
// forwarding client (policy.Client, T-151b) through a real HTTP round trip —
// the same posture parkedHarness (server_test.go) already uses for
// decide/resolve. Mirrors rules_handler.go's validation and error mapping:
// an empty tuple field or a bad outcome -> 400, a colliding tuple -> 409, an
// unknown or malformed id -> 404. hits counts every request that reaches it,
// so a CON-7/SEC-53 test can assert api never even forwarded.
func rulesPolicyServer(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	type row struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		Subject   string `json:"subject"`
		Edge      string `json:"edge"`
		Action    string `json:"action"`
		Target    string `json:"target"`
		Outcome   string `json:"outcome"`
		Enabled   bool   `json:"enabled"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}
	rules := map[string]row{}
	var hits int32

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/rules", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Label, Subject, Edge, Action, Target, Outcome, AuthoredBy string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Subject == "" || body.Edge == "" || body.Action == "" || body.Target == "" {
			http.Error(w, `invalid policy rule: subject, edge, action, target must not be empty`, http.StatusBadRequest)
			return
		}
		switch body.Outcome {
		case "allow", "requires_approval", "deny":
		default:
			http.Error(w, fmt.Sprintf("invalid policy rule: outcome %q is not one of allow, requires_approval, deny", body.Outcome), http.StatusBadRequest)
			return
		}
		for _, ru := range rules {
			if ru.Subject == body.Subject && ru.Edge == body.Edge && ru.Action == body.Action && ru.Target == body.Target {
				http.Error(w, "a rule already exists for this (subject, edge, action, target) tuple", http.StatusConflict)
				return
			}
		}
		id, err := uuid.NewV7()
		if err != nil {
			http.Error(w, "mint id failed", http.StatusInternalServerError)
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		ru := row{
			ID: id.String(), Label: body.Label, Subject: body.Subject, Edge: body.Edge,
			Action: body.Action, Target: body.Target, Outcome: body.Outcome, Enabled: true,
			CreatedAt: now, UpdatedAt: now,
		}
		rules[ru.ID] = ru
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(ru)
	})
	mux.HandleFunc("GET /v1/rules", func(w http.ResponseWriter, r *http.Request) {
		out := make([]row, 0, len(rules))
		for _, ru := range rules {
			out = append(out, ru)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /v1/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		ru, ok := rules[r.PathValue("id")]
		if !ok {
			http.Error(w, "policy rule not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ru)
	})
	mux.HandleFunc("PATCH /v1/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		ru, ok := rules[id]
		if !ok {
			http.Error(w, "policy rule not found", http.StatusNotFound)
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		ru.Enabled = body.Enabled
		ru.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		rules[id] = ru
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ru)
	})
	mux.HandleFunc("DELETE /v1/rules/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if _, ok := rules[id]; !ok {
			http.Error(w, "policy rule not found", http.StatusNotFound)
			return
		}
		delete(rules, id)
		w.WriteHeader(http.StatusNoContent)
	})

	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		mux.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(counting)
	t.Cleanup(srv.Close)
	return srv, &hits
}

// rulesHarness wires a real api handler to rulesPolicyServer over a real
// PASETO signer/verifier pair (the pairedHandler precedent, pairing_test.go)
// so SEC-53's negative tests can mint real tokens of each capability edge —
// gateByDeclaredSecurity and requireHumanAgentRule both read the verified
// Capability, not a test double.
func rulesHarness(t *testing.T, opts ...Option) (http.Handler, *auth.Signer, *int32) {
	t.Helper()
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "cluster-test")
	verifier := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "cluster-test", "api"))
	pol, hits := rulesPolicyServer(t)
	h := New(newFake(), nil, signer, verifier, "", pol.URL, opts...)
	return h, signer, hits
}

// --- SEC-53: negative test per initiator class ------------------------------

// TestPolicyRulesAgentToolRejected — class 1: an agent-tool token is already
// rejected upstream of any rule-specific code, because gateByDeclaredSecurity's
// RequireCapability(v, "human-agent", "service-service") does not include
// agent-tool in its allowed set (server.go:521).
func TestPolicyRulesAgentToolRejected(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	tok, err := signer.IssueAgent("agent:wedge", "api", auth.Scope{})
	if err != nil {
		t.Fatalf("issue agent token: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/policy-rules", "", tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent-tool: want 403, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("agent-tool request reached policy-service: %d hits", got)
	}
}

// TestPolicyRulesRunSubjectRejected — class 3: a run/charter/grant subject
// acts through the gateway on an agent-tool token (SEC-53's audit: "act
// through the gateway on agent tokens & fall to the first case"). Same
// mechanism as the agent-tool test above, exercised with a run-shaped
// subject so the class is proven directly rather than only by inspection.
func TestPolicyRulesRunSubjectRejected(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	tok, err := signer.IssueAgent("run:run-00123", "api", auth.Scope{})
	if err != nil {
		t.Fatalf("issue agent token: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodPost, "/v1/policy-rules", validPolicyRuleBody, tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("run subject (agent token): want 403, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("run-subject request reached policy-service: %d hits", got)
	}
}

// TestPolicyRulesPairedDeviceRejected — class 2: a live paired (approvals@1)
// session is already rejected because pairingRouteActions is deny-by-default
// (pairing.go) and /v1/policy-rules is deliberately absent from that map.
func TestPolicyRulesPairedDeviceRejected(t *testing.T) {
	fp := newFakePairingStore()
	h, signer, hits := rulesHarness(t, WithPairingStore(fp), WithPairingEnabled(true))
	sessID := fp.seedLive("operator:mallory")
	tok, err := signer.IssuePaired("operator:mallory", "api", sessID, auth.Scope{Name: pairing.ApprovalsScopeV1})
	if err != nil {
		t.Fatalf("issue paired token: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/policy-rules", "", tok))
	if rec.Code < 400 {
		t.Fatalf("paired device: want a refusal, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("paired-device request reached policy-service: %d hits", got)
	}
}

// TestPolicyRulesServiceServiceRejected — class 4, THE gap SEC-53 closes.
// gateByDeclaredSecurity's RequireCapability allows service-service through
// (it is in the same allowed set as human-agent, server.go:521) — without
// requireHumanAgentRule's handler-level guard, mcp-gateway's service-service
// token would reach the rule-CRUD handlers. This is the mutation-proof
// target: removing the guard must make exactly this assertion fail.
func TestPolicyRulesServiceServiceRejected(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	tok, err := signer.IssueService("mcp-gateway", "api")
	if err != nil {
		t.Fatalf("issue service token: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, bearerRequest(http.MethodGet, "/v1/policy-rules", "", tok))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("SEC-53 FIRING ASSERTION: service-service on /v1/policy-rules: want 403, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("service-service request reached policy-service: %d hits", got)
	}
}

// --- CRUD, operator (human-agent) caller -------------------------------------

const validPolicyRuleBody = `{"label":"deny wedge exec","subject":"agent:wedge","edge":"agent-tool","action":"remote.exec","target":"host-01","outcome":"deny"}`

func operatorRulesRequest(t *testing.T, signer *auth.Signer, method, path, body string) *http.Request {
	t.Helper()
	tok, err := signer.IssueOperator("operator:wendy", "api")
	if err != nil {
		t.Fatalf("issue operator token: %v", err)
	}
	return bearerRequest(method, path, body, tok)
}

func TestCreatePolicyRule(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules", validPolicyRuleBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created policyRuleView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: bad body %s: %v", rec.Body, err)
	}
	if created.ID == "" || created.Subject != "agent:wedge" || created.Outcome != "deny" || !created.Enabled {
		t.Fatalf("create: unexpected view %+v", created)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("create: want exactly 1 forward to policy-service, got %d", got)
	}
}

func TestCreatePolicyRuleValidationForwarded(t *testing.T) {
	h, signer, _ := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules",
		`{"label":"x","subject":"","edge":"agent-tool","action":"remote.exec","target":"host-01","outcome":"deny"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create empty subject: want 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreatePolicyRuleDuplicateConflicts(t *testing.T) {
	h, signer, _ := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules", validPolicyRuleBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("first create: want 201, got %d: %s", rec.Code, rec.Body)
	}
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules", validPolicyRuleBody))
	if rec2.Code != http.StatusConflict {
		t.Fatalf("duplicate create: want 409, got %d: %s", rec2.Code, rec2.Body)
	}
}

func TestCreatePolicyRuleUnknownFieldRejected(t *testing.T) {
	h, signer, _ := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules",
		`{"label":"x","subject":"agent:wedge","edge":"agent-tool","action":"remote.exec","target":"host-01","outcome":"deny","id":"client-supplied"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create with client-supplied id: want 400, got %d: %s", rec.Code, rec.Body)
	}
}

func TestListCreateGetPatchDeletePolicyRule(t *testing.T) {
	h, signer, _ := rulesHarness(t)

	// empty list
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list (empty): want 200, got %d: %s", rec.Code, rec.Body)
	}
	var empty []policyRuleView
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil || len(empty) != 0 {
		t.Fatalf("list (empty): want [], got %s (err %v)", rec.Body, err)
	}

	// create
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPost, "/v1/policy-rules", validPolicyRuleBody))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", rec.Code, rec.Body)
	}
	var created policyRuleView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// list (one item)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules", ""))
	var listed []policyRuleView
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list (one): want 1 item, got %s (err %v)", rec.Body, err)
	}

	// get
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules/"+created.ID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d: %s", rec.Code, rec.Body)
	}

	// patch: disable
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPatch, "/v1/policy-rules/"+created.ID, `{"enabled":false}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch disable: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var patched policyRuleView
	_ = json.Unmarshal(rec.Body.Bytes(), &patched)
	if patched.Enabled {
		t.Fatalf("patch disable: still enabled: %+v", patched)
	}

	// patch: missing enabled -> 400 (fragment declares it required)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPatch, "/v1/policy-rules/"+created.ID, `{}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch missing enabled: want 400, got %d: %s", rec.Code, rec.Body)
	}

	// delete
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodDelete, "/v1/policy-rules/"+created.ID, ""))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d: %s", rec.Code, rec.Body)
	}

	// get after delete -> 404
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules/"+created.ID, ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: want 404, got %d: %s", rec.Code, rec.Body)
	}
}

func TestGetPolicyRuleUnknownWellFormedID(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules/01991a3e-0000-7000-8000-000000000000", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown well-formed id: want 404, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("unknown well-formed id: want exactly 1 forward to policy-service, got %d", got)
	}
}

// --- CON-7: a malformed id must not surface as a 500, and api answers 404 --
// BEFORE ever forwarding (the fast path — the whole point of validating
// api-side rather than relying solely on the store fix one hop further in).

func TestGetPolicyRuleMalformedIDNoForward(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodGet, "/v1/policy-rules/not-a-uuid", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("CON-7 get: want 404, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("CON-7 get: malformed id reached policy-service (%d hits) — api must answer 404 before forwarding", got)
	}
}

func TestPatchPolicyRuleMalformedIDNoForward(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodPatch, "/v1/policy-rules/not-a-uuid", `{"enabled":true}`))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("CON-7 patch: want 404, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("CON-7 patch: malformed id reached policy-service (%d hits)", got)
	}
}

func TestDeletePolicyRuleMalformedIDNoForward(t *testing.T) {
	h, signer, hits := rulesHarness(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, operatorRulesRequest(t, signer, http.MethodDelete, "/v1/policy-rules/not-a-uuid", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("CON-7 delete: want 404, got %d: %s", rec.Code, rec.Body)
	}
	if got := atomic.LoadInt32(hits); got != 0 {
		t.Fatalf("CON-7 delete: malformed id reached policy-service (%d hits)", got)
	}
}

// --- unconfigured (no POLICY_URL) -------------------------------------------

func TestPolicyRulesUnconfigured(t *testing.T) {
	h := New(newFake(), nil, nil, allowAll{}, "", "") // policyURL ""
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/policy-rules", ""},
		{http.MethodPost, "/v1/policy-rules", validPolicyRuleBody},
		{http.MethodGet, "/v1/policy-rules/01991a3e-0000-7000-8000-000000000000", ""},
		{http.MethodPatch, "/v1/policy-rules/01991a3e-0000-7000-8000-000000000000", `{"enabled":true}`},
		{http.MethodDelete, "/v1/policy-rules/01991a3e-0000-7000-8000-000000000000", ""},
	} {
		rec := req(t, h, tc.method, tc.path, tc.body, true)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s unconfigured: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}
}
