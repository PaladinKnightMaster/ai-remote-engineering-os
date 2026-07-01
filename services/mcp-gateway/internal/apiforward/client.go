// Package apiforward is the gateway's client for services/api's internal delegated-launch
// route (ADR-0062 §3, T-065b): an agent's run.launch is forwarded here so api can validate
// the plan against the delegation grant and create the Run. The gateway does NOT create Runs
// — api is the sole authority. Mirrors internal/policy.Client: it mints the gateway's OWN
// asymmetric service token (IssueService("mcp-gateway","api"), ADR-0060) so api's route-scoped
// verifier accepts it (subject-pinned onlyGateway on the api side). It is NOT a held call.
package apiforward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"aireos.local/packages/go/auth"
	"aireos.local/services/mcp-gateway/internal/mcp"
)

// Client forwards run.launch to services/api.
type Client struct {
	baseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

// New builds the forward client. A nil signer sends no bearer (dev). The caller passes nil to
// the catalog when API_URL is unset, so run.launch then answers a tool error.
func New(baseURL string, signer *auth.AsymmetricSigner) *Client {
	return &Client{
		baseURL: baseURL,
		token: func(context.Context) (string, error) {
			if signer == nil {
				return "", nil
			}
			return signer.IssueService("mcp-gateway", "api")
		},
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

type reqStep struct {
	ResourceID string `json:"resourceId"`
	Command    string `json:"command"`
}

// Launch POSTs the forward and maps the response: 2xx → (runId, status); 4xx (out-of-grant,
// bad request) → a refusal reason (a tool error for the agent); other → an infra error
// (fail-closed).
func (c *Client) Launch(ctx context.Context, agentSubject, grantID, idempotencyKey string, steps []mcp.LaunchStep) (string, string, string, error) {
	rs := make([]reqStep, len(steps))
	for i, s := range steps {
		rs[i] = reqStep{ResourceID: s.ResourceID, Command: s.Command}
	}
	body, err := json.Marshal(map[string]any{
		"agentSubject":   agentSubject,
		"grantId":        grantID,
		"idempotencyKey": idempotencyKey,
		"steps":          rs,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("marshal launch: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/internal/delegated-runs", bytes.NewReader(body))
	if err != nil {
		return "", "", "", fmt.Errorf("build launch request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("mint launch token: %w", err)
	}
	if tok != "" {
		hreq.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return "", "", "", fmt.Errorf("dispatch launch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated:
		var ok struct {
			RunID  string `json:"runId"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil {
			return "", "", "", fmt.Errorf("decode launch result: %w", err)
		}
		return ok.RunID, ok.Status, "", nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		// api refused the plan (out-of-grant, N1) or rejected the request → a tool error.
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		reason := e.Message
		if reason == "" {
			reason = fmt.Sprintf("api rejected the launch (%d)", resp.StatusCode)
		}
		return "", "", reason, nil
	default:
		return "", "", "", fmt.Errorf("api launch: unexpected status %d", resp.StatusCode)
	}
}

var _ mcp.LaunchForwarder = (*Client)(nil)
