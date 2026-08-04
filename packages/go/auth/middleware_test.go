package auth

import (
	"context"
	"testing"
)

// TestAsMiddlewareVerifierCarriesTokenIDAndScopeName is T-114b's named test (the
// ticket's own §1): AsMiddlewareVerifier's adapter must not drop the jti or the
// named scope when it builds mw.Capability from CapabilityToken — the gap this
// ticket closes (packages/go/auth/middleware.go:33 previously left both behind,
// so services/api's paired-session liveness check (SEC-34) had nothing to look
// up). A paired token round-trips both; an ordinary operator token round-trips
// with both empty, so this addition is provably additive.
func TestAsMiddlewareVerifierCarriesTokenIDAndScopeName(t *testing.T) {
	key, _ := GenerateKey()
	signer := NewSigner(key, iss)
	verifier := AsMiddlewareVerifier(NewVerifier(key, iss, "api"))

	pairedTok, err := signer.IssuePaired("operator:alice", "api", "sess-fixed-id", Scope{Name: "approvals@1"})
	if err != nil {
		t.Fatalf("issue paired: %v", err)
	}
	cap, err := verifier.Verify(context.Background(), pairedTok)
	if err != nil {
		t.Fatalf("verify paired: %v", err)
	}
	if cap.TokenID != "sess-fixed-id" {
		t.Fatalf("paired: TokenID = %q, want %q", cap.TokenID, "sess-fixed-id")
	}
	if cap.Scope.Name != "approvals@1" {
		t.Fatalf("paired: Scope.Name = %q, want approvals@1", cap.Scope.Name)
	}
	if cap.Edge != "human-agent" {
		t.Fatalf("paired: Edge = %q, want human-agent (a scope WITHIN human-agent, ADR-0068 §D5)", cap.Edge)
	}

	opTok, err := signer.IssueOperator("operator:bob", "api")
	if err != nil {
		t.Fatalf("issue operator: %v", err)
	}
	opCap, err := verifier.Verify(context.Background(), opTok)
	if err != nil {
		t.Fatalf("verify operator: %v", err)
	}
	if opCap.TokenID == "" {
		t.Fatal("operator: TokenID must still be populated (every token has a jti, not just paired ones)")
	}
	if opCap.Scope.Name != "" {
		t.Fatalf("operator: Scope.Name = %q, want empty (an ordinary token never sets it)", opCap.Scope.Name)
	}
}
