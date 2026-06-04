// Package connector is the session-broker's client for connector-service's
// internal credential-lease RPC (ADR-0053). At v0.2.0 connector-service is the
// sole holder of AIREOS_RESOURCE_SECRET_KEY; the broker no longer decrypts target
// credentials itself (T-030, SEC-13). Instead, at WebSocket-channel connect it
// leases the decrypted SSH material from connector-service over an internal,
// service-service-authenticated POST — the egress is audited atomically on the
// connector side (store.Lease), so there is no unaudited credential egress.
//
// This endpoint is deliberately NOT in the shared OpenAPI fragment: its response
// carries decrypted credential material that must never reach the browser TS
// client generated from that fragment (T-029b consult; ADR-0053).
package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrNoCredential maps connector-service's 404 (no credential stored for the
// resource). The channel handler treats it the same as an unavailable target —
// the session cannot be brokered (424 Failed Dependency).
var ErrNoCredential = errors.New("connector has no credential for resource")

// Material is the decrypted SSH auth material returned by the lease. It is
// byte-identical to connector-service's lease response shape.
type Material struct {
	PrivateKey string `json:"private_key"`
	Passphrase string `json:"passphrase,omitempty"`
}

// Client calls connector-service's POST /v1/credentials/lease. Token supplies the
// service-service bearer (IssueService("session-broker","connector-service")); it
// may be nil or return "" for dev/insecure connectors. HTTP defaults to a 10s
// client when nil.
type Client struct {
	BaseURL string
	Token   func(context.Context) (string, error)
	HTTP    *http.Client
}

type leaseRequest struct {
	ResourceID      string `json:"resource_id"`
	SessionID       string `json:"session_id"`
	OperatorSubject string `json:"operator_subject"`
}

// Lease requests the decrypted credential for resourceID on behalf of the
// operator running sessionID. operatorSubject + sessionID are broker-supplied so
// connector attributes the egress event without any cross-service table read.
// Returns ErrNoCredential on a 404; any other non-200 is an error.
func (c *Client) Lease(ctx context.Context, resourceID, sessionID, operatorSubject string) (Material, error) {
	if c.BaseURL == "" {
		return Material{}, errors.New("connector URL not configured")
	}
	body, err := json.Marshal(leaseRequest{
		ResourceID: resourceID, SessionID: sessionID, OperatorSubject: operatorSubject,
	})
	if err != nil {
		return Material{}, fmt.Errorf("marshal lease request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/credentials/lease", bytes.NewReader(body))
	if err != nil {
		return Material{}, fmt.Errorf("build lease request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return Material{}, fmt.Errorf("mint connector token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Material{}, fmt.Errorf("lease request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var m Material
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&m); err != nil {
			return Material{}, fmt.Errorf("decode lease response: %w", err)
		}
		return m, nil
	case http.StatusNotFound:
		return Material{}, ErrNoCredential
	default:
		return Material{}, fmt.Errorf("connector lease: unexpected status %d", resp.StatusCode)
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}
