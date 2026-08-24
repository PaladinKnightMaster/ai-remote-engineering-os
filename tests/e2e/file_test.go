//go:build e2e

// TestGatewayGovernedFileFamilyLivePath — the T-144a live proof of the v0.14.0
// file family (ADR-0071, T-140b/141/142) on the go-run compose stack
// (docker-compose.e2e.yml, `task e2e`), real wires, no mocked boundary:
//
//	mint a scoped agent-tool token (ADR-0071 §D7: operations MUST explicitly
//	include "file.write"/"file.read" — an ["exec"]-only token, what
//	mintAgentTokenViaAPI mints, is refused by SEC-16) → tools/call
//	remote.file.write → remote.file.read → remote.file.list against an UNGATED
//	resource → the gateway opens one invocation per call, policy decides
//	ALLOW, the broker leases the credential from connector + SFTPs the real
//	sshd target, and the result returns to the agent — each leg audited:
//	tool.invoked → credential.leased → remote.file.<op> (sha256 evidence) →
//	tool.result (AUD-41/42).
//
// Isolated onto its OWN resource (fileResourceID, "...f1", ADR-0071 seed
// addition) rather than reusing resourceID/flaggedResourceID: T-144a's Task 1
// finding is that pollLeased/pollExecEvent-style helpers match on
// (target, outcome) alone, never on evidencePacketId, so a second leg sharing
// a resource id can hand a DIFFERENT session's/invocation's event to a poll
// that expects its own — exactly what made TestSSHCockpitEndToEnd and
// TestGovernedEgressApprovalEndToEnd flake against an earlier, discarded
// version of this test that shared resourceID. A dedicated resource id makes
// every poll in this file unambiguous by construction, and — since this is
// the only place in the suite that ever calls a file-family tool — pollOutcome
// below can safely match on Outcome alone.
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGatewayGovernedFileFamilyLivePath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")

	client := &http.Client{Timeout: 60 * time.Second}

	var sessionToken, csrf string
	loginDeadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, apiURL)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sessionToken, csrf = s, c
			break
		}
		if time.Now().After(loginDeadline) {
			t.Fatalf("login never succeeded (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}

	// D7 (ADR-0071 §D7): file.read covers both read AND list (§D1 — listing is a
	// read, no separate verb), so ["file.write","file.read"] is the full scope
	// this test needs.
	tok := mintAgentTokenViaAPIOps(t, client, apiURL, sessionToken, csrf, "e2e-file", fileResourceID,
		[]string{"file.write", "file.read"})

	marker := "T144A-" + hex.EncodeToString(randBytes(t, 6))
	content := []byte("file family capstone " + marker + "\n")
	path := "/home/aireos/" + marker + ".txt"
	auditToken := mintAuditToken(t)

	// --- write ------------------------------------------------------------------
	writeArgs, _ := json.Marshal(struct {
		ResourceID string `json:"resourceId"`
		Path       string `json:"path"`
		Content    []byte `json:"content"` // encoding/json base64s this for us — no hand-rolled encoding
	}{fileResourceID, path, content})
	writeResult := callFileTool(t, client, mcpURL, tok, "remote.file.write", writeArgs, 180*time.Second)
	if writeResult.IsError || len(writeResult.Content) == 0 {
		t.Fatalf("remote.file.write result = %+v, want a successful write", writeResult)
	}
	t.Logf("remote.file.write: %s", strings.TrimSpace(writeResult.Content[0].Text))

	wantSum := sha256.Sum256(content)
	wantDigest := hex.EncodeToString(wantSum[:])

	// remote.file.write's own egress event (AUD-42: only the sha256 digest ever
	// crosses into the chain — never the raw bytes) is globally unique in this
	// run (this is the only test that exercises the file family), so matching on
	// Outcome alone is unambiguous. Its Target ("invocation:<id>") IS the
	// invocation id, recovered here rather than via a separate tool.invoked poll.
	writeEv := pollOutcome(t, auditURL, auditToken, "remote.file.write", 45*time.Second)
	if writeEv.Edge != "agent-remote-session" {
		t.Fatalf("remote.file.write edge = %q, want agent-remote-session", writeEv.Edge)
	}
	if got := strings.TrimPrefix(writeEv.EvidencePacketID, "sha256:"); got != wantDigest {
		t.Fatalf("remote.file.write evidence %q != sha256(written content) %q", writeEv.EvidencePacketID, wantDigest)
	}
	writeInvocation := strings.TrimPrefix(writeEv.Target, "invocation:")
	if writeInvocation == "" || writeInvocation == writeEv.Target {
		t.Fatalf("remote.file.write target %q is not an invocation anchor", writeEv.Target)
	}

	// AUD-41: every file operation is bracketed tool.invoked/tool.result on the
	// agent-tool edge, anchored invocation:<id> — identical in shape to exec.
	invokedEv := pollEvidenceOutcome(t, auditURL, auditToken, "invocation:"+writeInvocation, "tool.invoked", 30*time.Second)
	if invokedEv.Edge != "agent-tool" || invokedEv.Actor != "agent:e2e-file" {
		t.Fatalf("write leg tool.invoked fields wrong: %+v", invokedEv)
	}
	pollEvidenceOutcome(t, auditURL, auditToken, "invocation:"+writeInvocation, "tool.result", 30*time.Second)

	// The governed egress: connector leased the credential for THIS resource,
	// attributed to the agent, anchored to the write's own invocation — safe to
	// match on (target, outcome) alone because fileResourceID is isolated (only
	// this test's three calls ever lease against it). connector-service always
	// literal-prefixes its evidence "session:" (services/connector-service/
	// internal/store/store.go) regardless of what kind of id the caller passed —
	// a naming holdover from the original SSH-cockpit-only design; the gateway
	// path hands it the invocation id under that same parameter, exactly like
	// exec's credential.leased does (exec_test.go / gateway_test.go never assert
	// this field's exact value for that reason, but the prefix is still literally
	// "session:", never "invocation:").
	leasedEv := pollLeased(t, auditURL, auditToken, "resource:"+fileResourceID, 45*time.Second)
	if leasedEv.Actor != "agent:e2e-file" || leasedEv.EvidencePacketID != "session:"+writeInvocation {
		t.Fatalf("credential.leased fields wrong: %+v", leasedEv)
	}
	t.Log("PASS write leg — tool.invoked(agent-tool) -> credential.leased -> remote.file.write(sha256) -> tool.result")

	// --- read: proves the write actually landed, byte for byte -------------------
	readArgs, _ := json.Marshal(struct {
		ResourceID string `json:"resourceId"`
		Path       string `json:"path"`
	}{fileResourceID, path})
	readResult := callFileTool(t, client, mcpURL, tok, "remote.file.read", readArgs, 180*time.Second)
	if readResult.IsError || len(readResult.Content) == 0 || !strings.Contains(readResult.Content[0].Text, string(content)) {
		t.Fatalf("remote.file.read result = %+v, want it to contain the written content %q", readResult, content)
	}

	readEv := pollOutcome(t, auditURL, auditToken, "remote.file.read", 45*time.Second)
	if readEv.Edge != "agent-remote-session" {
		t.Fatalf("remote.file.read edge = %q, want agent-remote-session", readEv.Edge)
	}
	if got := strings.TrimPrefix(readEv.EvidencePacketID, "sha256:"); got != wantDigest {
		t.Fatalf("remote.file.read evidence %q != sha256(written content) %q — read-back mismatch", readEv.EvidencePacketID, wantDigest)
	}
	t.Log("PASS read leg — remote.file.read digest matches the content this test wrote")

	// --- list: the directory now carries the file this test just wrote -----------
	listArgs, _ := json.Marshal(struct {
		ResourceID string `json:"resourceId"`
		Path       string `json:"path"`
	}{fileResourceID, "/home/aireos"})
	listResult := callFileTool(t, client, mcpURL, tok, "remote.file.list", listArgs, 180*time.Second)
	wantName := marker + ".txt"
	if listResult.IsError || len(listResult.Content) == 0 || !strings.Contains(listResult.Content[0].Text, wantName) {
		t.Fatalf("remote.file.list result = %+v, want it to list %q", listResult, wantName)
	}

	listEv := pollOutcome(t, auditURL, auditToken, "remote.file.list", 45*time.Second)
	if listEv.Edge != "agent-remote-session" || !strings.HasPrefix(listEv.EvidencePacketID, "sha256:") {
		t.Fatalf("remote.file.list event fields wrong: %+v", listEv)
	}
	t.Log("PASS list leg — remote.file.list sees the file this test wrote, sha256 evidence over the listing")

	t.Log("PASS T-144a — agent -> gateway -> policy(allow) -> connector lease -> broker SFTP -> audit: " +
		"file family (write/read/list) proven on real wires, isolated chain (AUD-41/42)")
}

// callFileTool drives one tools/call for a file-family tool, retrying while the
// go-run stack may still be compiling — the same "an rpc.Error means retry, a
// transport error means retry" contract TestGatewayGovernedExecAllowPath and
// fireHeldInvoke already use, so this test follows the established convention
// rather than inventing a second one. Arguments are built via json.Marshal on a
// typed struct (never hand-rolled JSON string concatenation) specifically so a
// []byte content field is base64-encoded correctly — a hand-built request body
// is exactly the kind of persistent, non-transient malformed-argument mistake
// that would make this retry loop spin for its whole deadline and misreport as
// "err=<nil>" (a bug class, not this test's own).
func callFileTool(t *testing.T, client *http.Client, mcpURL, tok, name string, args json.RawMessage, within time.Duration) mcpToolResult {
	t.Helper()
	reqBody, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"params"`
	}{
		JSONRPC: "2.0", ID: 1, Method: "tools/call",
		Params: struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("marshal tools/call for %s: %v", name, err)
	}

	var result mcpToolResult
	deadline := time.Now().Add(within)
	for {
		req, _ := http.NewRequest(http.MethodPost, mcpURL+"/mcp", bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := client.Do(req)
		if doErr == nil && resp.StatusCode == http.StatusOK {
			var rpc struct {
				Result json.RawMessage `json:"result"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpc)
			_ = resp.Body.Close()
			if rpc.Error == nil && len(rpc.Result) > 0 {
				if unmarshalErr := json.Unmarshal(rpc.Result, &result); unmarshalErr == nil {
					return result
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s tools/call never returned a result (last err=%v)", name, doErr)
		}
		time.Sleep(3 * time.Second)
	}
}

// pollOutcome waits for the event with the given Outcome, regardless of target —
// safe ONLY because this file is the sole caller of the file-family tools in the
// whole suite, so each of remote.file.write/read/list occurs exactly once per run.
func pollOutcome(t *testing.T, auditURL, token, outcome string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Outcome == outcome {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %q never reached the chain within %s", outcome, within)
		}
		time.Sleep(2 * time.Second)
	}
}
