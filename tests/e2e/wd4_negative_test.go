//go:build e2e

// TestKeylessGatewayCannotForgeForeignEdge — the WD-4 / SEC-21 blast-radius proof
// (v0.4.0, ADR-0060). The agent-facing gateway holds NO symmetric cluster key
// (T-049): the most an RCE'd gateway has is its OWN asymmetric signing key. This
// asserts that key cannot forge a token any peer accepts for a foreign edge — most
// importantly a connector credential lease (the crown jewel: decrypt any target's
// secret). connector and api are symmetric-only and were deliberately never given
// the gateway's public key (only policy/broker/audit hold it, T-048), so a
// gateway-signed v4.public token dies at the capability gate (401).
//
// The suite's green positive chain (AllowPath/ApprovalPath) is the control: the same
// gateway key drives policy/broker/audit successfully, so the token is well-formed —
// the 401 here means "this key is not trusted on this edge," not "malformed." The
// test fails loudly if a peer is ever handed the gateway's public key (re-opening the
// blast radius).
package e2e

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"aireos.local/packages/go/auth"
)

func TestKeylessGatewayCannotForgeForeignEdge(t *testing.T) {
	skHex := os.Getenv("AIREOS_GATEWAY_SIGN_KEY")
	if skHex == "" {
		// Mandatory in the only runtime this test has (the e2e tester). A missing key
		// is a compose regression, not a reason to skip the SEC-21 proof — fail loud.
		t.Fatal("AIREOS_GATEWAY_SIGN_KEY required on the e2e tester for the SEC-21 negative test")
	}
	sk, err := auth.NewAsymmetricSecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("load gateway sign key: %v", err)
	}
	gw := auth.NewAsymmetricSigner(sk, os.Getenv("AIREOS_CLUSTER_ID"))
	client := &http.Client{Timeout: 10 * time.Second}

	// 1. Crown jewel: the gateway's key cannot lease (decrypt) a credential. connector
	//    is symmetric-only and never holds the gateway's public key.
	connURL := env("E2E_CONNECTOR_URL", "http://connector:8083")
	leaseTok, err := gw.IssueService("mcp-gateway", "connector-service")
	if err != nil {
		t.Fatalf("mint forged lease token: %v", err)
	}
	body := `{"resource_id":"` + resourceID + `","session_id":"x","operator_subject":"operator:admin"}`
	if code := bearerStatus(t, client, http.MethodPost, connURL+"/v1/credentials/lease", leaseTok, body); code != http.StatusUnauthorized {
		t.Fatalf("connector accepted a gateway-signed lease token: status=%d, want 401 (SEC-21: keyless gateway cannot reach credentials)", code)
	}

	// 2. Not connector-specific: api also rejects a gateway-signed token (it verifies
	//    operator/service tokens with the cluster key, which the gateway lacks).
	apiURL := env("E2E_API_URL", "http://api:8080")
	apiTok, err := gw.IssueService("mcp-gateway", "api")
	if err != nil {
		t.Fatalf("mint forged api token: %v", err)
	}
	if code := bearerStatus(t, client, http.MethodGet, apiURL+"/v1/resources", apiTok, ""); code != http.StatusUnauthorized {
		t.Fatalf("api accepted a gateway-signed token: status=%d, want 401", code)
	}

	t.Log("PASS WD-4/SEC-21 — the keyless gateway's own key forges nothing connector or api accepts (401/401); blast radius bounded")
}

// bearerStatus issues an HTTP request with a Bearer token and returns the status code.
func bearerStatus(t *testing.T, c *http.Client, method, url, token, body string) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("request to %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}
