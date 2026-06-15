//go:build e2e

// TestGatewayGovernedExecAllowPath — the T-039c live proof of the Typed MCP Gateway
// driving the whole v0.3.0 spine on the go-run compose stack (`task e2e`), real
// wires, no mocked boundary. It is the first end-to-end gateway proof:
//
//	mint a scoped agent-tool token → MCP tools/call remote.exec on an UNGATED
//	resource → the gateway opens an invocation, policy decides ALLOW, the broker
//	leases the credential from connector + runs the command on the real sshd, and
//	the result returns to the agent — the full chain audited:
//	tool.invoked → credential.leased → remote.exec (stdout-SHA) → tool.result.
//
// No approval here — the ungated resource takes the allow path. The flagged
// approval path (block-and-wait + operator resolve) is T-040; the full WD-3 is T-041.
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
)

func TestGatewayGovernedExecAllowPath(t *testing.T) {
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	const wantOut = "EXEC-42-OK"

	// The agent's scoped agent-tool token (audience mcp-gateway), scoped to the
	// ungated resource it will target (SEC-16).
	tok := mintAgentToken(t, "agent:e2e", "mcp-gateway", resourceID)
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"remote.exec",` +
		`"arguments":{"resourceId":"` + resourceID + `","command":"echo ` + wantOut + `"}}}`

	// Retry: the go-run gateway/policy/broker may still be compiling (a not-ready
	// downstream surfaces fail-closed as a JSON-RPC error — retry, don't fail).
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	client := &http.Client{Timeout: 60 * time.Second}
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
				if err := json.Unmarshal(rpc.Result, &result); err == nil {
					break
				}
			}
		} else if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("gateway tools/call never succeeded (last err=%v)", err)
		}
		time.Sleep(3 * time.Second)
	}

	if result.IsError || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, wantOut) {
		t.Fatalf("tool result = %+v, want stdout containing %q (isError false)", result, wantOut)
	}
	stdout := result.Content[0].Text
	t.Logf("gateway remote.exec returned %q", strings.TrimSpace(stdout))

	auditToken := mintAuditToken(t)

	// tool.invoked carries the gateway-generated invocation id (the chain anchor).
	invokedEv := pollTargetOutcome(t, auditURL, auditToken, "resource:"+resourceID, "tool.invoked", 60*time.Second)
	if invokedEv.Actor != "agent:e2e" || invokedEv.Edge != "agent-tool" {
		t.Fatalf("tool.invoked fields wrong: %+v", invokedEv)
	}
	invocation := invokedEv.EvidencePacketID // "invocation:<id>"
	if !strings.HasPrefix(invocation, "invocation:") {
		t.Fatalf("tool.invoked evidence not an invocation anchor: %q", invocation)
	}

	// The governed egress was audited: connector leased the credential.
	pollLeased(t, auditURL, auditToken, "resource:"+resourceID, 45*time.Second)

	// The broker's remote.exec is anchored to the invocation, and its stdout SHA
	// matches the stdout the gateway returned to the agent (end-to-end integrity).
	execEv := pollExecEvent(t, auditURL, auditToken, invocation, 45*time.Second)
	wantSHA := sha256.Sum256([]byte(stdout))
	if strings.TrimPrefix(execEv.EvidencePacketID, "sha256:") != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("remote.exec sha %q != sha256(returned stdout)", execEv.EvidencePacketID)
	}

	// tool.result closes the chain.
	pollEvidenceOutcome(t, auditURL, auditToken, invocation, "tool.result", 45*time.Second)

	t.Log("PASS T-039c — agent → gateway → policy(allow) → connector lease → broker exec → audit: full governed chain on real wires")
}

// mintAgentToken mints a scoped agent-tool token (the operator's role at T-036),
// e.g. IssueAgent("agent:e2e","mcp-gateway",{resource}).
func mintAgentToken(t *testing.T, subject, audience, resourceScope string) string {
	t.Helper()
	key, err := auth.NewKeyFromHex(os.Getenv("AIREOS_PASETO_KEY"))
	if err != nil {
		t.Fatalf("parse paseto key: %v", err)
	}
	tok, err := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID")).
		IssueAgent(subject, audience, auth.Scope{Resource: resourceScope})
	if err != nil {
		t.Fatalf("mint agent token: %v", err)
	}
	return tok
}

// pollTargetOutcome waits for an event matching (target, outcome) and returns it.
func pollTargetOutcome(t *testing.T, auditURL, token, target, outcome string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Target == target && e.Outcome == outcome {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %s/%s never reached the chain within %s", target, outcome, within)
		}
		time.Sleep(2 * time.Second)
	}
}
