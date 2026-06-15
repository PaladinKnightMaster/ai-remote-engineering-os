package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"aireos.local/services/mcp-gateway/internal/mcp"
)

// The Service must satisfy the wire seam.
var _ mcp.Catalog = (*Service)(nil)

func TestRegistryListsRemoteExec(t *testing.T) {
	tools := New().List()
	if len(tools) != 1 || tools[0].Name != "remote.exec" {
		t.Fatalf("List() = %+v, want exactly [remote.exec]", tools)
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

func TestServiceInvokeStub(t *testing.T) {
	s := New()
	// Known tool, valid args → not-implemented (governed path is T-039).
	if _, err := s.Invoke(context.Background(), "remote.exec", json.RawMessage(`{"resourceId":"r-9","command":"x"}`)); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Invoke(remote.exec) err = %v, want ErrNotImplemented", err)
	}
	// Known tool, invalid args → the target validation error (not not-implemented).
	if _, err := s.Invoke(context.Background(), "remote.exec", json.RawMessage(`{"resourceId":""}`)); err == nil || errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Invoke(remote.exec, bad args) err = %v, want a validation error", err)
	}
	// Unknown tool → unknown-tool error.
	if _, err := s.Invoke(context.Background(), "nope", json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("Invoke(nope) err = %v, want ErrUnknownTool", err)
	}
}
