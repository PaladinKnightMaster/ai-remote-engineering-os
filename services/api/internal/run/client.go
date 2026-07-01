package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StepRequest is one governed step the sequencer dispatches to the gateway's
// subject-pinned /v1/steps route (T-055b). Subject is the launching operator.
type StepRequest struct {
	Subject        string // the ACTOR (agent for a delegated Run, operator for a direct Run)
	Principal      string // the PRINCIPAL (delegating operator) for a delegated Run; "" for a direct Run (ADR-0062, T-065b)
	ResourceID     string
	Command        string
	IdempotencyKey string
}

// StepResult is the gateway's governed-step outcome: InvocationID is the gateway
// invocation id (the run→invocation correlation anchor, AUD-15; empty for a refusal
// that created none); IsError marks a governance refusal the sequencer must stop on
// (denied / timeout / concurrency); Text is the tool-result text (stdout on success,
// the refusal reason otherwise).
type StepResult struct {
	InvocationID string
	IsError      bool
	Text         string
}

// StepDriver dispatches one governed step. httpStepClient is the production
// implementation; the sequencer depends on this interface so it stays testable and
// extraction-ready (RUN-1).
type StepDriver interface {
	RunStep(ctx context.Context, req StepRequest) (StepResult, error)
}

// ServiceIssuer mints the api→gateway service-service bearer (asymmetric, ADR-0060).
// Both *auth.AsymmetricSigner and *auth.Signer satisfy it; callers pass a concrete
// non-nil signer or an untyped nil (never a typed-nil) so the nil check holds.
type ServiceIssuer interface {
	IssueService(subject, audience string) (string, error)
}

// httpStepClient drives /v1/steps over HTTP with a LONG timeout — a step may block on
// the gateway's Run-approval window (RunWaitTimeout, ~10m), so the client timeout must
// exceed it.
type httpStepClient struct {
	baseURL string
	token   func(context.Context) (string, error)
	http    *http.Client
}

// NewStepClient builds the gateway-step client. A nil issuer → no bearer (dev).
func NewStepClient(baseURL string, issuer ServiceIssuer) *httpStepClient {
	return &httpStepClient{
		baseURL: baseURL,
		token: func(context.Context) (string, error) {
			if issuer == nil {
				return "", nil
			}
			return issuer.IssueService("api", "mcp-gateway")
		},
		http: &http.Client{Timeout: 15 * time.Minute}, // > gateway RunWaitTimeout (10m)
	}
}

// toolResultWire is the gateway /v1/steps response: the invocation id + the MCP
// ToolResult (content + isError).
type toolResultWire struct {
	InvocationID string `json:"invocationId"`
	Content      []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// RunStep posts one governed step to the gateway and returns its outcome. A non-200
// is an infrastructure error (the step did not run — fail-closed).
func (c *httpStepClient) RunStep(ctx context.Context, req StepRequest) (StepResult, error) {
	body, err := json.Marshal(map[string]string{
		"subject":        req.Subject,
		"principal":      req.Principal,
		"resourceId":     req.ResourceID,
		"command":        req.Command,
		"idempotencyKey": req.IdempotencyKey,
	})
	if err != nil {
		return StepResult{}, fmt.Errorf("marshal step: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/steps", bytes.NewReader(body))
	if err != nil {
		return StepResult{}, fmt.Errorf("build step request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	tok, err := c.token(ctx)
	if err != nil {
		return StepResult{}, fmt.Errorf("mint step token: %w", err)
	}
	if tok != "" {
		hreq.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.http.Do(hreq)
	if err != nil {
		return StepResult{}, fmt.Errorf("dispatch step: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return StepResult{}, fmt.Errorf("gateway step: unexpected status %d", resp.StatusCode)
	}
	var wire toolResultWire
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire); err != nil {
		return StepResult{}, fmt.Errorf("decode step result: %w", err)
	}
	var text string
	if len(wire.Content) > 0 {
		text = wire.Content[0].Text
	}
	return StepResult{InvocationID: wire.InvocationID, IsError: wire.IsError, Text: text}, nil
}

var _ StepDriver = (*httpStepClient)(nil)
