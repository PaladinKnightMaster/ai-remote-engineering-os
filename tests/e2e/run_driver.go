//go:build e2e || wd1

// Shared Run-launch HTTP driver helpers — used by both the compose e2e suite
// (run_engine_test.go, tag e2e) and the installed-stack WD-5 driver (wd1_test.go,
// tag wd1). HTTP-only (no DB), so they work from the external wd1 driver container
// that reaches the stack solely through Caddy HTTPS.

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

type stepSeed struct {
	resourceID string
	command    string
}

// launchRun POSTs an operator-authored plan to the real launch route (POST /v1/runs,
// T-057). Returns the new run id (or "" on a non-201) + the HTTP status, so callers
// can assert both the happy path (201) and a bounds rejection (422).
func launchRun(t *testing.T, client *http.Client, apiURL, session, csrf string, steps []stepSeed) (string, int) {
	t.Helper()
	type wireStep struct {
		ResourceID string `json:"resourceId"`
		Command    string `json:"command"`
	}
	ws := make([]wireStep, len(steps))
	for i, s := range steps {
		ws[i] = wireStep{ResourceID: s.resourceID, Command: s.command}
	}
	body, _ := json.Marshal(map[string]any{"steps": ws})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("launch run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", resp.StatusCode
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return out.ID, resp.StatusCode
}

type runStatusView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// Actor / GrantID carry the delegation provenance of an agent-launched Run
	// (T-067b, FE-19/EVD-3); both are absent for an operator-direct Run.
	Actor   string `json:"actor,omitempty"`
	GrantID string `json:"grantId,omitempty"`
	Steps   []struct {
		StepIndex    int    `json:"stepIndex"`
		ResourceID   string `json:"resourceId"`
		Status       string `json:"status"`
		InvocationID string `json:"invocationId"`
		// Command rides the wire ONLY when the parent Run is 'proposed' (D2, ADR-0065
		// §6, T-091) — the plan-preview review gate; absent otherwise.
		Command string `json:"command,omitempty"`
	} `json:"steps"`
}

// cancelRun cancels a Run via the operator route (POST /v1/runs/{id}/cancel, T-059a)
// and returns the HTTP status. Moved here from run_engine_test.go at T-084 so the
// installed-stack WD-8 N4 leg (tag wd1) shares the same operator cancel path.
func cancelRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs/"+id+"/cancel", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("cancel run: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// approveRun approves a submitted adaptive plan's parked 'proposed' Run (POST
// /v1/runs/{id}/approve, RUN-6, ADR-0065 §6, T-088/T-091) and returns the resulting
// view + HTTP status WITHOUT asserting it — callers decide what counts as success (the
// happy-path 200→pending leg and the N3 dead-grant 403 leg both need the raw status).
func approveRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) (runStatusView, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs/"+id+"/approve", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("approve run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return runStatusView{}, resp.StatusCode
	}
	var v runStatusView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode approved run: %v", err)
	}
	return v, resp.StatusCode
}

// denyRun denies a submitted adaptive plan's parked 'proposed' Run (POST
// /v1/runs/{id}/deny, RUN-6/C1, ADR-0065 §6, T-088) and returns the resulting view +
// HTTP status WITHOUT asserting it (the approveRun convention).
func denyRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) (runStatusView, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/runs/"+id+"/deny", nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("deny run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return runStatusView{}, resp.StatusCode
	}
	var v runStatusView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode denied run: %v", err)
	}
	return v, resp.StatusCode
}

// getRun reads a Run's status via the operator route (GET /v1/runs/{id}, T-057).
func getRun(t *testing.T, client *http.Client, apiURL, session, csrf, id string) runStatusView {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/runs/"+id, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get run: status %d", resp.StatusCode)
	}
	var v runStatusView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	return v
}
