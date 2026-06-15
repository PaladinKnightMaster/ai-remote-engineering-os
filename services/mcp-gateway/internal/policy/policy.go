// Package policy is the mcp-gateway's client for policy-service's INTERNAL decision
// RPC (ADR-0054/0057). The gateway consults it per invocation, passing the
// invocation id as policy's opaque correlation (the session_id field) — so the
// approval primitive generalizes from session to invocation with no policy change.
package policy

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

// Decision values (ADR-0054 §1); deny is reserved for a later rule engine.
const (
	DecisionAllow            = "allow"
	DecisionRequiresApproval = "requires_approval"
)

// Client calls policy-service POST /v1/decisions with a service-service bearer
// (IssueService("mcp-gateway","policy-service")). A nil signer → no bearer (dev).
type Client struct {
	baseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

func New(baseURL string, signer *auth.Signer) *Client {
	return &Client{
		baseURL: baseURL,
		token: func(context.Context) (string, error) {
			if signer == nil {
				return "", nil
			}
			return signer.IssueService("mcp-gateway", "policy-service")
		},
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

type decideRequest struct {
	SessionID        string `json:"session_id"`
	Subject          string `json:"subject"`
	Edge             string `json:"edge"`
	Target           string `json:"target"`
	Action           string `json:"action"`
	RequiresApproval bool   `json:"requires_approval"`
}

type decideResponse struct {
	Decision string `json:"decision"`
}

// Decide returns policy-service's decision (allow | requires_approval) for an
// invocation. invocationID rides in session_id (policy's opaque key). A non-200
// is an error so the caller fails closed (SEC-19).
func (c *Client) Decide(ctx context.Context, invocationID, subject, edge, target, action string, requiresApproval bool) (string, error) {
	if c.baseURL == "" {
		return "", errors.New("policy URL not configured")
	}
	body, err := json.Marshal(decideRequest{
		SessionID: invocationID, Subject: subject, Edge: edge,
		Target: target, Action: action, RequiresApproval: requiresApproval,
	})
	if err != nil {
		return "", fmt.Errorf("marshal decide request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/decisions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build decide request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return "", fmt.Errorf("mint policy token: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("decide request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("policy decide: unexpected status %d", resp.StatusCode)
	}
	var d decideResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return "", fmt.Errorf("decode decision: %w", err)
	}
	return d.Decision, nil
}
