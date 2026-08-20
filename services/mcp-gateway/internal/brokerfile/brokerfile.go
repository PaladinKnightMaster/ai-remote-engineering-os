// Package brokerfile is the mcp-gateway's client for the session-broker's
// INTERNAL file-transfer RPC (ADR-0071 §D4). The gateway never dials SFTP —
// the broker stays the sole egress engine (SEC-18); this just hands it a
// governed file operation. Mirrors internal/brokerexec's shape exactly, but is
// kept as its own client rather than folded into BrokerExecer: the T-138 seam
// (BrokerExecer / performAndFinish's remote.exec leg) stays untouched, and a
// second egress family gets its own interface (catalog.BrokerFiler) instead of
// widening the first one.
package brokerfile

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

// Client calls the broker POST /v1/file with a service-service bearer
// (IssueService("mcp-gateway","session-broker")). The HTTP timeout is
// comfortably longer than the broker's own file-operation timeout
// (BROKER_FILE_TIMEOUT_SECONDS, 60s default, session-broker/main.go:389) so the
// broker's bound governs, not the client's — the same reasoning as
// brokerexec.Client.
type Client struct {
	baseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

func New(baseURL string, signer *auth.AsymmetricSigner) *Client {
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

// fileRequest mirrors the broker's fileRequest wire shape (T-139b,
// session-broker/internal/server/server.go) — only the fields a read needs are
// populated; Content/Mode stay zero (omitempty on the broker side).
type fileRequest struct {
	Op           string `json:"op"`
	ResourceID   string `json:"resourceId"`
	Path         string `json:"path"`
	InvocationID string `json:"invocationId"`
	Subject      string `json:"subject"`
}

// fileResponse mirrors the broker's fileResponse wire shape; Entries is
// omitted here (list is T-142, out of scope for this client).
type fileResponse struct {
	Content []byte `json:"content,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
}

// Read fetches one file's content plus its sha256 digest via the broker's
// POST /v1/file (op="read"). invocationID is the correlation anchor; subject
// is the agent (the audit actor). A non-200 is an error so the caller fails
// closed (SEC-19) — the broker's distinct statuses (404/403/413/504/422/502)
// collapse to one Go error here, exactly as brokerexec.Client.Exec does for
// /v1/exec; performAndFinish's fail-closed handling doesn't need to
// distinguish them.
func (c *Client) Read(ctx context.Context, invocationID, resourceID, path, subject string) (content []byte, sha256sum string, err error) {
	if c.baseURL == "" {
		return nil, "", errors.New("broker URL not configured")
	}
	body, err := json.Marshal(fileRequest{
		Op: "read", ResourceID: resourceID, Path: path, InvocationID: invocationID, Subject: subject,
	})
	if err != nil {
		return nil, "", fmt.Errorf("marshal file request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/file", bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("build file request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("mint broker token: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("file request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("broker file: unexpected status %d", resp.StatusCode)
	}
	var r fileResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, "", fmt.Errorf("decode file response: %w", err)
	}
	return r.Content, r.SHA256, nil
}
