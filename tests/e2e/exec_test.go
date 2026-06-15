//go:build e2e

// TestBrokerExecEgress — the T-038b live proof of the broker non-interactive exec
// path (ADR-0058) on the go-run compose stack (docker-compose.e2e.yml, `task e2e`),
// real wires, no mocked boundary.
//
// A service-service caller (the gateway's role at T-039) POSTs /v1/exec for an
// UNGATED resource; the broker leases the credential from connector-service, runs
// the command on the real sshd, retains the captured stdout as an access-controlled
// artifact, and audits the egress. We assert: the command's stdout comes back; the
// governed egress chained credential.leased; the remote.exec event is anchored to
// the invocation with a stdout SHA; and the retained <invocationId>.exec artifact's
// SHA matches that chain anchor (bytes-in-cast parity, ADR-0049/0058).
//
// No approval here — approval is the gateway's concern (T-039/WD-3); the broker
// exec is the internal egress leg, proven live before the gateway wires it.
package e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
)

func TestBrokerExecEgress(t *testing.T) {
	brokerURL := strings.Replace(env("E2E_BROKER_WS", "ws://broker:8082"), "ws://", "http://", 1)
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	castDir := env("AIREOS_CAST_DIR", "/var/lib/aireos/sessions")

	invocationID := "inv-" + hex.EncodeToString(randBytes(t, 8))
	const wantOut = "EXEC-42-OK"
	body, _ := json.Marshal(map[string]string{
		"resourceId":   resourceID, // the ungated resource (no approval — gateway's job)
		"command":      "echo " + wantOut,
		"invocationId": invocationID,
		"subject":      "agent:e2e",
	})

	// The gateway's role: a service-service token of audience session-broker.
	tok := mintServiceToken(t, "mcp-gateway", "session-broker")

	// Retry the POST: the go-run broker may still be compiling on first call.
	var execResp struct {
		Stdout    string `json:"stdout"`
		ExitCode  int    `json:"exitCode"`
		Truncated bool   `json:"truncated"`
	}
	client := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(120 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodPost, brokerURL+"/v1/exec", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&execResp)
			_ = resp.Body.Close()
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			t.Fatalf("broker /v1/exec never returned 200 (last err=%v)", err)
		}
		time.Sleep(2 * time.Second)
	}

	if !strings.Contains(execResp.Stdout, wantOut) {
		t.Fatalf("exec stdout = %q, want it to contain %q", execResp.Stdout, wantOut)
	}
	if execResp.ExitCode != 0 || execResp.Truncated {
		t.Fatalf("exec exitCode=%d truncated=%v, want 0/false", execResp.ExitCode, execResp.Truncated)
	}
	t.Logf("broker exec returned stdout containing %q (exit 0)", wantOut)

	auditToken := mintAuditToken(t)

	// 1. The governed egress was audited: connector leased the credential for the
	// resource (evidence carries the invocation id under connector's session: prefix).
	pollLeased(t, auditURL, auditToken, "resource:"+resourceID, 45*time.Second)

	// 2. The remote.exec event, anchored to the invocation with a stdout SHA.
	execEv := pollExecEvent(t, auditURL, auditToken, "invocation:"+invocationID, 45*time.Second)
	if execEv.Actor != "agent:e2e" || execEv.Edge != "agent-remote-session" {
		t.Fatalf("remote.exec event fields wrong: %+v", execEv)
	}

	// 3. The retained artifact's SHA matches the chain anchor (bytes-in-cast parity).
	sha := strings.TrimPrefix(execEv.EvidencePacketID, "sha256:")
	if sha == "" || sha == execEv.EvidencePacketID {
		t.Fatalf("remote.exec evidencePacketId not a sha256 anchor: %q", execEv.EvidencePacketID)
	}
	artifact, err := os.ReadFile(filepath.Join(castDir, invocationID+".exec"))
	if err != nil {
		t.Fatalf("read exec artifact: %v", err)
	}
	gotSum := sha256.Sum256(artifact)
	if hex.EncodeToString(gotSum[:]) != sha {
		t.Fatalf("artifact SHA mismatch: on-disk %x != chain-anchored %s", gotSum, sha)
	}
	if !strings.Contains(string(artifact), wantOut) {
		t.Fatalf("retained artifact missing %q: %q", wantOut, artifact)
	}
	t.Log("PASS T-038b — broker exec on real wires: stdout + credential.leased + remote.exec (stdout-SHA; artifact verified)")
}

// mintServiceToken mints a service-service token (workload → audience) under the
// cluster key, e.g. the gateway's IssueService("mcp-gateway","session-broker").
func mintServiceToken(t *testing.T, workload, audience string) string {
	t.Helper()
	key, err := auth.NewKeyFromHex(os.Getenv("AIREOS_PASETO_KEY"))
	if err != nil {
		t.Fatalf("parse paseto key: %v", err)
	}
	tok, err := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID")).IssueService(workload, audience)
	if err != nil {
		t.Fatalf("mint service token: %v", err)
	}
	return tok
}

// pollExecEvent waits for the remote.exec egress event anchored to the given
// invocation target (ADR-0058), returning it for field/anchor assertions.
func pollExecEvent(t *testing.T, auditURL, token, target string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEvents(t, auditURL, token) {
			if e.Target == target && e.Outcome == "remote.exec" {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote.exec event for %s never reached the chain within %s", target, within)
		}
		time.Sleep(2 * time.Second)
	}
}
