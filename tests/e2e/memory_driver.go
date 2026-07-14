//go:build e2e || wd1

// Shared Persistent Scoped Memory HTTP driver helpers (ADR-0064) — used by the
// compose e2e WD-8 capstone (wd8_test.go / wd8_negatives_test.go, tag e2e) and, at
// T-084, the installed-stack WD-8 driver (wd1_test.go, tag wd1). HTTP-only (no DB),
// so they work from the external wd1 driver container that reaches the stack solely
// through Caddy HTTPS (the workstream_driver.go / run_driver.go pattern).
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// memoryView mirrors services/api/internal/server's memoryView (the MemoryView wire
// shape, ADR-0064 §7) — the full v0.8.0 field set.
type memoryView struct {
	ID          string     `json:"id"`
	ScopeType   string     `json:"scopeType"`
	ScopeID     string     `json:"scopeId"`
	Key         string     `json:"key,omitempty"`
	Ref         string     `json:"ref"`
	HasContent  bool       `json:"hasContent"`
	Content     *string    `json:"content,omitempty"`
	SourceTrace string     `json:"sourceTrace"`
	Confidence  float32    `json:"confidence"`
	Pinned      bool       `json:"pinned"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	RedactedAt  *time.Time `json:"redactedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// memoryCreateInput is the operator-authored write's input (POST /v1/memories,
// ADR-0064 §2 secondary path).
type memoryCreateInput struct {
	ScopeType   string
	ScopeID     string
	Key         string
	Content     string
	SourceTrace string
	Confidence  *float32
	ExpiresAt   *time.Time
}

// listMemories reads the caller's memories for one scope (GET /v1/memories,
// owner-scoped; ADR-0064 §7).
func listMemories(t *testing.T, client *http.Client, apiURL, session, csrf, scopeType, scopeID string) []memoryView {
	t.Helper()
	q := url.Values{"scopeType": {scopeType}, "scopeId": {scopeID}}
	req, _ := http.NewRequest(http.MethodGet, apiURL+"/v1/memories?"+q.Encode(), nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list memories: status %d, want 200", resp.StatusCode)
	}
	var out []memoryView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode memories: %v", err)
	}
	return out
}

// createMemory authors a free-text memory (POST /v1/memories — ADR-0064 §2 secondary
// path). Asserts 201.
func createMemory(t *testing.T, client *http.Client, apiURL, session, csrf string, in memoryCreateInput) memoryView {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"scopeType": in.ScopeType, "scopeId": in.ScopeID, "key": in.Key,
		"content": in.Content, "sourceTrace": in.SourceTrace,
		"confidence": in.Confidence, "expiresAt": in.ExpiresAt,
	})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/memories", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create memory: status %d, want 201", resp.StatusCode)
	}
	var v memoryView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode memory: %v", err)
	}
	return v
}

// memoryVerb POSTs a forgetting-verb route (redact|pin|unpin, ADR-0064 §3) and
// returns the resulting view + HTTP status WITHOUT asserting it — callers decide what
// counts as success (the idempotent-200 legs and the owner-404 negative both need the
// raw status).
func memoryVerb(t *testing.T, client *http.Client, apiURL, session, csrf, id, verb string) (memoryView, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/memories/"+id+"/"+verb, nil)
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s memory: %v", verb, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return memoryView{}, resp.StatusCode
	}
	var v memoryView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatalf("decode %s memory: %v", verb, err)
	}
	return v, resp.StatusCode
}

// redactMemory tombstones a memory (POST /v1/memories/{id}/redact) — idempotent 200 on
// a re-redact (D1); returns the raw status so N1 can assert both calls succeed.
func redactMemory(t *testing.T, client *http.Client, apiURL, session, csrf, id string) (memoryView, int) {
	t.Helper()
	return memoryVerb(t, client, apiURL, session, csrf, id, "redact")
}

// pinMemory exempts a memory from the expiry sweep (POST /v1/memories/{id}/pin).
// Asserts 200 — the happy-path caller never expects the negative branch.
func pinMemory(t *testing.T, client *http.Client, apiURL, session, csrf, id string) memoryView {
	t.Helper()
	v, code := memoryVerb(t, client, apiURL, session, csrf, id, "pin")
	if code != http.StatusOK {
		t.Fatalf("pin memory: status %d, want 200", code)
	}
	return v
}

// unpinMemory makes a memory eligible for the expiry sweep again
// (POST /v1/memories/{id}/unpin). Asserts 200.
func unpinMemory(t *testing.T, client *http.Client, apiURL, session, csrf, id string) memoryView {
	t.Helper()
	v, code := memoryVerb(t, client, apiURL, session, csrf, id, "unpin")
	if code != http.StatusOK {
		t.Fatalf("unpin memory: status %d, want 200", code)
	}
	return v
}

// waitForMemory polls listMemories(scopeType, scopeID) until pred matches one row,
// returning it — the bounded accrual/sweep-cadence poll primitive (the sweep rides a
// short e2e-only tick, AIREOS_MEMORY_SWEEP_SECONDS).
func waitForMemory(t *testing.T, client *http.Client, apiURL, session, csrf, scopeType, scopeID string, within time.Duration, pred func(memoryView) bool) memoryView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, m := range listMemories(t, client, apiURL, session, csrf, scopeType, scopeID) {
			if pred(m) {
				return m
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no memory in scope %s/%s matched the predicate within %s", scopeType, scopeID, within)
		}
		time.Sleep(1 * time.Second)
	}
}
