package auth

import (
	"context"
	"errors"
	"testing"
)

// iss / aud consts are declared in auth_test.go (same package).

func TestDualModeRoutesLocal(t *testing.T) {
	symKey, _ := GenerateKey()
	_, pk, _, _ := GenerateAsymmetricKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), NewAsymmetricVerifier(pk, iss, aud))
	tok, _ := NewSigner(symKey, iss).IssueService("svc", aud)
	ct, err := d.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("local token via dual-mode: %v", err)
	}
	if ct.Subject != "svc" || ct.Edge != "service-service" {
		t.Fatalf("claims: %+v", ct)
	}
}

func TestDualModeRoutesPublic(t *testing.T) {
	symKey, _ := GenerateKey()
	sk, pk, _, _ := GenerateAsymmetricKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), NewAsymmetricVerifier(pk, iss, aud))
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc-gw", aud)
	ct, err := d.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("public token via dual-mode: %v", err)
	}
	if ct.Subject != "svc-gw" {
		t.Fatalf("claims: %+v", ct)
	}
}

func TestDualModePublicWithoutAsymmetricRejected(t *testing.T) {
	symKey, _ := GenerateKey()
	sk, _, _, _ := GenerateAsymmetricKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), nil) // symmetric-only
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc", aud)
	if _, err := d.Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken when no asymmetric verifier, got %v", err)
	}
}

func TestDualModeUnrecognizedRejected(t *testing.T) {
	symKey, _ := GenerateKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), nil)
	if _, err := d.Verify(context.Background(), "not-a-paseto-token"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for garbage, got %v", err)
	}
}

// Audience/edge rules still apply through the dispatch — a public token for the
// wrong audience is rejected by the asymmetric half, not waved through.
func TestDualModePublicWrongAudienceRejected(t *testing.T) {
	symKey, _ := GenerateKey()
	sk, pk, _, _ := GenerateAsymmetricKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), NewAsymmetricVerifier(pk, iss, aud))
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc", "policy-service") // != aud
	if _, err := d.Verify(context.Background(), tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected rejection for wrong audience, got %v", err)
	}
}

// The dual-mode verifier wraps into middleware.Verifier via AsMiddlewareVerifier.
func TestDualModeAsMiddleware(t *testing.T) {
	symKey, _ := GenerateKey()
	sk, pk, _, _ := GenerateAsymmetricKey()
	d := NewDualModeVerifier(NewVerifier(symKey, iss, aud), NewAsymmetricVerifier(pk, iss, aud))
	tok, _ := NewAsymmetricSigner(sk, iss).IssueService("svc:x", aud)
	cap, err := AsMiddlewareVerifier(d).Verify(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	if cap.Edge != "service-service" || cap.Subject != "svc:x" {
		t.Fatalf("adapter dropped claims: %+v", cap)
	}
}
