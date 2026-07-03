//go:build e2e || wd1

// Shared delegated-launch HTTP driver helpers (ADR-0062) — used by the compose e2e
// suite (delegated_run_test.go / wd6_test.go, tag e2e) and, at T-069/OI-13, the
// installed-stack WD-6 driver (wd1_test.go, tag wd1). HTTP-only (no DB), so they work
// from the external wd1 driver container that reaches the stack solely through Caddy
// HTTPS. Moved verbatim from delegated_run_test.go at T-068 (the T-061 run_driver.go
// pattern).

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// issueGrant POSTs a delegation grant (operator, POST /v1/grants) and returns its id.
func issueGrant(t *testing.T, client *http.Client, apiURL, session, csrf, agent string, resources []string, stepBudget int) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"agent": agent, "label": "e2e", "resources": resources, "verbs": []string{"exec"},
		"ttlSeconds": 3600, "maxStepsPerRun": 10, "maxConcurrentRuns": 4, "stepBudget": stepBudget,
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/grants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("issue grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("issue grant: status %d", resp.StatusCode)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" {
		t.Fatalf("decode grant: %v (id=%q)", err, out.ID)
	}
	return out.ID
}

// revokeGrantHTTP revokes a grant via the operator route (POST /v1/grants/{id}/revoke).
func revokeGrantHTTP(t *testing.T, client *http.Client, apiURL, session, csrf, id string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/grants/"+id+"/revoke", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke grant: status %d, want 200", resp.StatusCode)
	}
}

// callRunLaunch invokes the run.launch MCP tool and returns the tool result text + isError.
// It retries a still-compiling stack (a not-ready downstream surfaces as a JSON-RPC error).
func callRunLaunch(t *testing.T, client *http.Client, mcpURL, tok, grantID string, steps []map[string]string) (string, bool) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"grantId": grantID, "steps": steps})
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run.launch","arguments":` + string(args) + `}}`
	deadline := time.Now().Add(180 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", strings.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpc)
			_ = resp.Body.Close()
			if rpc.Error == nil && len(rpc.Result) > 0 {
				var res struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
					IsError bool `json:"isError"`
				}
				if json.Unmarshal(rpc.Result, &res) == nil && len(res.Content) > 0 {
					return res.Content[0].Text, res.IsError
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("run.launch never returned a result (last err=%v)", err)
		}
		time.Sleep(3 * time.Second)
	}
}

// waitRunStatusHTTP polls the operator run view (GET /v1/runs/{id}) until the Run
// reaches want. Any OTHER terminal status fails fast. HTTP-only (unlike
// run_engine_test.go's DB-backed waitRunStatus) so the external wd1 driver can use it.
func waitRunStatusHTTP(t *testing.T, client *http.Client, apiURL, session, csrf, id, want string, within time.Duration) runStatusView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		view := getRun(t, client, apiURL, session, csrf, id)
		if view.Status == want {
			return view
		}
		for _, terminal := range []string{"completed", "failed", "canceled"} {
			if view.Status == terminal {
				t.Fatalf("run %s ended %s, want %s: %+v", id, view.Status, want, view)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s status = %q, want %q within %s", id, view.Status, want, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// callRunLaunchOK invokes run.launch, asserts success, and extracts the new run id from the
// tool result text ("launched run <id> (<status>)").
func callRunLaunchOK(t *testing.T, client *http.Client, mcpURL, tok, grantID string, steps []map[string]string) string {
	t.Helper()
	text, isErr := callRunLaunch(t, client, mcpURL, tok, grantID, steps)
	if isErr {
		t.Fatalf("run.launch returned a tool error: %q", text)
	}
	fields := strings.Fields(text) // "launched run <id> (<status>)"
	if len(fields) < 3 || fields[0] != "launched" {
		t.Fatalf("unexpected run.launch result %q, want 'launched run <id> ...'", text)
	}
	return fields[2]
}
