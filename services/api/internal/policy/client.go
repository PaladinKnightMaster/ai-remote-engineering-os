// Package policy is services/api's client for policy-service's INTERNAL decision
// RPC (ADR-0054 §5). At session-create, when the target resource is labeled
// requires_approval, services/api consults policy-service over this client; the
// returned decision (allow | requires_approval) determines whether the session is
// parked for an operator approval. The endpoint is NOT in the shared OpenAPI
// fragment — it is a service-service call, the same posture as the broker's
// connector-lease client.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"aireos.local/packages/go/auth"
)

// Decision values returned by decide (ADR-0054 §1). deny is reserved (no deny
// rules until the v0.3.0 rule engine).
const (
	DecisionAllow            = "allow"
	DecisionRequiresApproval = "requires_approval"
)

// DecideInput is the decide request the enforcing service supplies.
type DecideInput struct {
	SessionID        string
	Subject          string
	Edge             string
	Target           string
	Action           string
	RequiresApproval bool
}

// Decision is policy-service's decide response (the approval ref, if any, is not
// needed by api at session-create — api parks the session itself).
type Decision struct {
	Decision string `json:"decision"`
}

// Client calls policy-service's POST /v1/decisions. Token supplies the
// service-service bearer (IssueService("api","policy-service")); HTTP defaults to
// a 10s client when nil.
type Client struct {
	BaseURL string
	Token   func(context.Context) (string, error)
	HTTP    *http.Client
}

// NewClient builds a policy client for baseURL, minting the service-service bearer
// from signer (nil signer → no bearer, for dev/insecure policy).
func NewClient(baseURL string, signer *auth.Signer) *Client {
	return &Client{
		BaseURL: baseURL,
		Token: func(context.Context) (string, error) {
			if signer == nil {
				return "", nil
			}
			return signer.IssueService("api", "policy-service")
		},
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

// Decide returns policy-service's typed decision for a session-start. A non-200
// response is an error (so the caller fails closed).
func (c *Client) Decide(ctx context.Context, in DecideInput) (Decision, error) {
	if c.BaseURL == "" {
		return Decision{}, fmt.Errorf("policy URL not configured")
	}
	body, err := json.Marshal(decideRequest{
		SessionID: in.SessionID, Subject: in.Subject, Edge: in.Edge,
		Target: in.Target, Action: in.Action, RequiresApproval: in.RequiresApproval,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("marshal decide request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/decisions", bytes.NewReader(body))
	if err != nil {
		return Decision{}, fmt.Errorf("build decide request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return Decision{}, fmt.Errorf("mint policy token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Decision{}, fmt.Errorf("decide request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Decision{}, fmt.Errorf("policy decide: unexpected status %d", resp.StatusCode)
	}
	var d Decision
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d); err != nil {
		return Decision{}, fmt.Errorf("decode decision: %w", err)
	}
	return d, nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}
