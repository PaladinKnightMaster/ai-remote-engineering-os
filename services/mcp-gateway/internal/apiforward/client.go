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

// post is the shared plumbing every forward on this client reuses (T-090): mint the
// gateway's own asymmetric service token, POST the JSON body to path, and return the
// raw response bytes + status code. Each public method above/below maps that status
// itself, since the 2xx/4xx/other split differs slightly per verb (a poll/read never
// refuses; a launch/submit does).
func (c *Client) post(ctx context.Context, path string, body []byte) ([]byte, int, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("mint token: %w", err)
	}
	if tok != "" {
		hreq.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, 0, fmt.Errorf("dispatch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp.StatusCode, nil
}

// refusalReason maps a 4xx body to the agent-facing refusal string, falling back to a
// generic status-coded message when api sent no (or an unparseable) message body.
func refusalReason(raw []byte, status int, verb string) string {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &e)
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("api rejected the %s (%d)", verb, status)
}

// Launch POSTs the run.launch forward and maps the response: 2xx → (runId, status); 4xx
// (out-of-grant, bad request) → a refusal reason (a tool error for the agent); other →
// an infra error (fail-closed).
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
	raw, status, err := c.post(ctx, "/v1/internal/delegated-runs", body)
	if err != nil {
		return "", "", "", err
	}
	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		var ok struct {
			RunID  string `json:"runId"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil {
			return "", "", "", fmt.Errorf("decode launch result: %w", err)
		}
		return ok.RunID, ok.Status, "", nil
	case status >= 400 && status < 500:
		// api refused the plan (out-of-grant, N1) or rejected the request → a tool error.
		return "", "", refusalReason(raw, status, "launch"), nil
	default:
		return "", "", "", fmt.Errorf("api launch: unexpected status %d", status)
	}
}

// Poll POSTs the workstream.poll forward (ADR-0065 §5, T-090): 2xx → the caller's open
// plan requests (possibly empty — poll has no refusal shape; an unknown/foreign
// workstreamId simply returns empty, api-side, no oracle); other → an infra error.
func (c *Client) Poll(ctx context.Context, agentSubject, workstreamID string) ([]mcp.PlanRequestView, error) {
	body, err := json.Marshal(map[string]any{
		"agentSubject": agentSubject,
		"workstreamId": workstreamID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal poll: %w", err)
	}
	raw, status, err := c.post(ctx, "/v1/internal/plan-polls", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("api poll: unexpected status %d", status)
	}
	var ok struct {
		Requests []mcp.PlanRequestView `json:"requests"`
	}
	if err := json.Unmarshal(raw, &ok); err != nil {
		return nil, fmt.Errorf("decode poll result: %w", err)
	}
	return ok.Requests, nil
}

// Submit POSTs the plan.submit forward (ADR-0065 §6, T-090): 2xx → (runId, status, "",
// nil); 4xx → a refusal reason (a tool error for the agent — the impersonation-pin
// no-oracle shape, §6); other → an infra error (fail-closed).
func (c *Client) Submit(ctx context.Context, agentSubject, requestID string, steps []mcp.LaunchStep) (string, string, string, error) {
	rs := make([]reqStep, len(steps))
	for i, s := range steps {
		rs[i] = reqStep{ResourceID: s.ResourceID, Command: s.Command}
	}
	body, err := json.Marshal(map[string]any{
		"agentSubject": agentSubject,
		"requestId":    requestID,
		"steps":        rs,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("marshal submit: %w", err)
	}
	raw, status, err := c.post(ctx, "/v1/internal/plan-submissions", body)
	if err != nil {
		return "", "", "", err
	}
	switch {
	case status == http.StatusOK || status == http.StatusCreated:
		var ok struct {
			RunID  string `json:"runId"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &ok); err != nil {
			return "", "", "", fmt.Errorf("decode submit result: %w", err)
		}
		return ok.RunID, ok.Status, "", nil
	case status >= 400 && status < 500:
		return "", "", refusalReason(raw, status, "submission"), nil
	default:
		return "", "", "", fmt.Errorf("api submit: unexpected status %d", status)
	}
}

// Read POSTs the memory.read forward (ADR-0065 §3, T-090): 2xx → the readable rows
// (possibly empty — a revoked grant or an out-of-scope filter, api-side, no error, no
// oracle); other → an infra error (fail-closed). memory.read has no refusal shape: api
// never distinguishes "denied" from "nothing to see."
func (c *Client) Read(ctx context.Context, agentSubject, workstreamID string) ([]mcp.MemoryRowView, error) {
	body, err := json.Marshal(map[string]any{
		"agentSubject": agentSubject,
		"workstreamId": workstreamID,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal read: %w", err)
	}
	raw, status, err := c.post(ctx, "/v1/internal/memory-reads", body)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("api read: unexpected status %d", status)
	}
	var ok struct {
		Items []mcp.MemoryRowView `json:"items"`
	}
	if err := json.Unmarshal(raw, &ok); err != nil {
		return nil, fmt.Errorf("decode read result: %w", err)
	}
	return ok.Items, nil
}

var (
	_ mcp.LaunchForwarder = (*Client)(nil)
	_ mcp.PollForwarder   = (*Client)(nil)
	_ mcp.SubmitForwarder = (*Client)(nil)
	_ mcp.ReadForwarder   = (*Client)(nil)
)
