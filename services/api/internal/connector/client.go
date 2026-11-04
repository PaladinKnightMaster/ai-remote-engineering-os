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

func (c *Client) Generate(ctx context.Context, resourceID, operator string, rotate bool) (Public, error) {
	return c.call(ctx, http.MethodPost, "/v1/credentials/"+resourceID+"/generate",
		map[string]any{"rotate": rotate, "operator_subject": operator})
}

func (c *Client) PublicKey(ctx context.Context, resourceID string) (Public, error) {
	return c.call(ctx, http.MethodGet, "/v1/credentials/"+resourceID+"/public", nil)
}

func (c *Client) call(ctx context.Context, method, path string, body any) (Public, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return Public{}, fmt.Errorf("build connector request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if tok, err := c.Token(ctx); err != nil {
		return Public{}, fmt.Errorf("mint connector token: %w", err)
	} else if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Public{}, fmt.Errorf("connector request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		return parsePublic(io.LimitReader(resp.Body, 16<<10))
	case http.StatusConflict:
		return Public{}, ErrExists
	case http.StatusNotFound:
		return Public{}, ErrNotFound
	default: // the body is never read into an error: it is not ours to relay
		return Public{}, fmt.Errorf("connector: unexpected status %d", resp.StatusCode)
	}
}

func parsePublic(r io.Reader) (Public, error) {
	raw, _ := io.ReadAll(r)
	if bytes.Contains(raw, []byte("PRIVATE")) || bytes.Contains(raw, []byte("-----")) {
		return Public{}, fmt.Errorf("%w: private-key marker in response (dropped)", ErrBadResponse)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p Public
	if err := dec.Decode(&p); err != nil || !pubLine.MatchString(p.PublicKey) || !fpLine.MatchString(p.Fingerprint) {
		return Public{}, fmt.Errorf("%w: not a public key and fingerprint", ErrBadResponse)
	}
	return p, nil
}
