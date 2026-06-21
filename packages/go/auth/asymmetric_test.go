package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

// iss / aud consts are declared in auth_test.go (same package).

func TestAsymmetricRoundTrip(t *testing.T) {
	sk, pk, _, _ := GenerateAsymmetricKey()
	tok, err := NewAsymmetricSigner(sk, iss).IssueService("svc:mcp-gateway", aud)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ct, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ct.Subject != "svc:mcp-gateway" || ct.Edge != "service-service" {
		t.Fatalf("unexpected claims: %+v", ct)
	}
	if ct.Audience != aud || ct.Issuer != iss {
		t.Fatalf("aud/iss not preserved: %+v", ct)
	}
	if ct.TokenID == "" || ct.Expiry.Before(time.Now()) {
		t.Fatalf("jti/exp not set: %+v", ct)
	}
}

func TestAsymmetricIssueAgentScope(t *testing.T) {
	sk, pk, _, _ := GenerateAsymmetricKey()
	scope := Scope{Resource: "res-9", Operations: []string{"exec"}}
	tok, err := NewAsymmetricSigner(sk, iss).IssueAgent("agent:wedge", "mcp-gateway", scope)
	if err != nil {
		t.Fatalf("issue agent: %v", err)
	}
	ct, err := NewAsymmetricVerifier(pk, iss, "mcp-gateway").Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ct.Edge != "agent-tool" || ct.Scope.Resource != "res-9" || len(ct.Scope.Operations) != 1 {
		t.Fatalf("agent claims/scope not preserved: %+v", ct)
	}
	if d := time.Until(ct.Expiry); d <= 0 || d > AgentTTL+time.Second {
		t.Fatalf("agent TTL = %s, want ~%s", d, AgentTTL)
	}
	// Audience-alignment (ADR-0050 class): an agent token for the gateway must NOT
	// verify for audience "api".
	if _, err := NewAsymmetricVerifier(pk, iss, "api").Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("agent token must not verify for audience api, got %v", err)
	}
}

func TestAsymmetricWrongPublicKeyRejected(t *testing.T) {
	skA, _, _, _ := GenerateAsymmetricKey()
	_, pkB, _, _ := GenerateAsymmetricKey()
	tok, _ := NewAsymmetricSigner(skA, iss).IssueService("svc", aud)
	if _, err := NewAsymmetricVerifier(pkB, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for wrong public key, got %v", err)
	}
}

func TestAsymmetricTamperRejected(t *testing.T) {
	sk, pk, _, _ := GenerateAsymmetricKey()
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc", aud)
	tampered := tok[:len(tok)-2] + "xx"
	if _, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for tampered token, got %v", err)
	}
}

func TestAsymmetricExpiredRejected(t *testing.T) {
	sk, pk, _, _ := GenerateAsymmetricKey()
	tok, err := NewAsymmetricSigner(sk, iss).Issue(IssueParams{
		Subject: "s", Edge: "service-service", Audience: aud, TTL: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for expired token, got %v", err)
	}
}

func TestAsymmetricWrongAudienceRejected(t *testing.T) {
	sk, pk, _, _ := GenerateAsymmetricKey()
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("s", "policy-service")
	if _, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for wrong audience, got %v", err)
	}
}

// TestTokenTypeIsolation is the property the dual-mode verifier (T-046) relies on:
// a v4.public token never verifies under the symmetric Verifier, and a v4.local
// token never verifies under the AsymmetricVerifier. This is what makes the
// type-dispatch in KEY-2 safe (ADR-0060 §4).
func TestTokenTypeIsolation(t *testing.T) {
	symKey, _ := GenerateKey()
	sk, pk, _, _ := GenerateAsymmetricKey()

	publicTok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc", aud)
	if _, err := NewVerifier(symKey, iss, aud).Verify(context.Background(), publicTok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("v4.public token must not verify under the symmetric verifier, got %v", err)
	}

	localTok, _ := NewSigner(symKey, iss).IssueService("svc", aud)
	if _, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), localTok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("v4.local token must not verify under the asymmetric verifier, got %v", err)
	}
}

func TestAsymmetricHexRoundTrip(t *testing.T) {
	_, _, skHex, pkHex := GenerateAsymmetricKey()
	sk, err := NewAsymmetricSecretKeyFromHex(skHex)
	if err != nil {
		t.Fatalf("load secret hex: %v", err)
	}
	pk, err := NewAsymmetricPublicKeyFromHex(pkHex)
	if err != nil {
		t.Fatalf("load public hex: %v", err)
	}
	tok, err := NewAsymmetricSigner(sk, iss).IssueService("svc", aud)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := NewAsymmetricVerifier(pk, iss, aud).Verify(context.Background(), tok); err != nil {
		t.Fatalf("verify after hex round-trip: %v", err)
	}
	// Malformed hex is a typed load error, not a panic.
	if _, err := NewAsymmetricSecretKeyFromHex("zzzz"); err == nil {
		t.Fatal("expected error for malformed secret-key hex")
	}
	if _, err := NewAsymmetricPublicKeyFromHex("zzzz"); err == nil {
		t.Fatal("expected error for malformed public-key hex")
	}
}
