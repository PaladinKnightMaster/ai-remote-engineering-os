package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

const (
	iss = "cluster-test"
	aud = "audit-service"
)

func TestIssueVerifyRoundTrip(t *testing.T) {
	key, _ := GenerateKey()
	tok, err := NewSigner(key, iss).IssueService("svc:policy-service", aud)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ct, err := NewVerifier(key, iss, aud).Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ct.Subject != "svc:policy-service" || ct.Edge != "service-service" {
		t.Fatalf("unexpected claims: %+v", ct)
	}
	if ct.Audience != aud || ct.Issuer != iss {
		t.Fatalf("aud/iss not preserved: %+v", ct)
	}
	if ct.TokenID == "" || ct.Expiry.Before(time.Now()) {
		t.Fatalf("jti/exp not set: %+v", ct)
	}
}

func TestScopePreserved(t *testing.T) {
	key, _ := GenerateKey()
	sc := Scope{Resource: "res-1", Operations: []string{"read", "append"}, LeaseID: "lease-9"}
	tok, err := NewSigner(key, iss).Issue(IssueParams{
		Subject: "agent-1", Edge: "agent-tool", Audience: aud, Scope: sc, TTL: AgentTTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	ct, err := NewVerifier(key, iss, aud).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if ct.Scope.Resource != "res-1" || ct.Scope.LeaseID != "lease-9" || len(ct.Scope.Operations) != 2 {
		t.Fatalf("scope not preserved: %+v", ct.Scope)
	}
}

func TestIssueAgent(t *testing.T) {
	key, _ := GenerateKey()
	sc := Scope{Resource: "res-9", Operations: []string{"exec"}}
	tok, err := NewSigner(key, iss).IssueAgent("agent:wedge", "mcp-gateway", sc)
	if err != nil {
		t.Fatalf("issue agent: %v", err)
	}
	// Verifies under the gateway's verifier config (audience mcp-gateway).
	ct, err := NewVerifier(key, iss, "mcp-gateway").Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ct.Subject != "agent:wedge" || ct.Edge != "agent-tool" {
		t.Fatalf("unexpected claims: %+v", ct)
	}
	if ct.Scope.Resource != "res-9" || len(ct.Scope.Operations) != 1 {
		t.Fatalf("scope not preserved: %+v", ct.Scope)
	}
	// Cross-service guard: an agent token for the gateway must NOT verify for
	// audience "api" (the audience-alignment class behind ADR-0050).
	if _, err := NewVerifier(key, iss, "api").Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("agent token must not verify for audience api, got %v", err)
	}
	// The TTL is the 5-minute agent tier.
	if d := time.Until(ct.Expiry); d <= 0 || d > AgentTTL+time.Second {
		t.Fatalf("agent TTL = %s, want ~%s", d, AgentTTL)
	}
}

func TestExpiredRejected(t *testing.T) {
	key, _ := GenerateKey()
	tok, err := NewSigner(key, iss).Issue(IssueParams{
		Subject: "s", Edge: "service-service", Audience: aud, TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := NewVerifier(key, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestWrongAudienceRejected(t *testing.T) {
	key, _ := GenerateKey()
	tok, _ := NewSigner(key, iss).IssueService("s", "policy-service")
	if _, err := NewVerifier(key, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for wrong audience, got %v", err)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	a, _ := GenerateKey()
	b, _ := GenerateKey()
	tok, _ := NewSigner(a, iss).IssueService("s", aud)
	if _, err := NewVerifier(b, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for wrong key, got %v", err)
	}
}

func TestTamperRejected(t *testing.T) {
	key, _ := GenerateKey()
	tok, _ := NewSigner(key, iss).IssueService("s", aud)
	tampered := tok[:len(tok)-2] + "xx"
	if _, err := NewVerifier(key, iss, aud).Verify(context.Background(), tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for tampered token, got %v", err)
	}
}

// TestIssuePaired proves the paired-session tier (ADR-0068 §D4/§D5): the jti
// is the supplied tokenID verbatim (the per-request liveness lookup key) and
// the edge is human-agent (no eighth capability edge).
func TestIssuePaired(t *testing.T) {
	key, _ := GenerateKey()
	tok, err := NewSigner(key, iss).IssuePaired("operator-1", "api", "sess-abc123", Scope{Resource: "approvals@1"})
	if err != nil {
		t.Fatalf("issue paired: %v", err)
	}
	ct, err := NewVerifier(key, iss, "api").Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ct.Edge != "human-agent" {
		t.Fatalf("paired session must carry edge human-agent, got %q", ct.Edge)
	}
	if ct.TokenID != "sess-abc123" {
		t.Fatalf("jti = %q, want the supplied tokenID", ct.TokenID)
	}
	if ct.Subject != "operator-1" {
		t.Fatalf("unexpected subject: %+v", ct)
	}
	if d := time.Until(ct.Expiry); d <= 0 || d > PairedTTL+time.Second {
		t.Fatalf("paired TTL = %s, want ~%s", d, PairedTTL)
	}
}

// TestIssueParamsEmptyTokenIDStillRandom is the no-regression proof for every
// existing caller (IssueOperator, IssueAgent, IssueService, and any direct
// Issue call that never sets TokenID): buildToken's additive TokenID branch
// must not change the prior random-uuid behavior when TokenID is left empty —
// two mints produce two DIFFERENT jtis.
func TestIssueParamsEmptyTokenIDStillRandom(t *testing.T) {
	key, _ := GenerateKey()
	signer := NewSigner(key, iss)
	tok1, err := signer.Issue(IssueParams{Subject: "s", Edge: "service-service", Audience: aud, TTL: ServiceTTL})
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := signer.Issue(IssueParams{Subject: "s", Edge: "service-service", Audience: aud, TTL: ServiceTTL})
	if err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(key, iss, aud)
	ct1, err := v.Verify(context.Background(), tok1)
	if err != nil {
		t.Fatal(err)
	}
	ct2, err := v.Verify(context.Background(), tok2)
	if err != nil {
		t.Fatal(err)
	}
	if ct1.TokenID == "" || ct2.TokenID == "" {
		t.Fatalf("jti must be set even with TokenID empty: %q / %q", ct1.TokenID, ct2.TokenID)
	}
	if ct1.TokenID == ct2.TokenID {
		t.Fatalf("two mints with empty TokenID produced the SAME jti %q — random uuid regression", ct1.TokenID)
	}
}

// The adapter yields a middleware.Capability carrying the verified edge.
func TestAsMiddlewareVerifier(t *testing.T) {
	key, _ := GenerateKey()
	tok, _ := NewSigner(key, iss).IssueService("svc:x", aud)
	cap, err := AsMiddlewareVerifier(NewVerifier(key, iss, aud)).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Edge != "service-service" || cap.Subject != "svc:x" {
		t.Fatalf("adapter dropped claims: %+v", cap)
	}
}
