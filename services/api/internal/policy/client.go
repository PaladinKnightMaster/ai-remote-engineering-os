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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aireos.local/packages/go/auth"
)

// Decision values returned by decide (ADR-0054 §1, ADR-0074 §D2).
const (
	DecisionAllow            = "allow"
	DecisionRequiresApproval = "requires_approval"
	DecisionDeny             = "deny"
)

// ErrNotPending maps policy-service's 409 on resolve — the session has no
// still-pending approval (already resolved, or the approval expired/timed out).
// The api enforcement handler reconciles this to a closed session.
var ErrNotPending = errors.New("policy: no pending approval for session")

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
	body, err := json.Marshal(decideRequest(in))
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

// ResolveResult is policy-service's resolve response.
type ResolveResult struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type resolveRequest struct {
	Decision   string `json:"decision"` // granted | denied
	ResolvedBy string `json:"resolved_by"`
}

// Resolve records the operator's approve/deny on a parked session via
// policy-service's POST /v1/approvals/{sessionID}/resolve. decision is the policy
// vocabulary (granted|denied). A 409 (no still-pending approval — already resolved
// or timed out) surfaces as ErrNotPending; any other non-200 is an error.
func (c *Client) Resolve(ctx context.Context, sessionID, decision, resolvedBy string) (ResolveResult, error) {
	if c.BaseURL == "" {
		return ResolveResult{}, fmt.Errorf("policy URL not configured")
	}
	body, err := json.Marshal(resolveRequest{Decision: decision, ResolvedBy: resolvedBy})
	if err != nil {
		return ResolveResult{}, fmt.Errorf("marshal resolve request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/v1/approvals/"+sessionID+"/resolve", bytes.NewReader(body))
	if err != nil {
		return ResolveResult{}, fmt.Errorf("build resolve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return ResolveResult{}, fmt.Errorf("mint policy token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return ResolveResult{}, fmt.Errorf("resolve request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var rr ResolveResult
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rr); err != nil {
			return ResolveResult{}, fmt.Errorf("decode resolve result: %w", err)
		}
		return rr, nil
	case http.StatusConflict:
		return ResolveResult{}, ErrNotPending
	default:
		return ResolveResult{}, fmt.Errorf("policy resolve: unexpected status %d", resp.StatusCode)
	}
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// --- rule CRUD forwarding (ADR-0074, T-151a/T-151b) --------------------------

// Rule is policy-service's wire shape for a policy_rules row (rules_handler.go's
// ruleResponse). services/api's operator-facing /v1/policy-rules surface (T-151b,
// SEC-53) forwards to policy-service's internal, off-fragment /v1/rules plane
// (T-151a) rather than re-implementing rule storage — the same forwarding
// posture Decide/Resolve above already establish for decisions and approvals.
type Rule struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Subject   string `json:"subject"`
	Edge      string `json:"edge"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Outcome   string `json:"outcome"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ErrRuleNotFound maps policy-service's 404 on the rule routes (unknown id, or
// (CON-7) a malformed one — policy-service's store maps both to the same
// underlying error, so this client does too).
var ErrRuleNotFound = errors.New("policy: rule not found")

// ErrDuplicateRule maps policy-service's 409 on rule create — a rule already
// exists for the (subject, edge, action, target) tuple.
var ErrDuplicateRule = errors.New("policy: a rule already exists for this (subject, edge, action, target) tuple")

// ErrInvalidRule maps policy-service's 400 on rule create, carrying its
// message verbatim (the store.ErrInvalidRule text) rather than a generic
// "bad request" — both sides validate the same tuple vocabulary, so there is
// no reason for api to re-derive or paraphrase policy-service's own reason.
type ErrInvalidRule struct{ Message string }

func (e ErrInvalidRule) Error() string { return e.Message }

// CreateRuleInput is the create-rule request api sends to policy-service.
type CreateRuleInput struct {
	Label   string
	Subject string
	Edge    string
	Action  string
	Target  string
	Outcome string
	// Actor is the operator subject (operatorSubject(r)) that authored this
	// rule (T-151c, AUD-40; renames T-151b's "authored_by" — Commander
	// ruling 2026-08-31: one concept, one name, used uniformly on all four
	// mutating rule methods below, not just CreateRule). NOT the rule's
	// match Subject above (which agent the rule applies to); conflating the
	// two was flagged as a defect waiting to happen.
	Actor string
}

type createRuleRequest struct {
	Label   string `json:"label"`
	Subject string `json:"subject"`
	Edge    string `json:"edge"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Outcome string `json:"outcome"`
	Actor   string `json:"actor"`
}

type patchRuleRequest struct {
	Enabled bool   `json:"enabled"`
	Actor   string `json:"actor"`
}

// deleteRuleRequest carries the DELETE request's only field — actor
// (AUD-40, T-151c).
type deleteRuleRequest struct {
	Actor string `json:"actor"`
}

// ruleRequest issues a rule-CRUD request against policy-service's internal
// /v1/rules plane, minting the same service-service bearer as Decide/Resolve.
// body may be nil (GET/DELETE carry none).
func (c *Client) ruleRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("policy URL not configured")
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != nil {
		tok, err := c.Token(ctx)
		if err != nil {
			return nil, fmt.Errorf("mint policy token: %w", err)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	return resp, nil
}

// ruleErrorFromStatus maps a non-2xx rule-route response to a typed error.
// body is the response body, already read and closed by the caller (needed
// for the 400 path only, since policy-service's writeRuleError writes the
// message as a plain-text body via http.Error).
func ruleErrorFromStatus(status int, body []byte) error {
	switch status {
	case http.StatusNotFound:
		return ErrRuleNotFound
	case http.StatusConflict:
		return ErrDuplicateRule
	case http.StatusBadRequest:
		return ErrInvalidRule{Message: strings.TrimSpace(string(body))}
	default:
		return fmt.Errorf("policy rules: unexpected status %d", status)
	}
}

// CreateRule forwards a rule-author request to policy-service's POST
// /v1/rules.
func (c *Client) CreateRule(ctx context.Context, in CreateRuleInput) (Rule, error) {
	resp, err := c.ruleRequest(ctx, http.MethodPost, "/v1/rules", createRuleRequest(in))
	if err != nil {
		return Rule{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Rule{}, ruleErrorFromStatus(resp.StatusCode, body)
	}
	var r Rule
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	return r, nil
}

// ListRules forwards to policy-service's GET /v1/rules — every rule, enabled
// and disabled alike, newest first (the no-filter contract T-151a's store
// establishes).
func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	resp, err := c.ruleRequest(ctx, http.MethodGet, "/v1/rules", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, ruleErrorFromStatus(resp.StatusCode, body)
	}
	var rules []Rule
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rules); err != nil {
		return nil, fmt.Errorf("decode rules: %w", err)
	}
	return rules, nil
}

// GetRule forwards to policy-service's GET /v1/rules/{id}. ErrRuleNotFound on
// an unknown or malformed id (CON-7 — policy-service maps both alike).
func (c *Client) GetRule(ctx context.Context, id string) (Rule, error) {
	resp, err := c.ruleRequest(ctx, http.MethodGet, "/v1/rules/"+id, nil)
	if err != nil {
		return Rule{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Rule{}, ruleErrorFromStatus(resp.StatusCode, body)
	}
	var r Rule
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	return r, nil
}

// SetRuleEnabled forwards to policy-service's PATCH /v1/rules/{id} — the only
// field the internal contract lets api mutate post-create (SEC-54: a disabled
// rule goes inert without deleting its row). actor is the operator flipping
// it (AUD-40, T-151c) — last parameter, matching Resolve's resolvedBy above.
func (c *Client) SetRuleEnabled(ctx context.Context, id string, enabled bool, actor string) (Rule, error) {
	resp, err := c.ruleRequest(ctx, http.MethodPatch, "/v1/rules/"+id, patchRuleRequest{Enabled: enabled, Actor: actor})
	if err != nil {
		return Rule{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Rule{}, ruleErrorFromStatus(resp.StatusCode, body)
	}
	var r Rule
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return Rule{}, fmt.Errorf("decode rule: %w", err)
	}
	return r, nil
}

// DeleteRule forwards to policy-service's DELETE /v1/rules/{id}. Deleting a
// rule only removes the policy_rules row — decisions already produced under
// it stay intact on the chain (SEC-54). actor is the operator deleting it
// (AUD-40, T-151c) — carried as a JSON body on the DELETE request, the same
// shape PATCH already uses.
func (c *Client) DeleteRule(ctx context.Context, id, actor string) error {
	resp, err := c.ruleRequest(ctx, http.MethodDelete, "/v1/rules/"+id, deleteRuleRequest{Actor: actor})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return ruleErrorFromStatus(resp.StatusCode, body)
	}
	return nil
}
