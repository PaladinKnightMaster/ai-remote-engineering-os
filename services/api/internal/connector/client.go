// Package connector is services/api's client for connector-service's api-only key
// generation plane (ADR-0079). api never holds a private key, not even in transit:
// every response is checked to carry public material ONLY before it is relayed, and
// a body that fails the check is dropped, never forwarded or logged.
package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"aireos.local/packages/go/auth"
)

var (
	ErrExists      = errors.New("connector: credential already exists")
	ErrNotFound    = errors.New("connector: not found")
	ErrBadResponse = errors.New("connector: unusable response")
)

type Public struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}

var (
	pubLine = regexp.MustCompile(`^ssh-ed25519 [A-Za-z0-9+/]+={0,2}$`)
	fpLine  = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]+={0,2}$`)
)

type Client struct {
	BaseURL string
	Token   func(context.Context) (string, error)
	HTTP    *http.Client
}

func NewClient(baseURL string, signer *auth.Signer) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: func(context.Context) (string, error) {
		if signer == nil {
			return "", nil
		}
		return signer.IssueService("api", "connector-service")
	}}
}

// Pending is a pending credential's public half plus the non-secret metadata the
// installer recorded; Public is all a generate/public response may carry.
type Pending struct {
	Public
	Account string `json:"account,omitempty"`
	HostKey string `json:"host_key,omitempty"`
}

func (c *Client) Generate(ctx context.Context, resourceID, operator string, rotate bool) (Public, error) {
	p, err := c.call(ctx, http.MethodPost, "/v1/credentials/"+resourceID+"/generate",
		map[string]any{"rotate": rotate, "operator_subject": operator})
	return p.Public, err
}

func (c *Client) PublicKey(ctx context.Context, resourceID string) (Public, error) {
	p, err := c.call(ctx, http.MethodGet, "/v1/credentials/"+resourceID+"/public", nil)
	return p.Public, err
}

// Pending reads the install-time pending credential's metadata (ErrNotFound if none).
func (c *Client) Pending(ctx context.Context, label string) (Pending, error) {
	return c.call(ctx, http.MethodGet, "/v1/credentials/pending/"+label, nil)
}

// Bind moves the pending credential onto resourceID; operator becomes the audit actor.
func (c *Client) Bind(ctx context.Context, label, resourceID, operator string) (Public, error) {
	p, err := c.call(ctx, http.MethodPost, "/v1/credentials/pending/"+label+"/bind",
		map[string]any{"resource_id": resourceID, "operator_subject": operator})
	return p.Public, err
}

func (c *Client) call(ctx context.Context, method, path string, body any) (Pending, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return Pending{}, fmt.Errorf("build connector request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok, err := c.Token(ctx); err != nil {
		return Pending{}, fmt.Errorf("mint connector token: %w", err)
	} else if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Pending{}, fmt.Errorf("connector request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return parse(io.LimitReader(resp.Body, 16<<10))
	case http.StatusConflict:
		return Pending{}, ErrExists
	case http.StatusNotFound:
		return Pending{}, ErrNotFound
	default: // the body is never read into an error: it is not ours to relay
		return Pending{}, fmt.Errorf("connector: unexpected status %d", resp.StatusCode)
	}
}

var acctLine = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

func parse(r io.Reader) (Pending, error) {
	raw, _ := io.ReadAll(r)
	if bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("-----")) {
		return Pending{}, fmt.Errorf("%w: private-key marker in response (dropped)", ErrBadResponse)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Pending
	if err := dec.Decode(&p); err != nil || !pubLine.MatchString(p.PublicKey) || !fpLine.MatchString(p.Fingerprint) ||
		(p.Account != "" && !acctLine.MatchString(p.Account)) || strings.ContainsAny(p.HostKey, "\r\n") {
		return Pending{}, fmt.Errorf("%w: not a public key, fingerprint and metadata", ErrBadResponse)
	}
	return p, nil
}
