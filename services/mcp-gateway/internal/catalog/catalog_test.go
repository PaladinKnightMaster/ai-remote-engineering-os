package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"aireos.local/services/mcp-gateway/internal/brokerfile"
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
	// context.search. v0.14.0 (ADR-0071) adds remote.file.read (T-140b), remote.file.write
	// (T-141), then remote.file.list (T-142) — the NINTH tool and the exact set this
	// assertion now covers.
	if len(tools) != 9 || tools[0].Name != "remote.exec" || tools[1].Name != "remote.file.read" ||
		tools[2].Name != "remote.file.write" || tools[3].Name != "remote.file.list" || tools[4].Name != "run.launch" {
		t.Fatalf("List() = %+v, want [remote.exec remote.file.read remote.file.write remote.file.list run.launch workstream.poll plan.submit memory.read context.search]", tools)
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

// TestRemoteFileReadTargetAndDescribe is the T-140b registration proof (ADR-0071
// §D1/§D3/§D4): remote.file.read carries the SAME edge as remote.exec (no new
// boundary), its own Action/Operation, and a descriptor that round-trips back to the
// raw path via the exact prefix performAndFinish strips post-approval — the property
// this design depends on, since the invocation persists only ONE field (Command) and
// there is no separate args column (C4).
func TestRemoteFileReadTargetAndDescribe(t *testing.T) {
	def, ok := NewRegistry().Lookup("remote.file.read")
	if !ok {
		t.Fatal("remote.file.read not found")
	}
	if def.Edge != "agent-remote-session" {
		t.Fatalf("edge = %q, want agent-remote-session (ADR-0071 §D1: same trust boundary as remote.exec)", def.Edge)
	}
	if def.Action != "remote.file.read" {
		t.Fatalf("action = %q, want remote.file.read", def.Action)
	}
	if def.Operation != "file.read" {
		t.Fatalf("operation = %q, want file.read", def.Operation)
	}
	if def.Description == "" {
		t.Fatal("remote.file.read must carry a description (CON-10: honest about policy/approval/audit)")
	}

	tgt, err := def.Target(json.RawMessage(`{"resourceId":"r-9","path":"/etc/nginx/nginx.conf"}`))
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if tgt != "resource:r-9" {
		t.Fatalf("Target = %q, want resource:r-9", tgt)
	}
	for _, raw := range []string{`{"resourceId":"r-9"}`, `{"path":"/x"}`, `{`} {
		if _, err := def.Target(json.RawMessage(raw)); err == nil {
			t.Fatalf("Target(%q) = nil error, want rejection", raw)
		}
	}

	desc, err := def.Describe(json.RawMessage(`{"resourceId":"r-9","path":"/etc/nginx/nginx.conf"}`))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "remote.file.read /etc/nginx/nginx.conf" {
		t.Fatalf("Describe = %q, want the ADR-0071 §D3 descriptor", desc)
	}
	if got := strings.TrimPrefix(desc, remoteFileReadDescriptorPrefix); got != "/etc/nginx/nginx.conf" {
		t.Fatalf("TrimPrefix(desc, prefix) = %q, want the raw path back (performAndFinish's recovery step)", got)
	}
}

// TestParseFileReadDescriptorFailsClosedOnMalformed is T-141 Part 4's proof for
// remote.file.read: strings.CutPrefix plus an explicit refusal, not TrimPrefix's
// silent whole-string fallback. A descriptor missing the exact prefix, or one that
// strips down to an empty path, must be REFUSED, never reinterpreted as a path.
func TestParseFileReadDescriptorFailsClosedOnMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"not the right prefix at all",
		"remote.file.read",  // missing the trailing space — no prefix match
		"remote.file.read ", // prefix matches, but strips to an empty path
		"remote.exec /etc/passwd",
	} {
		if _, err := parseFileReadDescriptor(bad); err == nil {
			t.Fatalf("parseFileReadDescriptor(%q) = nil error, want a fail-closed refusal", bad)
		}
	}
	// The positive control: a well-formed descriptor still round-trips.
	path, err := parseFileReadDescriptor(remoteFileReadDescriptorPrefix + "/etc/nginx/nginx.conf")
	if err != nil || path != "/etc/nginx/nginx.conf" {
		t.Fatalf("parseFileReadDescriptor(well-formed) = (%q, %v), want (/etc/nginx/nginx.conf, nil)", path, err)
	}
}

// TestRemoteFileWriteTargetAndDescribe is the T-141 registration proof (ADR-0071
// §D1/§D3/§D4): remote.file.write carries the SAME edge as remote.exec/remote.file.read
// (no new boundary), its own Action/Operation ("file.write", distinct from "file.read"
// per SEC-50), and a §D3 descriptor that carries path, size, digest and mode — never
// the bytes (§14c option (c)) — plus a Content extractor that recovers exactly the
// bytes the descriptor's digest was computed over.
func TestRemoteFileWriteTargetAndDescribe(t *testing.T) {
	def, ok := NewRegistry().Lookup("remote.file.write")
	if !ok {
		t.Fatal("remote.file.write not found")
	}
	if def.Edge != "agent-remote-session" {
		t.Fatalf("edge = %q, want agent-remote-session (ADR-0071 §D1: same trust boundary)", def.Edge)
	}
	if def.Action != "remote.file.write" {
		t.Fatalf("action = %q, want remote.file.write", def.Action)
	}
	if def.Operation != "file.write" {
		t.Fatalf("operation = %q, want file.write (distinct from file.read, SEC-50)", def.Operation)
	}
	if def.Description == "" || !strings.Contains(def.Description, "MODIFIES A FILE") {
		t.Fatalf("description = %q, want it to plainly state the tool mutates a real host (CON-10)", def.Description)
	}

	tgt, err := def.Target(json.RawMessage(`{"resourceId":"r-9","path":"/etc/nginx/nginx.conf","content":"c2VydmVyIHt9"}`))
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if tgt != "resource:r-9" {
		t.Fatalf("Target = %q, want resource:r-9", tgt)
	}
	for _, raw := range []string{`{"resourceId":"r-9"}`, `{"path":"/x"}`, `{`} {
		if _, err := def.Target(json.RawMessage(raw)); err == nil {
			t.Fatalf("Target(%q) = nil error, want rejection", raw)
		}
	}

	// content = base64("server {}") — encoding/json base64-decodes []byte fields
	// automatically, so the wire arg is base64 and the descriptor's digest/size are
	// computed over the DECODED bytes.
	args := json.RawMessage(`{"resourceId":"r-9","path":"/etc/nginx/nginx.conf","content":"c2VydmVyIHt9"}`)
	content, err := def.Content(args)
	if err != nil {
		t.Fatalf("Content: %v", err)
	}
	if string(content) != "server {}" {
		t.Fatalf("Content = %q, want the base64-decoded payload %q", content, "server {}")
	}
	sum := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(sum[:])

	desc, err := def.Describe(args)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	wantDesc := fmt.Sprintf("remote.file.write /etc/nginx/nginx.conf (%d bytes, sha256:%s, mode 0644)", len(content), wantDigest)
	if desc != wantDesc {
		t.Fatalf("Describe = %q, want the ADR-0071 §D3 descriptor %q", desc, wantDesc)
	}

	// A default-mode descriptor round-trips through parseFileWriteDescriptor to the
	// SAME path/digest/mode the operator approved.
	gotPath, gotDigest, gotMode, perr := parseFileWriteDescriptor(desc)
	if perr != nil {
		t.Fatalf("parseFileWriteDescriptor(well-formed): %v", perr)
	}
	if gotPath != "/etc/nginx/nginx.conf" || gotDigest != wantDigest || gotMode != 0o644 {
		t.Fatalf("parseFileWriteDescriptor = (%q, %q, %o), want (/etc/nginx/nginx.conf, %q, 0644)", gotPath, gotDigest, gotMode, wantDigest)
	}

	// An explicit mode is honored end to end (384 decimal = 0600 octal).
	descMode, err := def.Describe(json.RawMessage(`{"resourceId":"r-9","path":"/x","content":"YWJj","mode":384}`))
	if err != nil {
		t.Fatalf("Describe with mode: %v", err)
	}
	if !strings.Contains(descMode, "mode 0600") {
		t.Fatalf("Describe with mode 384 (0600) = %q, want it to render mode 0600", descMode)
	}
}

// TestParseFileWriteDescriptorFailsClosedOnMalformed is SEC-49's second guard
// (T-141 Part 3): a descriptor whose digest cannot be parsed must REFUSE, never
// proceed as if verification were unnecessary.
func TestParseFileWriteDescriptorFailsClosedOnMalformed(t *testing.T) {
	valid := sha256.Sum256([]byte("x"))
	validHex := hex.EncodeToString(valid[:])
	for _, bad := range []string{
		"",
		"remote.exec /etc/passwd (1 bytes, sha256:" + validHex + ", mode 0644)", // wrong tool prefix
		"remote.file.write ",                                                             // no metadata block at all
		"remote.file.write /etc/x.conf",                                                  // missing "(...)"
		"remote.file.write /etc/x.conf (1 bytes, mode 0644)",                             // missing digest field
		"remote.file.write /etc/x.conf (1 bytes, sha256:" + validHex + ")",               // missing mode field
		"remote.file.write /etc/x.conf (1 bytes, sha256:deadbeef, mode 0644)",            // digest too short
		"remote.file.write /etc/x.conf (1 bytes, notsha256:" + validHex + ", mode 0644)", // wrong digest key
		"remote.file.write /etc/x.conf (1 bytes, sha256:" + validHex + ", mode nine)",    // non-octal mode
		"remote.file.write  (1 bytes, sha256:" + validHex + ", mode 0644)",               // empty path
	} {
		if _, _, _, err := parseFileWriteDescriptor(bad); err == nil {
			t.Fatalf("parseFileWriteDescriptor(%q) = nil error, want a fail-closed refusal", bad)
		}
	}
}

// TestRemoteFileListTargetAndDescribe is the T-142 registration proof (ADR-0071
// §D1/§D3/§D4): remote.file.list carries the SAME edge as the rest of the file
// family (no new boundary), and — the deliberate D1 choice — shares
// remote.file.read's "file.read" Operation rather than minting a third verb.
func TestRemoteFileListTargetAndDescribe(t *testing.T) {
	def, ok := NewRegistry().Lookup("remote.file.list")
	if !ok {
		t.Fatal("remote.file.list not found")
	}
	if def.Edge != "agent-remote-session" {
		t.Fatalf("edge = %q, want agent-remote-session (ADR-0071 §D1: same trust boundary)", def.Edge)
	}
	if def.Action != "remote.file.list" {
		t.Fatalf("action = %q, want remote.file.list", def.Action)
	}
	if def.Operation != "file.read" {
		t.Fatalf("operation = %q, want file.read (ADR-0071 §D1: listing shares the read verb, deliberately no new one)", def.Operation)
	}
	if def.Description == "" {
		t.Fatal("remote.file.list must carry a description (CON-10: honest about policy/approval/audit)")
	}

	tgt, err := def.Target(json.RawMessage(`{"resourceId":"r-9","path":"/etc"}`))
	if err != nil {
		t.Fatalf("Target: %v", err)
	}
	if tgt != "resource:r-9" {
		t.Fatalf("Target = %q, want resource:r-9", tgt)
	}
	for _, raw := range []string{`{"resourceId":"r-9"}`, `{"path":"/x"}`, `{`} {
		if _, err := def.Target(json.RawMessage(raw)); err == nil {
			t.Fatalf("Target(%q) = nil error, want rejection", raw)
		}
	}

	desc, err := def.Describe(json.RawMessage(`{"resourceId":"r-9","path":"/etc"}`))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "remote.file.list /etc" {
		t.Fatalf("Describe = %q, want the ADR-0071 §D3 descriptor", desc)
	}
	if got := strings.TrimPrefix(desc, remoteFileListDescriptorPrefix); got != "/etc" {
		t.Fatalf("TrimPrefix(desc, prefix) = %q, want the raw path back (performAndFinish's recovery step)", got)
	}
	// CON-9: additionalProperties: false — the schema must reject a stray field.
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("inputSchema is not valid JSON: %v", err)
	}
	if schema.AdditionalProperties {
		t.Fatal("inputSchema additionalProperties = true, want false (CON-9)")
	}
}

// TestParseFileListDescriptorFailsClosedOnMalformed mirrors
// TestParseFileReadDescriptorFailsClosedOnMalformed exactly: CutPrefix plus an
// explicit refusal, never TrimPrefix's silent whole-string fallback.
//
// Mutation guard (VER-16): replacing parseFileListDescriptor's strings.CutPrefix
// with strings.TrimPrefix makes this test fail on the "not the right prefix at
// all" case specifically — TrimPrefix returns that string UNCHANGED (no
// trimming occurs), so `ok` is lost and the malformed input is silently
// accepted as a literal path instead of refused. The failing assertion is
// `parseFileListDescriptor(%q) = nil error, want a fail-closed refusal`.
func TestParseFileListDescriptorFailsClosedOnMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"not the right prefix at all",
		"remote.file.list",  // missing the trailing space — no prefix match
		"remote.file.list ", // prefix matches, but strips to an empty path
		"remote.file.read /etc/passwd",
	} {
		if _, err := parseFileListDescriptor(bad); err == nil {
			t.Fatalf("parseFileListDescriptor(%q) = nil error, want a fail-closed refusal", bad)
		}
	}
	// The positive control: a well-formed descriptor still round-trips.
	path, err := parseFileListDescriptor(remoteFileListDescriptorPrefix + "/etc")
	if err != nil || path != "/etc" {
		t.Fatalf("parseFileListDescriptor(well-formed) = (%q, %v), want (/etc, nil)", path, err)
	}
}

// TestRemoteExecDescribeVerbatim is the ADR-0071 §D3 seam proof for T-140a: remote.exec's
// descriptor must be the command verbatim, byte-identical to what Invoke persisted as
// the invocation's Command before this ticket's refactor (the old hardcoded
// remoteExecArgs unmarshal at Invoke's call site).
func TestRemoteExecDescribeVerbatim(t *testing.T) {
	def, _ := NewRegistry().Lookup("remote.exec")
	desc, err := def.Describe(json.RawMessage(`{"resourceId":"r-9","command":"uname -a"}`))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "uname -a" {
		t.Fatalf("Describe = %q, want the command verbatim", desc)
	}
}

// TestRemoteExecDescribeMalformedArgs proves remote.exec's descriptor rejects malformed
// or incomplete arguments with a Go error (Describe's own function contract) rather than
// panicking or silently returning an empty descriptor — the case the old
// `_ = json.Unmarshal(args, &execArgs)` in Invoke ignored.
func TestRemoteExecDescribeMalformedArgs(t *testing.T) {
	def, _ := NewRegistry().Lookup("remote.exec")
	for _, raw := range []string{`{"resourceId":"r-9"}`, `{`, `{}`} {
		if _, err := def.Describe(json.RawMessage(raw)); err == nil {
			t.Fatalf("Describe(%q) = nil error, want rejection", raw)
		}
	}
}

// TestDescribeCommandMapsDescriptorErrorToToolError is the ADR-0071 §D3 guard proof: a
// tool's descriptor failure must become a tool-level refusal (mcp.ToolResult{IsError:
// true}), never a bubbled Go error and never a panic — the treatment Invoke's SEC-16
// scope checks already get via toolError(), extended here to descriptor rendering.
// remote.exec's own Target validation makes this branch unreachable through the real
// dispatch (Target already guarantees valid args by the time Invoke reaches Describe),
// so this drives describeCommand directly with a synthetic ToolDef whose describe
// fails — the exact mapping every future operation family (T-140b) depends on the
// moment its Target and Describe validation diverge.
func TestDescribeCommandMapsDescriptorErrorToToolError(t *testing.T) {
	def := ToolDef{
		Name: "test.synthetic",
		describe: func(json.RawMessage) (string, error) {
			return "", errors.New("cannot render descriptor")
		},
	}
	command, res, isErr := describeCommand(def, json.RawMessage(`{}`))
	if !isErr {
		t.Fatal("describeCommand isErr = false, want true for a descriptor failure")
	}
	if command != "" {
		t.Fatalf("command = %q, want empty on a descriptor failure", command)
	}
	if !res.IsError {
		t.Fatalf("result = %+v, want a tool-level error result (IsError true)", res)
	}
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "cannot render descriptor") {
		t.Fatalf("result content = %+v, want the descriptor error surfaced", res.Content)
	}
}

// TestDescribeCommandSuccessPassesThrough proves the non-error branch of describeCommand
// returns the rendered descriptor unmodified and no error result — the counterpart to
// the failure-mapping test above.
func TestDescribeCommandSuccessPassesThrough(t *testing.T) {
	def := ToolDef{
		Name: "test.synthetic",
		describe: func(json.RawMessage) (string, error) {
			return "rendered descriptor", nil
		},
	}
	command, res, isErr := describeCommand(def, json.RawMessage(`{}`))
	if isErr {
		t.Fatalf("describeCommand isErr = true, want false; result = %+v", res)
	}
	if command != "rendered descriptor" {
		t.Fatalf("command = %q, want the rendered descriptor", command)
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
//
// Uses "test.never.registered" — a RESERVED SENTINEL, not a real (future) tool name —
// as the placeholder (T-142 review finding). T-140b registered "remote.file.read",
// T-141 registered "remote.file.write", and T-142 registers "remote.file.list", so a
// real tool name placeholder has now needed re-pointing twice across those three
// tickets. This sentinel is deliberately never claimed by any tool: no future ticket
// should ever register "test.never.registered", so this test's correctness no longer
// depends on staying ahead of the registry.
func TestPerformAndFinishFailsClosedForUnknownTool(t *testing.T) {
	st := &fakeSeamStore{}
	brk := &fakeSeamBroker{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: brk,
		MaxInFlight: 4, WaitTimeout: 50 * time.Millisecond, PollInterval: time.Millisecond,
	})

	id, res, err := svc.runGovernedStep(context.Background(), stepRequest{
		Subject:     "agent:test",
		Tool:        "test.never.registered", // reserved sentinel — never a registered tool, so the seam must always fail closed on it
		ResourceID:  "r-1",
		Command:     "irrelevant",
		Edge:        "agent-remote-session",
		Action:      "test.never.registered",
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

// --- T-149b / SEC-58: policy deny (and any unrecognized decision) fails closed ---
//
// Before T-149b, runGovernedStep only ever named PolicyAllow explicitly; deny AND
// every other decision string fell through into the requires_approval park branch
// and became human-approvable (ADR-0074 §D2 is inverted by a deny that parks). These
// tests drive the real, shared runGovernedStep — the same core the agent path and
// the operator Run engine both use — with a stub PolicyDecider returning "deny" and
// then an unrecognized "maybe", each paired with SPY brokers that t.Fatal the moment
// either BrokerExecer or BrokerFiler is invoked, so "no lease, no egress" is proven
// directly rather than inferred from a status field.

// fakeSEC58Policy returns a fixed decision string, driving whichever branch of
// runGovernedStep's switch the test under it targets.
type fakeSEC58Policy struct{ decision string }

func (f fakeSEC58Policy) Decide(context.Context, string, string, string, string, string, bool) (string, error) {
	return f.decision, nil
}

// fakeSEC58Store records every status this invocation passes through — the test
// asserts it NEVER contains "pending_approval", which is the heart of SEC-58: a
// deny (or an unrecognized decision) that parks is the bug — plus the single
// Finish call's status/outcome.
type fakeSEC58Store struct {
	statuses      []string
	finishCalls   int
	finishStatus  string
	finishOutcome string
}

func (f *fakeSEC58Store) Create(_ context.Context, in store.CreateInput) (store.Invocation, bool, error) {
	return store.Invocation{ID: "inv-sec58", ResourceID: in.ResourceID, Tool: in.Tool, Command: in.Command}, true, nil
}
func (f *fakeSEC58Store) CountInFlight(context.Context, string) (int, error) { return 0, nil }
func (f *fakeSEC58Store) SetStatus(_ context.Context, _, status, _ string) error {
	f.statuses = append(f.statuses, status)
	return nil
}
func (f *fakeSEC58Store) Finish(_ context.Context, _, status, outcome string) error {
	f.finishCalls++
	f.finishStatus, f.finishOutcome = status, outcome
	return nil
}
func (f *fakeSEC58Store) RequiresApproval(context.Context, string) (bool, error) { return false, nil }
func (f *fakeSEC58Store) Get(context.Context, string) (store.Invocation, error) {
	return store.Invocation{}, nil
}
func (f *fakeSEC58Store) ClaimForExec(context.Context, string) (store.Invocation, bool, error) {
	return store.Invocation{}, false, nil
}

// fatalBroker is a SPY BrokerExecer that fails the test the instant Exec is called.
// SEC-58's whole point is that a deny (or an unrecognized decision) must never
// reach the broker; this proves it directly rather than by inference.
type fatalBroker struct{ t *testing.T }

func (b fatalBroker) Exec(context.Context, string, string, string, string) (string, int, bool, error) {
	b.t.Fatal("BrokerExecer.Exec was called — a policy deny/unrecognized decision must never reach the broker (SEC-58)")
	return "", 0, false, nil
}

// fatalBrokerFile is fatalBroker's BrokerFiler sibling — ADR-0071 kept the two
// broker interfaces deliberately separate, so SEC-58 must hold for both.
type fatalBrokerFile struct{ t *testing.T }

func (b fatalBrokerFile) Read(context.Context, string, string, string, string) ([]byte, string, error) {
	b.t.Fatal("BrokerFiler.Read was called — a policy deny/unrecognized decision must never reach the broker (SEC-58)")
	return nil, "", nil
}
func (b fatalBrokerFile) Write(context.Context, string, string, string, []byte, uint32, string) (string, error) {
	b.t.Fatal("BrokerFiler.Write was called — a policy deny/unrecognized decision must never reach the broker (SEC-58)")
	return "", nil
}
func (b fatalBrokerFile) List(context.Context, string, string, string, string) ([]brokerfile.Entry, error) {
	b.t.Fatal("BrokerFiler.List was called — a policy deny/unrecognized decision must never reach the broker (SEC-58)")
	return nil, nil
}

// assertSEC58FailsClosed drives runGovernedStep with a PolicyDecider returning
// decision and asserts the full fail-closed contract shared by both the deny
// branch and the unrecognized-decision default branch: a tool-error result, the
// invocation finished exactly once as denied/tool.result:denied, the invocation
// NEVER visiting pending_approval, and (via fatalBroker/fatalBrokerFile) neither
// broker interface ever called.
func assertSEC58FailsClosed(t *testing.T, decision string) {
	t.Helper()
	st := &fakeSEC58Store{}
	svc := New(Config{
		Store: st, Policy: fakeSEC58Policy{decision: decision},
		Broker: fatalBroker{t: t}, BrokerFile: fatalBrokerFile{t: t},
		MaxInFlight: 4, WaitTimeout: 50 * time.Millisecond, PollInterval: time.Millisecond,
	})

	id, res, err := svc.runGovernedStep(context.Background(), stepRequest{
		Subject: "agent:test", Tool: remoteExecTool, ResourceID: "r-1", Command: "echo hi",
		Edge: "agent-remote-session", Action: "remote.exec", Target: "resource:r-1",
		BracketEdge: "agent-tool", WaitTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("runGovernedStep(decision=%q): unexpected Go error %v, want a tool-error result", decision, err)
	}
	if id != "inv-sec58" {
		t.Fatalf("invocation id = %q, want inv-sec58", id)
	}
	// Checked FIRST and named distinctly (VER-mutation, T-149b): this is the heart
	// of SEC-58 — a deny (or unrecognized decision) that parks is exactly the bug
	// this ticket closes, so it must be the assertion a regression trips.
	for _, s := range st.statuses {
		if s == "pending_approval" {
			t.Fatalf("SEC58_NEVER_PARKS: decision=%q invocation entered pending_approval — this IS the SEC-58 bug (a deny that parks)", decision)
		}
	}
	if !res.IsError {
		t.Fatalf("decision=%q: result = %+v, want a tool error", decision, res)
	}
	if st.finishCalls != 1 {
		t.Fatalf("decision=%q: Finish called %d times, want 1", decision, st.finishCalls)
	}
	if st.finishStatus != "denied" {
		t.Fatalf("decision=%q: finish status = %q, want denied", decision, st.finishStatus)
	}
	if st.finishOutcome != "tool.result:denied" {
		t.Fatalf("decision=%q: finish outcome = %q, want tool.result:denied", decision, st.finishOutcome)
	}
}

// TestRunGovernedStepDenyFailsClosedSEC58 is the SEC-58 capstone: an explicit
// policy "deny" must fail closed (finalize denied, tool-error result), never park,
// and never reach the broker.
func TestRunGovernedStepDenyFailsClosedSEC58(t *testing.T) {
	assertSEC58FailsClosed(t, PolicyDeny)
}

// TestRunGovernedStepUnrecognizedDecisionFailsClosedSEC58 covers the branch that
// matters MORE than the deny branch itself (ticket brief): any decision string this
// gateway does not recognize must fail closed the same way as an explicit deny —
// not fall through into the park-and-wait branch, which is what happened before
// this ticket for every non-"allow" decision, "deny" included.
func TestRunGovernedStepUnrecognizedDecisionFailsClosedSEC58(t *testing.T) {
	assertSEC58FailsClosed(t, "maybe")
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

	// Second call: SAME idempotency key, but declares "test.never.registered" — the
	// reserved sentinel (never a registered tool). Under the pre-fix code (dispatch on
	// the CALLER's req.Tool) this would fail closed; under the fix (dispatch on the
	// PERSISTED tool) it must still run the original remote.exec invocation.
	secondID, res, err := svc.runGovernedStep(ctx, stepRequest{
		Subject: "agent:test", Tool: "test.never.registered", ResourceID: "r-1", Command: "attacker payload",
		Edge: "agent-remote-session", Action: "test.never.registered", Target: "resource:r-1",
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

// fakeSeamBrokerFile fakes catalog.BrokerFiler (Read+Write+List) to drive
// performAndFinish's remote.file.{write,list} cases directly — the SEC-49 proofs
// below MUST NOT reach Write on a digest mismatch or an unparseable descriptor,
// which is exactly what writeCalled lets them assert; the list proofs use
// listCalled/listEntries/listErr the same way.
type fakeSeamBrokerFile struct {
	writeCalled bool
	gotPath     string
	gotContent  []byte
	gotMode     uint32
	writeErr    error
	writeSHA    string

	listCalled  bool
	gotListPath string
	listEntries []brokerfile.Entry
	listErr     error
}

func (f *fakeSeamBrokerFile) Read(context.Context, string, string, string, string) ([]byte, string, error) {
	return nil, "", errors.New("Read not used by the SEC-49 proofs")
}
func (f *fakeSeamBrokerFile) List(_ context.Context, _, _, path, _ string) ([]brokerfile.Entry, error) {
	f.listCalled = true
	f.gotListPath = path
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listEntries, nil
}
func (f *fakeSeamBrokerFile) Write(_ context.Context, _, _, path string, content []byte, mode uint32, _ string) (string, error) {
	f.writeCalled = true
	f.gotPath, f.gotContent, f.gotMode = path, content, mode
	if f.writeErr != nil {
		return "", f.writeErr
	}
	return f.writeSHA, nil
}

// fileWriteDescriptor builds a well-formed ADR-0071 §D3 descriptor for the given
// content, mirroring exactly what remoteFileWriteDescribe renders — so these tests
// exercise the same string shape performAndFinish parses in production.
func fileWriteDescriptor(path string, content []byte, mode uint32) string {
	sum := sha256.Sum256(content)
	return fmt.Sprintf("remote.file.write %s (%d bytes, sha256:%s, mode %04o)", path, len(content), hex.EncodeToString(sum[:]), mode)
}

// TestPerformAndFinishSEC49RefusesDigestMismatch is the ticket's block gate (proof 1):
// an invocation approved for digest X, executed with bytes hashing to Y, must be
// refused — nothing written, invocation failed, broker Write never called. The
// descriptor here is built over "approved bytes" (digest X); performAndFinish is
// handed "swapped bytes" (hashing to Y) as the content parameter — modeling either a
// tampered reattach or a direct mutation of the SEC-49 check.
func TestPerformAndFinishSEC49RefusesDigestMismatch(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	approved := []byte("approved config bytes")
	swapped := []byte("attacker swapped these bytes")
	descriptor := fileWriteDescriptor("/etc/nginx/nginx.conf", approved, 0o644)

	res, err := svc.performAndFinish(context.Background(), "inv-w1", "r-1", descriptor, "agent:test", remoteFileWriteTool, swapped)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected Go error %v, want a tool-level refusal", err)
	}
	if !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "digest") {
		t.Fatalf("result = %+v, want an IsError result mentioning the digest mismatch (SEC-49)", res)
	}
	if fileBrk.writeCalled {
		t.Fatal("SEC-49 FAILED: broker Write was called despite a digest mismatch — nothing may be written")
	}
	if st.finishCalls != 1 || st.finishStatus != "failed" {
		t.Fatalf("finish = (%d calls, status %q), want (1, failed)", st.finishCalls, st.finishStatus)
	}
}

// TestPerformAndFinishSEC49AllowsMatchingDigest is the ticket's proof 2 (the happy
// path): when the content handed to performAndFinish hashes to EXACTLY the digest in
// the approved descriptor, the write proceeds and the broker receives the right
// path/content/mode.
func TestPerformAndFinishSEC49AllowsMatchingDigest(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{writeSHA: "abc123"}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	content := []byte("server { listen 80; }")
	descriptor := fileWriteDescriptor("/etc/nginx/nginx.conf", content, 0o644)

	res, err := svc.performAndFinish(context.Background(), "inv-w2", "r-1", descriptor, "agent:test", remoteFileWriteTool, content)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected error %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %+v, want a successful write result", res)
	}
	if !fileBrk.writeCalled {
		t.Fatal("the matching-digest happy path must call broker Write")
	}
	if fileBrk.gotPath != "/etc/nginx/nginx.conf" || string(fileBrk.gotContent) != string(content) || fileBrk.gotMode != 0o644 {
		t.Fatalf("broker Write got (path=%q content=%q mode=%o), want (/etc/nginx/nginx.conf, %q, 0644)",
			fileBrk.gotPath, fileBrk.gotContent, fileBrk.gotMode, content)
	}
	if st.finishCalls != 1 || st.finishStatus != "completed" || st.finishOutcome != "tool.result" {
		t.Fatalf("finish = (%d calls, %q, %q), want (1, completed, tool.result)", st.finishCalls, st.finishStatus, st.finishOutcome)
	}
}

// TestPerformAndFinishSEC49RefusesUnparseableDescriptor is proof 3: a descriptor whose
// digest cannot be parsed must refuse rather than proceed unverified — the failure
// mode is distinct from a digest MISMATCH (proof 1) but must fire the SAME "never call
// Write" guarantee, for its own reason (a parse failure, not a hash comparison).
func TestPerformAndFinishSEC49RefusesUnparseableDescriptor(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	malformed := "remote.file.write /etc/nginx/nginx.conf this is not a valid metadata block"

	res, err := svc.performAndFinish(context.Background(), "inv-w3", "r-1", malformed, "agent:test", remoteFileWriteTool, []byte("anything"))
	if err != nil {
		t.Fatalf("performAndFinish: unexpected Go error %v, want a tool-level refusal", err)
	}
	if !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "malformed") {
		t.Fatalf("result = %+v, want an IsError result naming the malformed descriptor", res)
	}
	if fileBrk.writeCalled {
		t.Fatal("SEC-49 FAILED: broker Write was called for an unparseable descriptor — must refuse, not proceed unverified")
	}
	if st.finishCalls != 1 || st.finishStatus != "failed" {
		t.Fatalf("finish = (%d calls, status %q), want (1, failed)", st.finishCalls, st.finishStatus)
	}
}

// TestPerformAndFinishFileWriteNilBrokerFileAnswersToolError mirrors the read side's
// nil-able pattern for the write leg.
func TestPerformAndFinishFileWriteNilBrokerFileAnswersToolError(t *testing.T) {
	st := &fakeSeamStore{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{},
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	descriptor := fileWriteDescriptor("/etc/x.conf", []byte("x"), 0o644)
	res, err := svc.performAndFinish(context.Background(), "inv-w4", "r-1", descriptor, "agent:test", remoteFileWriteTool, []byte("x"))
	if err != nil {
		t.Fatalf("performAndFinish: unexpected error %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not available") {
		t.Fatalf("nil BrokerFile: isError=%v content=%+v, want a tool-level refusal, not a panic", res.IsError, res.Content)
	}
}

// TestPerformAndFinishFileListReturnsEntries is the T-142 happy-path proof: a
// well-formed descriptor dispatches to BrokerFiler.List with the recovered path,
// and the entries come back rendered as readable text (not silently dropped or
// left as a Go struct the agent could never read).
func TestPerformAndFinishFileListReturnsEntries(t *testing.T) {
	st := &fakeSeamStore{}
	modTime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	fileBrk := &fakeSeamBrokerFile{listEntries: []brokerfile.Entry{
		{Name: "nginx.conf", Size: 1234, Mode: 0o644, ModTime: modTime, IsDir: false},
		{Name: "sites-enabled", Size: 4096, Mode: 0o755, ModTime: modTime, IsDir: true},
	}}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	descriptor := remoteFileListDescriptorPrefix + "/etc/nginx"

	res, err := svc.performAndFinish(context.Background(), "inv-l1", "r-1", descriptor, "agent:test", remoteFileListTool, nil)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected error %v", err)
	}
	if res.IsError {
		t.Fatalf("result = %+v, want a successful list result", res)
	}
	if !fileBrk.listCalled || fileBrk.gotListPath != "/etc/nginx" {
		t.Fatalf("BrokerFiler.List called=%v path=%q, want called=true path=/etc/nginx", fileBrk.listCalled, fileBrk.gotListPath)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "nginx.conf") || !strings.Contains(text, "sites-enabled") {
		t.Fatalf("result text = %q, want both entry names present", text)
	}
	if !strings.Contains(text, "dir") || !strings.Contains(text, "file") {
		t.Fatalf("result text = %q, want both entry kinds (dir/file) rendered", text)
	}
	if st.finishCalls != 1 || st.finishStatus != "completed" || st.finishOutcome != "tool.result" {
		t.Fatalf("finish = (%d calls, %q, %q), want (1, completed, tool.result)", st.finishCalls, st.finishStatus, st.finishOutcome)
	}
}

// TestPerformAndFinishFileListEmptyDirectory proves an empty directory renders an
// explicit marker rather than an empty string — the caller must be able to tell
// "listed, zero entries" apart from "no text came back."
func TestPerformAndFinishFileListEmptyDirectory(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{listEntries: nil}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	descriptor := remoteFileListDescriptorPrefix + "/empty"

	res, err := svc.performAndFinish(context.Background(), "inv-l2", "r-1", descriptor, "agent:test", remoteFileListTool, nil)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected error %v", err)
	}
	if res.IsError || len(res.Content) == 0 || res.Content[0].Text == "" {
		t.Fatalf("result = %+v, want non-empty text even for zero entries", res)
	}
	if !strings.Contains(res.Content[0].Text, "empty") {
		t.Fatalf("result text = %q, want an explicit empty-directory marker", res.Content[0].Text)
	}
}

// TestPerformAndFinishFileListSurfacesBrokerErrorNotEmptyListing is the PERF-18
// proof: when BrokerFiler.List fails (e.g. the broker's 413 over-cap refusal,
// T-139b's ListDir refuse-don't-truncate rule), performAndFinish must return a Go
// error, NOT a successful result with an empty or partial entry list — those are
// indistinguishable from "this directory has zero entries" to the agent.
//
// Mutation guard (VER-16): if the `if err != nil` check on BrokerFiler.List's
// return were dropped (or its error swallowed), this test fails on
// `performAndFinish with a failing BrokerFiler.List = nil error`, because the
// call would fall through to render whatever zero-value entries slice came back
// as a successful (and misleadingly empty) listing instead of refusing.
func TestPerformAndFinishFileListSurfacesBrokerErrorNotEmptyListing(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{listErr: errors.New("broker file: unexpected status 413")}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	descriptor := remoteFileListDescriptorPrefix + "/huge"

	res, err := svc.performAndFinish(context.Background(), "inv-l3", "r-1", descriptor, "agent:test", remoteFileListTool, nil)
	if err == nil {
		t.Fatalf("performAndFinish with a failing BrokerFiler.List = nil error, want the broker's refusal surfaced")
	}
	if res.Content != nil || res.IsError {
		t.Fatalf("result = %+v, want zero-value (the caller surfaces the Go error, not a tool result)", res)
	}
	if st.finishCalls != 1 || st.finishStatus != "failed" {
		t.Fatalf("finish = (%d calls, status %q), want (1, failed)", st.finishCalls, st.finishStatus)
	}
}

// TestPerformAndFinishFileListNilBrokerFileAnswersToolError mirrors the
// read/write nil-able pattern for the list leg.
func TestPerformAndFinishFileListNilBrokerFileAnswersToolError(t *testing.T) {
	st := &fakeSeamStore{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{},
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})
	descriptor := remoteFileListDescriptorPrefix + "/etc"
	res, err := svc.performAndFinish(context.Background(), "inv-l4", "r-1", descriptor, "agent:test", remoteFileListTool, nil)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected error %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not available") {
		t.Fatalf("nil BrokerFile: isError=%v content=%+v, want a tool-level refusal, not a panic", res.IsError, res.Content)
	}
}

// TestPerformAndFinishFileListMalformedDescriptorRefuses proves the descriptor
// parse failure path is fail-closed at the performAndFinish level too (not just
// parseFileListDescriptor's own unit test): BrokerFiler.List must never be
// called for a malformed descriptor.
func TestPerformAndFinishFileListMalformedDescriptorRefuses(t *testing.T) {
	st := &fakeSeamStore{}
	fileBrk := &fakeSeamBrokerFile{}
	svc := New(Config{
		Store: st, Policy: fakeSeamPolicy{}, Broker: &fakeSeamBroker{}, BrokerFile: fileBrk,
		MaxInFlight: 4, WaitTimeout: time.Second, PollInterval: time.Millisecond,
	})

	res, err := svc.performAndFinish(context.Background(), "inv-l5", "r-1", "not a valid descriptor", "agent:test", remoteFileListTool, nil)
	if err != nil {
		t.Fatalf("performAndFinish: unexpected Go error %v, want a tool-level refusal", err)
	}
	if !res.IsError || len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "malformed") {
		t.Fatalf("result = %+v, want an IsError result naming the malformed descriptor", res)
	}
	if fileBrk.listCalled {
		t.Fatal("BrokerFiler.List was called for an unparseable descriptor — must refuse, not proceed")
	}
	if st.finishCalls != 1 || st.finishStatus != "failed" {
		t.Fatalf("finish = (%d calls, status %q), want (1, failed)", st.finishCalls, st.finishStatus)
	}
}
