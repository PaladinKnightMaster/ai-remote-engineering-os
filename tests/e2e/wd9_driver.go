//go:build e2e || wd1

// Shared Adaptive-Workstream agent-plane driver helpers (ADR-0065) — used by the
// compose e2e WD-9 capstone (wd9_test.go / wd9_negatives_test.go, tag e2e) and, at
// T-093, the installed-stack WD-9 driver (wd1_test.go, tag wd1). Drives the three v0.9
// control-plane MCP tools (workstream.poll / plan.submit / memory.read) over the SAME
// gateway /mcp transport the run.launch driver (delegated_driver.go) uses — a real
// external MCP client, not an internal-route shortcut (G1).
package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// planRequestView mirrors services/mcp-gateway/internal/mcp.PlanRequestView field-for-
// field (ADR-0065 §5, T-090) — the composition context workstream.poll returns.
type planRequestView struct {
	RequestID      string    `json:"requestId"`
	WorkstreamID   string    `json:"workstreamId"`
	Objective      string    `json:"objective"`
	Resources      []string  `json:"resources"`
	Verbs          []string  `json:"verbs"`
	MaxStepsPerRun int       `json:"maxStepsPerRun"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// agentMemoryRowView mirrors services/mcp-gateway/internal/mcp.MemoryRowView
// field-for-field (ADR-0065 §3, T-090) — the agent-plane read's narrower field set
// (content rides ONLY for a shared authored row; an accrued reference row is
// content-free).
type agentMemoryRowView struct {
	ID          string    `json:"id"`
	ScopeType   string    `json:"scopeType"`
	ScopeID     string    `json:"scopeId"`
	Key         string    `json:"key,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Content     string    `json:"content,omitempty"`
	SourceTrace string    `json:"sourceTrace,omitempty"`
	Confidence  float32   `json:"confidence"`
	CreatedAt   time.Time `json:"createdAt"`
}

// callMCPTool POSTs a tools/call for name/args and returns the tool result text +
// isError — the shared plumbing every v0.9 control-plane tool call below reuses
// (mirrors delegated_driver.go's callRunLaunch: it retries a still-compiling stack, a
// not-ready downstream surfacing as a JSON-RPC error, never a tool error).
func callMCPTool(t *testing.T, client *http.Client, mcpURL, tok, name string, args any) (string, bool) {
	t.Helper()
	argsBody, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal %s arguments: %v", name, err)
	}
	reqBody := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, argsBody)
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
				var res mcpToolResult
				if json.Unmarshal(rpc.Result, &res) == nil && len(res.Content) > 0 {
					return res.Content[0].Text, res.IsError
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never returned a result (last err=%v)", name, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// pollWorkstreamMCP invokes the workstream.poll tool and decodes the composer's open
// plan requests. workstream.poll has NO refusal shape (ADR-0065 §5): an unknown/
// foreign/revoked filter answers empty, never a tool error — so isError is asserted
// false unconditionally here, and a revoked-grant caller (N3) simply sees an empty
// slice.
func pollWorkstreamMCP(t *testing.T, client *http.Client, mcpURL, tok, workstreamID string) []planRequestView {
	t.Helper()
	text, isErr := callMCPTool(t, client, mcpURL, tok, "workstream.poll", map[string]any{"workstreamId": workstreamID})
	if isErr {
		t.Fatalf("workstream.poll returned a tool error (want none — no refusal shape): %q", text)
	}
	var out struct {
		Requests []planRequestView `json:"requests"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode workstream.poll result %q: %v", text, err)
	}
	return out.Requests
}

// waitForPlanRequest polls workstream.poll(workstreamID) until at least one open
// request appears, returning the first — the composer's discovery primitive (ADR-0065
// §5, WD-9 leg 2/4).
func waitForPlanRequest(t *testing.T, client *http.Client, mcpURL, tok, workstreamID string, within time.Duration) planRequestView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		reqs := pollWorkstreamMCP(t, client, mcpURL, tok, workstreamID)
		if len(reqs) > 0 {
			return reqs[0]
		}
		if time.Now().After(deadline) {
			t.Fatalf("no open plan request for workstream %s appeared within %s", workstreamID, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForFreshPlanRequest polls until an open request OTHER than excludeID appears —
// the loop-closure / never-wedges primitive (WD-9 leg 7, N4): the ticker keeps firing
// after the prior request was submitted or expired, and this proves the NEXT one
// actually opened rather than merely re-observing the same row.
func waitForFreshPlanRequest(t *testing.T, client *http.Client, mcpURL, tok, workstreamID, excludeID string, within time.Duration) planRequestView {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, r := range pollWorkstreamMCP(t, client, mcpURL, tok, workstreamID) {
			if r.RequestID != excludeID {
				return r
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no fresh plan request (excluding %s) for workstream %s appeared within %s", excludeID, workstreamID, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// submitPlanMCP invokes the plan.submit tool. On success the result text is
// "submitted plan; run <id> (<status>)" (catalog.go's submitPlan) — parsed into
// (runID, status, false, text); a refusal (out-of-grant SEC-29, a closed/foreign
// request, N3/N5) is a tool error "plan submit refused: <reason>" → ("", "", true,
// text).
func submitPlanMCP(t *testing.T, client *http.Client, mcpURL, tok, requestID string, steps []map[string]string) (runID, status string, isErr bool, text string) {
	t.Helper()
	text, isErr = callMCPTool(t, client, mcpURL, tok, "plan.submit", map[string]any{"requestId": requestID, "steps": steps})
	if isErr {
		return "", "", true, text
	}
	fields := strings.Fields(text) // "submitted plan; run <id> (<status>)"
	if len(fields) < 5 || fields[0] != "submitted" {
		t.Fatalf("unexpected plan.submit result %q, want 'submitted plan; run <id> (<status>)'", text)
	}
	return fields[3], strings.Trim(fields[4], "()"), false, text
}

// readMemoryMCP invokes the memory.read tool. Like workstream.poll, it has NO refusal
// shape (ADR-0065 §3): a revoked grant or an out-of-scope filter answers empty, never a
// tool error (no oracle, N1/N3) — isError is asserted false unconditionally.
func readMemoryMCP(t *testing.T, client *http.Client, mcpURL, tok, workstreamID string) []agentMemoryRowView {
	t.Helper()
	text, isErr := callMCPTool(t, client, mcpURL, tok, "memory.read", map[string]any{"workstreamId": workstreamID})
	if isErr {
		t.Fatalf("memory.read returned a tool error (want none — no refusal shape): %q", text)
	}
	var out struct {
		Items []agentMemoryRowView `json:"items"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode memory.read result %q: %v", text, err)
	}
	return out.Items
}

// countActorOutcome counts chain events matching (actor, outcome) exactly — the
// per-composer/per-grant precision assertOutcome/hasOutcome cannot give, since
// "memory.read" (and the plan.* vocabulary) recurs across every WD-9 test with a
// DIFFERENT unique-suffixed composer/charter actor each time (the WD-8 inline-count
// pattern generalized).
func countActorOutcome(t *testing.T, auditURL, token, actor, outcome string) int {
	t.Helper()
	n := 0
	for _, e := range listAllEvents(t, auditURL, token) {
		if e.Actor == actor && e.Outcome == outcome {
			n++
		}
	}
	return n
}

// countTargetOutcome counts chain events matching (target, outcome) exactly — the
// per-run/per-request precision companion to countActorOutcome (e.g. asserting ZERO
// run.denied beside a cascade-canceled run.canceled, N3/D3).
func countTargetOutcome(t *testing.T, auditURL, token, target, outcome string) int {
	t.Helper()
	n := 0
	for _, e := range listAllEvents(t, auditURL, token) {
		if e.Target == target && e.Outcome == outcome {
			n++
		}
	}
	return n
}

// pollActorOutcomeCount waits until at least `want` events matching (actor, outcome)
// have reached the chain (poll-before-count, the T-083 outbox-race lesson) and returns
// them — the caller then asserts len(...) == want exactly, the same immediate-recount
// pattern wd8_test.go/wd8_negatives_test.go use right after their own poll.
func pollActorOutcomeCount(t *testing.T, auditURL, token, actor, outcome string, want int, within time.Duration) []auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var matched []auditEvent
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Actor == actor && e.Outcome == outcome {
				matched = append(matched, e)
			}
		}
		if len(matched) >= want {
			return matched
		}
		if time.Now().After(deadline) {
			t.Fatalf("actor %q outcome %q: got %d event(s), want >=%d within %s", actor, outcome, len(matched), want, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// w9suffix returns a short time-derived suffix so re-runs against a dirty stack never
// collide on a composer agent id / charter objective (the T-073/m8suffix fresh-key
// lesson applied to WD-9's agent subjects).
func w9suffix() string {
	return time.Now().UTC().Format("150405.000000000")
}
