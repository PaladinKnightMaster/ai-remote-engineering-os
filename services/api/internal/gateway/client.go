// Package gateway is services/api's client for the mcp-gateway's INTERNAL operator
// plane (ADR-0057 §4). The operator approves/denies a parked tool invocation through
// services/api (human-agent + CSRF, ADR-0050); api proxies it here as a
// service-service call (the gateway has no browser surface). The gateway OWNS the
// reconcile — api maps the gateway's status codes straight through. These routes are
// NOT in the OpenAPI fragment: like the broker exec / connector lease, they are
// internal service-service RPCs.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"aireos.local/packages/go/auth"
)

// Sentinels mapping the gateway's resolve status codes (the gateway owns the
// reconcile; api passes these through to the operator).
var (
	// ErrNotFound maps the gateway's 404 — no such invocation.
	ErrNotFound = errors.New("gateway: invocation not found")
	// ErrNotPending maps the gateway's 409 — the invocation is no longer awaiting
	// approval (already resolved, or policy timed it out and the gateway reconciled).
	ErrNotPending = errors.New("gateway: invocation not awaiting approval")
)

// PendingInvocation is the operator's view of a parked invocation. It carries the
// persisted command (ADR-0059) so the operator approves the exact payload; this is
// the only surface the command crosses — never the agent plane.
type PendingInvocation struct {
	ID         string `json:"id"`
	Agent      string `json:"agent"`
	ResourceID string `json:"resourceId"`
	Tool       string `json:"tool"`
	Command    string `json:"command"`
	Status     string `json:"status"`
}

// Client calls the mcp-gateway internal plane with a service-service bearer
// (IssueService("api","mcp-gateway")). A nil signer → no bearer (dev).
type Client struct {
	baseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

// NewClient builds a gateway client for baseURL, minting the service-service bearer
// from signer (nil signer → no bearer, for dev/insecure).
func NewClient(baseURL string, signer *auth.Signer) *Client {
	return &Client{
		baseURL: baseURL,
		token: func(context.Context) (string, error) {
			if signer == nil {
				return "", nil
			}
			return signer.IssueService("api", "mcp-gateway")
		},
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// ListPending returns the invocations parked pending operator approval
// (GET /v1/invocations). A non-200 is an error.
func (c *Client) ListPending(ctx context.Context) ([]PendingInvocation, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/invocations", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list invocations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list invocations: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		Invocations []PendingInvocation `json:"invocations"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode invocations: %w", err)
	}
	return body.Invocations, nil
}

type resolveRequest struct {
	Decision   string `json:"decision"` // granted | denied (policy vocabulary)
	ResolvedBy string `json:"resolvedBy"`
}

// Resolve records the operator's decision on a parked invocation
// (POST /v1/invocations/{id}/resolve). decision is the policy vocabulary
// (granted|denied) — api maps the operator verb. A 404 → ErrNotFound; a 409 →
// ErrNotPending; any other non-200 is an error (the operator sees a gateway outage).
func (c *Client) Resolve(ctx context.Context, invocationID, decision, resolvedBy string) error {
	body, err := json.Marshal(resolveRequest{Decision: decision, ResolvedBy: resolvedBy})
	if err != nil {
		return fmt.Errorf("marshal resolve request: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/invocations/"+invocationID+"/resolve", body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("resolve invocation: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusConflict:
		return ErrNotPending
	default:
		return fmt.Errorf("resolve invocation: unexpected status %d", resp.StatusCode)
	}
}

func (c *Client) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build gateway request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok, err := c.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("mint gateway token: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}
