// Unit tests for the gateway client's FE-16 Run-context derivation: parseRunStep
// (the step idempotency-key parser) and the ListPending mapping that turns the
// gateway's raw idempotencyKey into structured runId/stepIndex without leaking the
// raw key to the SPA.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRunStep(t *testing.T) {
	idx := func(n int) *int { return &n }
	cases := []struct {
		name    string
		key     string
		wantRun string
		wantIdx *int
	}{
		{"run step 0", "run:01900000-0000-7000-8000-00000000abcd:step:0", "01900000-0000-7000-8000-00000000abcd", idx(0)},
		{"run step 7", "run:abc:step:7", "abc", idx(7)},
		{"empty key (no idempotency)", "", "", nil},
		{"agent-tool key shape", "agent:smoke:tools/call", "", nil},
		{"missing step segment", "run:abc", "", nil},
		{"non-numeric index", "run:abc:step:two", "", nil},
		{"empty run id", "run::step:0", "", nil},
		{"negative index", "run:abc:step:-1", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			run, gotIdx := parseRunStep(c.key)
			if run != c.wantRun {
				t.Fatalf("runID = %q, want %q", run, c.wantRun)
			}
			switch {
			case c.wantIdx == nil && gotIdx != nil:
				t.Fatalf("stepIndex = %d, want nil", *gotIdx)
			case c.wantIdx != nil && gotIdx == nil:
				t.Fatalf("stepIndex = nil, want %d", *c.wantIdx)
			case c.wantIdx != nil && gotIdx != nil && *gotIdx != *c.wantIdx:
				t.Fatalf("stepIndex = %d, want %d", *gotIdx, *c.wantIdx)
			}
		})
	}
}

// ListPending maps the gateway's idempotencyKey into runId/stepIndex and never
// surfaces the raw key on the returned struct.
func TestListPendingDerivesRunContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"invocations": []map[string]any{
				{"id": "i1", "agent": "operator:admin", "resourceId": "r", "tool": "remote.exec",
					"command": "echo a", "status": "pending_approval", "idempotencyKey": "run:R1:step:2"},
				{"id": "i2", "agent": "agent:x", "resourceId": "r", "tool": "remote.exec",
					"command": "echo b", "status": "pending_approval", "idempotencyKey": ""},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	got, err := c.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].RunID != "R1" || got[0].StepIndex == nil || *got[0].StepIndex != 2 {
		t.Fatalf("step invocation context = (%q, %v), want (R1, 2)", got[0].RunID, got[0].StepIndex)
	}
	if got[1].RunID != "" || got[1].StepIndex != nil {
		t.Fatalf("agent invocation got Run context %q/%v, want none", got[1].RunID, got[1].StepIndex)
	}

	// The raw idempotency key must not be serializable from the returned struct.
	if blob, _ := json.Marshal(got[0]); jsonHasKey(blob, "idempotencyKey") {
		t.Fatalf("serialized PendingInvocation leaks idempotencyKey: %s", blob)
	}
}

func jsonHasKey(blob []byte, key string) bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(blob, &m)
	_, ok := m[key]
	return ok
}
