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
