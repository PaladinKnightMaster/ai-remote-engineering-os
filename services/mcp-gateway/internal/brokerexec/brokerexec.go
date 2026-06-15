// Package brokerexec is the mcp-gateway's client for the session-broker's INTERNAL
// non-interactive exec RPC (ADR-0058). The gateway never dials SSH — the broker
// stays the sole egress engine; this just hands it a governed command to run.
package brokerexec

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

// Client calls the broker POST /v1/exec with a service-service bearer
// (IssueService("mcp-gateway","session-broker")). The HTTP timeout is comfortably
// longer than the broker's own command timeout (BROKER_EXEC_TIMEOUT_SECONDS, 60s
// default) so the broker's bound governs, not the client's.
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
			return signer.IssueService("mcp-gateway", "session-broker")
		},
		http: &http.Client{Timeout: 120 * time.Second},
	}
}

type execRequest struct {
	ResourceID   string `json:"resourceId"`
	Command      string `json:"command"`
	InvocationID string `json:"invocationId"`
	Subject      string `json:"subject"`
}

type execResponse struct {
	Stdout    string `json:"stdout"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

// Exec runs command on resourceID via the broker, returning its captured stdout +
// exit status. invocationID is the correlation anchor; subject is the agent (the
// audit actor). A non-200 is an error so the caller fails closed (SEC-19).
func (c *Client) Exec(ctx context.Context, invocationID, resourceID, command, subject string) (stdout string, exitCode int, truncated bool, err error) {
	if c.baseURL == "" {
		return "", 0, false, errors.New("broker URL not configured")
	}
	body, err := json.Marshal(execRequest{
		ResourceID: resourceID, Command: command, InvocationID: invocationID, Subject: subject,
	})
	if err != nil {
		return "", 0, false, fmt.Errorf("marshal exec request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/exec", bytes.NewReader(body))
	if err != nil {
		return "", 0, false, fmt.Errorf("build exec request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return "", 0, false, fmt.Errorf("mint broker token: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, false, fmt.Errorf("exec request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false, fmt.Errorf("broker exec: unexpected status %d", resp.StatusCode)
	}
	var r execResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return "", 0, false, fmt.Errorf("decode exec response: %w", err)
	}
	return r.Stdout, r.ExitCode, r.Truncated, nil
}
