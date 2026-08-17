//go:build e2e

// TestCreateResourceChainsAuditEvent — the AUD-40 / T-131 G1 live proof:
// POST /v1/resources over the real wire chains resource.created into the
// audit chain, with the operator actor and the resource:<id> target — the
// symmetric case of T-118c's resource.deleted (proven live at wd1_test.go's
// resource-delete leg / store_integration_test.go). Every e2e resource up to
// this ticket was seeded directly (tests/e2e/cmd/seed), so there was no
// existing live leg that POSTs a resource over HTTP; this is a new one, not
// an extension.
package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestCreateResourceChainsAuditEvent(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	auditTok := mintAuditToken(t)
	client := &http.Client{Timeout: 30 * time.Second}

	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	resID, code := createResourceHTTP(t, client, apiURL, session, csrf, "aud40-target")
	if code != http.StatusCreated {
		t.Fatalf("create resource: status %d, want 201", code)
	}
	t.Logf("created resource %s", resID)

	evt := pollTargetOutcome(t, auditURL, auditTok, "resource:"+resID, "resource.created", 30*time.Second)
	if evt.Actor != "operator:admin" || evt.Edge != "human-agent" || evt.EvidencePacketID != "resource:"+resID {
		t.Fatalf("unexpected resource.created event: %+v", evt)
	}
	t.Log("PASS AUD-40 — CreateResource chains resource.created live, actor + target + evidence packet all correct")
}

// createResourceHTTP POSTs a minimal, ungated ssh resource to the real
// registration route and returns its server-assigned id (or "" on a
// non-201) + the HTTP status.
func createResourceHTTP(t *testing.T, client *http.Client, apiURL, session, csrf, name string) (string, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"type": "ssh", "name": name})
	req, _ := http.NewRequest(http.MethodPost, apiURL+"/v1/resources", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookieHeader(session, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("create resource: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", resp.StatusCode
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode resource: %v", err)
	}
	return out.ID, resp.StatusCode
}
