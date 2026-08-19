package catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/store"
)

// The Service must satisfy the wire seam.
var _ mcp.Catalog = (*Service)(nil)

func TestRegistryListsRemoteExec(t *testing.T) {
	tools := NewRegistry().List()
	// v0.9 (ADR-0065, T-090): the registry advertises remote.exec, the control-plane
	// run.launch (agent-initiated Runs), and the three new control-plane tools
	// (workstream.poll/plan.submit/memory.read). v0.10 (ADR-0066, T-099) adds a sixth,
	// context.search — the exact set the F-b WD-8/WD-9 amendment now asserts.
	if len(tools) != 6 || tools[0].Name != "remote.exec" || tools[1].Name != "run.launch" {
		t.Fatalf("List() = %+v, want [remote.exec run.launch workstream.poll plan.submit memory.read context.search]", tools)
	}
	// The advertised inputSchema is a valid JSON Schema object requiring the
	// two typed args — this is the agent's interface (ADR-0057 / UX).
	var schema struct {
		Type       string         `json:"type"`
		Required   []string       `json:"required"`
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("inputSchema is not valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Fatalf("inputSchema type = %q, want object", schema.Type)
	}
	for _, want := range []string{"resourceId", "command"} {
		if _, ok := schema.Properties[want]; !ok {
			t.Fatalf("inputSchema missing property %q", want)
		}
	}
	if len(schema.Required) != 2 {
		t.Fatalf("inputSchema required = %v, want [resourceId command]", schema.Required)
	}
	if tools[0].Description == "" {
		t.Fatal("remote.exec must carry a description (the agent's UX)")
	}
}

func TestLookupGovernanceMetadata(t *testing.T) {
	r := NewRegistry()
	def, ok := r.Lookup("remote.exec")
	if !ok {
		t.Fatal("remote.exec not found")
	}
	if def.Edge != "agent-remote-session" {
		t.Fatalf("edge = %q, want agent-remote-session", def.Edge)
	}
	if def.Action != "remote.exec" {
		t.Fatalf("action = %q, want remote.exec", def.Action)
	}
	if _, ok := r.Lookup("nope"); ok {
		t.Fatal("Lookup(nope) returned ok")
	}
}

// TestControlPlaneToolsMetadata — ADR-0065 §1/§2 + ADR-0066 §4, T-090/T-099: all
// control-plane tools carry no Edge/Action/Operation EXCEPT memory.read (the reserved
// agent-memory edge, SEC-32) and context.search (the reserved agent-workspace edge,
// SEC-33) — the two lit read paths. workstream.poll/plan.submit ride the existing
// agent-tool transport reading, same as run.launch.
func TestControlPlaneToolsMetadata(t *testing.T) {
	r := NewRegistry()
	for _, name := range []string{"run.launch", "workstream.poll", "plan.submit"} {
		def, ok := r.Lookup(name)
		if !ok {
			t.Fatalf("%s not found", name)
		}
		if def.Edge != "" || def.Action != "" || def.Operation != "" {
			t.Fatalf("%s must carry no governance metadata (control-plane), got %+v", name, def)
		}
	}
	def, ok := r.Lookup("memory.read")
	if !ok {
		t.Fatal("memory.read not found")
	}
	if def.Edge != "agent-memory" {
		t.Fatalf("memory.read edge = %q, want agent-memory (SEC-32, the sole lit path)", def.Edge)
	}
	if def.Action != "" || def.Operation != "" {
		t.Fatalf("memory.read must carry no Action/Operation (control-plane dispatch), got %+v", def)
	}
	if def.Description == "" {
		t.Fatal("memory.read must carry a description (the agent's UX)")
	}
	csDef, ok := r.Lookup("context.search")
	if !ok {
		t.Fatal("context.search not found")
	}
	if csDef.Edge != "agent-workspace" {
		t.Fatalf("context.search edge = %q, want agent-workspace (SEC-33, the sole lit path)", csDef.Edge)
	}
	if csDef.Action != "" || csDef.Operation != "" {
		t.Fatalf("context.search must carry no Action/Operation (control-plane dispatch), got %+v", csDef)
	}
	if csDef.Description == "" {
		t.Fatal("context.search must carry a description (the agent's UX)")
	}
}

func TestTargetResolution(t *testing.T) {
	def, _ := NewRegistry().Lookup("remote.exec")
	tgt, err := def.Target(json.RawMessage(`{"resourceId":"r-9","command":"uname -a"}`))
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if tgt != "resource:r-9" {
		t.Fatalf("Target = %q, want resource:r-9", tgt)
	}
	for _, raw := range []string{`{"resourceId":"r-9"}`, `{"command":"x"}`, `{`} {
		if _, err := def.Target(json.RawMessage(raw)); err == nil {
			t.Fatalf("Target(%q) = nil error, want rejection", raw)
		}
	}
}

// The governed Service.Invoke (policy → broker exec, scope/concurrency/idempotency/
// fail-closed) is exercised through the wired server with real agent-tool tokens +
// fakes in server_test (it needs the capability the gate stashes); the Registry
// tests above cover the pure data layer.

// fakeSeamStore is a minimal Store fake that only supports the allow-now path
// (Create → SetStatus → performAndFinish → Finish) — exactly what
// TestPerformAndFinishFailsClosedForUnknownTool needs to drive the seam directly.
type fakeSeamStore struct {
	finishStatus  string
	finishOutcome string
	finishCalls   int
}

func (f *fakeSeamStore) Create(_ context.Context, in store.CreateInput) (store.Invocation, bool, error) {
	return store.Invocation{ID: "inv-seam-1", ResourceID: in.ResourceID, Tool: in.Tool, Command: in.Command}, true, nil
}
func (f *fakeSeamStore) CountInFlight(context.Context, string) (int, error)      { return 0, nil }
func (f *fakeSeamStore) SetStatus(context.Context, string, string, string) error { return nil }
func (f *fakeSeamStore) Finish(_ context.Context, _, status, outcome string) error {
	f.finishCalls++
	f.finishStatus, f.finishOutcome = status, outcome
	return nil
}
func (f *fakeSeamStore) RequiresApproval(context.Context, string) (bool, error) { return false, nil }
func (f *fakeSeamStore) Get(context.Context, string) (store.Invocation, error) {
	return store.Invocation{}, nil
}
func (f *fakeSeamStore) ClaimForExec(context.Context, string) (store.Invocation, bool, error) {
	return store.Invocation{}, false, nil
}

type fakeSeamPolicy struct{}

func (fakeSeamPolicy) Decide(context.Context, string, string, string, string, string, bool) (string, error) {
	return PolicyAllow, nil
}

// fakeSeamBroker MUST NOT be called by TestPerformAndFinishFailsClosedForUnknownTool —
// that is exactly what the test asserts (an unknown tool must never reach the broker).
// gotCommand/gotResource let TestReattachDispatchesOnPersistedToolNotCallerTool assert
// WHICH invocation actually ran.
type fakeSeamBroker struct {
	called      bool
	gotResource string
	gotCommand  string
}

func (f *fakeSeamBroker) Exec(_ context.Context, _, resource, command, _ string) (string, int, bool, error) {
	f.called = true
	f.gotResource, f.gotCommand = resource, command
	return "ok", 0, false, nil
}

// TestPerformAndFinishFailsClosedForUnknownTool is the VER-20/CON-11 capstone for this
// ticket (ADR-0071 §D2): the seam's terminal step dispatches on the invocation's tool,
// and an unrecognized tool must fail closed (SEC-19) rather than silently falling
// through to remote.exec. It drives the real, shared runGovernedStep (the same core the
// agent path and the operator Run engine both use) with a tool name the registry has
// never heard of — proving the default: branch, not just asserting it exists.
func TestPerformAndFinishFailsClosedForUnknownTool(t *testing.T) {
	st := &fakeSeamStore{}
	brk := &fakeSeamBroker{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: brk,
		MaxInFlight: 4, WaitTimeout: 50 * time.Millisecond, PollInterval: time.Millisecond,
	})

	id, res, err := svc.runGovernedStep(context.Background(), stepRequest{
		Subject:     "agent:test",
		Tool:        "remote.file.read", // not yet a registered tool — the seam must still fail closed
		ResourceID:  "r-1",
		Command:     "irrelevant",
		Edge:        "agent-remote-session",
		Action:      "remote.file.read",
		Target:      "resource:r-1",
		BracketEdge: "agent-tool",
		WaitTimeout: 50 * time.Millisecond,
	})

	if err == nil {
		t.Fatal("runGovernedStep with an unrecognized tool = nil error, want a fail-closed error")
	}
	if id != "inv-seam-1" {
		t.Fatalf("invocation id = %q, want inv-seam-1 (the invocation was created before dispatch)", id)
	}
	if res.Content != nil || res.IsError {
		t.Fatalf("result = %+v, want zero-value (the caller surfaces the Go error, not a tool result)", res)
	}
	if brk.called {
		t.Fatal("broker.Exec was called for an unrecognized tool — the seam did not fail closed")
	}
	if st.finishCalls != 1 {
		t.Fatalf("Finish called %d times, want 1", st.finishCalls)
	}
	if st.finishStatus != "failed" {
		t.Fatalf("finish status = %q, want failed", st.finishStatus)
	}
	if st.finishOutcome != "tool.result:error" {
		t.Fatalf("finish outcome = %q, want tool.result:error", st.finishOutcome)
	}
}

// fakeIdempotentStore models the ONE property the reattach vulnerability turns on:
// store.Create's ON CONFLICT is keyed on (agent_subject, idempotency_key), NOT tool
// (store.go:110/118) — so a second call sharing the first call's idempotency key gets
// back the ORIGINAL invocation regardless of what tool the second call declares. Get
// and ClaimForExec likewise echo the PERSISTED tool, exactly like the real store's row
// (whose `tool` column was written once, at the first Create, and never updated).
type fakeIdempotentStore struct {
	createCalled     bool
	persistedTool    string
	persistedCommand string
	claimCalled      int
}

func (f *fakeIdempotentStore) Create(_ context.Context, in store.CreateInput) (store.Invocation, bool, error) {
	if !f.createCalled {
		f.createCalled = true
		f.persistedTool, f.persistedCommand = in.Tool, in.Command
		return store.Invocation{ID: "inv-reattach", Status: "pending_approval",
			ResourceID: in.ResourceID, Tool: in.Tool, Command: in.Command}, true, nil
	}
	// Idempotent hit: the ORIGINAL row comes back untouched — in.Tool (the SECOND
	// call's declared tool) is discarded, exactly as the real store's ON CONFLICT
	// clause discards it.
	return store.Invocation{ID: "inv-reattach", Status: "pending_approval",
		ResourceID: "r-1", Tool: f.persistedTool, Command: f.persistedCommand}, false, nil
}
func (f *fakeIdempotentStore) CountInFlight(context.Context, string) (int, error) { return 0, nil }
func (f *fakeIdempotentStore) SetStatus(context.Context, string, string, string) error {
	return nil
}
func (f *fakeIdempotentStore) Finish(context.Context, string, string, string) error { return nil }
func (f *fakeIdempotentStore) RequiresApproval(context.Context, string) (bool, error) {
	return true, nil
}
func (f *fakeIdempotentStore) Get(_ context.Context, id string) (store.Invocation, error) {
	return store.Invocation{ID: id, Status: "pending_approval", Decision: PolicyAllow,
		ResourceID: "r-1", Tool: f.persistedTool, Command: f.persistedCommand}, nil
}
func (f *fakeIdempotentStore) ClaimForExec(_ context.Context, id string) (store.Invocation, bool, error) {
	f.claimCalled++
	return store.Invocation{ID: id, Status: "running", ResourceID: "r-1",
		Tool: f.persistedTool, Command: f.persistedCommand}, true, nil
}

// TestReattachDispatchesOnPersistedToolNotCallerTool is the regression test for the
// tool-confusion vector the T-138 review caught: on the idempotent-reattach path
// (runGovernedStep's `!created` branch), the SECOND call's stepRequest.Tool must NOT
// drive performAndFinish's dispatch, because a conflicting idempotency key returns the
// ORIGINAL invocation, not a row matching the second call's declared tool. Only the
// PERSISTED tool (read back via Get/ClaimForExec) may decide what runs — never the
// caller's restated value (SEC-49: approval binds to persisted state).
//
// The first call creates an invocation under remote.exec. The second call shares its
// idempotency key but declares a DIFFERENT (unregistered) tool name. If dispatch used
// the caller's value, this would fail closed on the second call even though nothing
// about the original, persisted, approved remote.exec invocation changed. The correct
// behavior — proven here — is that it still runs remote.exec, because that is what was
// actually persisted and approved.
func TestReattachDispatchesOnPersistedToolNotCallerTool(t *testing.T) {
	st := &fakeIdempotentStore{}
	brk := &fakeSeamBroker{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: brk,
		MaxInFlight: 4, WaitTimeout: 200 * time.Millisecond, PollInterval: time.Millisecond,
	})
	ctx := context.Background()

	// First call: creates the invocation under remote.exec.
	firstID, _, err := svc.runGovernedStep(ctx, stepRequest{
		Subject: "agent:test", Tool: remoteExecTool, ResourceID: "r-1", Command: "echo hi",
		Edge: "agent-remote-session", Action: "remote.exec", Target: "resource:r-1",
		BracketEdge: "agent-tool", IdempotencyKey: "shared-key", WaitTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("first call: unexpected error %v", err)
	}

	// Second call: SAME idempotency key, but declares a tool the registry has never
	// heard of. Under the pre-fix code (dispatch on the CALLER's req.Tool) this would
	// fail closed; under the fix (dispatch on the PERSISTED tool) it must still run the
	// original remote.exec invocation.
	secondID, res, err := svc.runGovernedStep(ctx, stepRequest{
		Subject: "agent:test", Tool: "remote.file.write", ResourceID: "r-1", Command: "attacker payload",
		Edge: "agent-remote-session", Action: "remote.file.write", Target: "resource:r-1",
		BracketEdge: "agent-tool", IdempotencyKey: "shared-key", WaitTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("reattach call: unexpected error %v, want the persisted remote.exec invocation to run", err)
	}
	if secondID != firstID {
		t.Fatalf("reattach id = %q, want the original invocation id %q", secondID, firstID)
	}
	if res.IsError {
		t.Fatalf("reattach result = %+v, want a successful remote.exec result", res)
	}
	if !brk.called {
		t.Fatal("broker.Exec was never called — the reattach failed closed instead of dispatching on the persisted tool")
	}
	if brk.gotCommand != "echo hi" {
		t.Fatalf("broker ran command %q, want the ORIGINAL persisted command %q (not the reattach caller's %q)",
			brk.gotCommand, "echo hi", "attacker payload")
	}
	if st.claimCalled != 1 {
		t.Fatalf("ClaimForExec called %d times, want 1", st.claimCalled)
	}
}
