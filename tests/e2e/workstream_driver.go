//go:build e2e || wd1

// Shared chartered-Workstream HTTP driver helpers (ADR-0063) — used by the compose e2e
// WD-7 capstone (wd7_test.go, tag e2e) and, at T-077/OI-14, the installed-stack WD-7
// driver (wd1_test.go, tag wd1). HTTP-only (no DB), so they work from the external wd1
// driver container that reaches the stack solely through Caddy HTTPS (the
// delegated_driver.go / run_driver.go pattern).

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// charterStep is one template step of a workstream charter.
type charterStep struct {
	resourceID string
	command    string
}

// charterOpts parameterizes a charter (the fixed-bounds issueGrant shape won't do: the
// recurrence test needs a generous budget, and N3 needs a tight budget + a window).
// kind/composer are the v0.9 adaptive-charter fields (ADR-0065 §4, T-091's fragment
// addition): kind "" (omitted) → the api defaults to "template" (byte-behavior-preserved
// for every pre-v0.9 caller of this helper); kind "adaptive" requires composer set and
// steps left empty (the store's kind matrix, WS-5).
type charterOpts struct {
	objective       string
	resources       []string
	steps           []charterStep
	intervalSeconds int
	stepBudget      int
	budgetWindowSec int    // 0 → omit (the api defaults it to the interval)
	kind            string // "" | "template" | "adaptive" (WS-5, ADR-0065 §4); "" → the store defaults to template
	composer        string // required for kind=adaptive (the auto-issued grant's grantee, R2); "" for template
}

// workstreamView is the operator-facing charter shape (GET /v1/workstreams/{id} + the
// charter response); grantId is the auto-issued grant (revocable via /v1/grants for the
// self-retire leg), status the lifecycle state. Kind/Composer are the v0.9 adaptive
// fields (WS-5, ADR-0065 §4) — Kind is always present ("template" | "adaptive"),
// Composer only for an adaptive charter.
type workstreamView struct {
	ID         string `json:"id"`
	GrantID    string `json:"grantId"`
	Status     string `json:"status"`
	NextFireAt string `json:"nextFireAt"`
	Kind       string `json:"kind"`
	Composer   string `json:"composer,omitempty"`
	// PlanRequests is the charter's bounded plan-request history (D3, T-091) — set by
	// GetWorkstream only (server.go's toWorkstreamView convention); absent on the
	// charter POST response and on list items.
	PlanRequests []planRequestHistoryView `json:"planRequests,omitempty"`
}

// planRequestHistoryView mirrors services/api's workstreamPlanRequestView (D3,
// ADR-0065 §5, T-091) — the charter detail's bounded plan-request history entry.
type planRequestHistoryView struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expiresAt"`
	RunID     string `json:"runId,omitempty"`
}

// charterWorkstream POSTs a charter (operator, POST /v1/workstreams) and returns it. The
// charter auto-issues its own delegation grant in the same transaction (WS-2).
func charterWorkstream(t *testing.T, client *http.Client, apiURL, session, csrf string, o charterOpts) workstreamView {
	t.Helper()
	steps := make([]map[string]string, len(o.steps))
	for i, s := range o.steps {
		steps[i] = map[string]string{"resourceId": s.resourceID, "command": s.command}
	}
	payload := map[string]any{
		"objective": o.objective, "label": "wd7", "resources": o.resources, "verbs": []string{"exec"},
		"ttlSeconds": 3600, "intervalSeconds": o.intervalSeconds,
		"maxStepsPerRun": 10, "maxConcurrentRuns": 4, "stepBudget": o.stepBudget, "steps": steps,
	}
	if o.budgetWindowSec > 0 {
		payload["budgetWindowSeconds"] = o.budgetWindowSec
	}
	if o.kind != "" {
		payload["kind"] = o.kind
	}
	if o.composer != "" {
		payload["composer"] = o.composer
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/workstreams", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("charter workstream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("charter workstream: status %d, want 201", resp.StatusCode)
	}
	var v workstreamView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.ID == "" || v.GrantID == "" {
		t.Fatalf("decode charter: %v (%+v)", err, v)
	}
	return v
}

// workstreamVerb POSTs a lifecycle verb (pause|resume|retire) on a charter.
func workstreamVerb(t *testing.T, client *http.Client, apiURL, session, csrf, id, verb string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/workstreams/"+id+"/"+verb, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s workstream: %v", verb, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// retireWorkstream retires a charter (cleanup hygiene: an active charter's ticker keeps
// firing into later tests, so every WD-7 test t.Cleanup-retires — retire is idempotent
// enough here, a 409 on an already-retired charter is fine).
func retireWorkstream(t *testing.T, client *http.Client, apiURL, session, csrf, id string) {
	t.Helper()
	if code := workstreamVerb(t, client, apiURL, session, csrf, id, "retire"); code != http.StatusOK && code != http.StatusConflict {
		t.Logf("cleanup retire %s: status %d (non-fatal)", id, code)
	}
}

// getWorkstream reads a charter's lifecycle state (GET /v1/workstreams/{id}).
func getWorkstream(t *testing.T, client *http.Client, apiURL, session, csrf, id string) workstreamView {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/workstreams/"+id, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get workstream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get workstream: status %d", resp.StatusCode)
	}
	var v workstreamView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode workstream: %v", err)
	}
	return v
}

// runsForGrant lists the operator's Runs (GET /v1/runs) whose grantId matches — the
// client-side join the /workstreams UI uses (T-075). A workstream's runs carry
// actor=workstream:<id> + the charter's grantId (T-064/T-067b), so this attributes the
// autonomous ticker's fires without a launch-call return value.
func runsForGrant(t *testing.T, client *http.Client, apiURL, session, csrf, grantID string) []runStatusView {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/runs?limit=200", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list runs: status %d", resp.StatusCode)
	}
	var page struct {
		Items []runStatusView `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode runs: %v", err)
	}
	var out []runStatusView
	for _, r := range page.Items {
		if r.GrantID == grantID {
			out = append(out, r)
		}
	}
	return out
}

// waitRunsForGrant polls runsForGrant until at least min Runs bound to the grant reach a
// terminal `status`, returning them — the recurrence proof primitive (the autonomous
// ticker fires them; this observes ≥min complete).
func waitRunsForGrant(t *testing.T, client *http.Client, apiURL, session, csrf, grantID, status string, min int, within time.Duration) []runStatusView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		all := runsForGrant(t, client, apiURL, session, csrf, grantID)
		var terminal []runStatusView
		for _, r := range all {
			if r.Status == status {
				terminal = append(terminal, r)
			}
		}
		if len(terminal) >= min {
			return terminal
		}
		if time.Now().After(deadline) {
			t.Fatalf("grant %s: %d runs reached %q within %s, want ≥%d (all: %+v)", grantID, len(terminal), status, within, min, all)
		}
		time.Sleep(2 * time.Second)
	}
}
