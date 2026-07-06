package run

import (
	"strings"
	"testing"
	"time"

	"aireos.local/services/api/internal/grant"
)

// TestValidatePlanAgainstGrant — the SEC-29 gate (moved from the server package at
// T-072): accepts a plan within the grant and rejects every out-of-grant case with the
// RIGHT typed kind — OutOfGrant (403; wrong agent, verb/resource out of scope, oversize
// plan) vs GrantDead (403; revoked/expired — the T-073 self-retire trigger). The reason
// strings are wire contract (the live e2e asserts their substrings). Pure — no HTTP/PG.
func TestValidatePlanAgainstGrant(t *testing.T) {
	base := grant.Grant{
		Agent: "agent:bot-1", Status: grant.StatusActive, ExpiresAt: time.Now().Add(time.Hour),
		Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"}, MaxStepsPerRun: 3,
	}
	cases := []struct {
		name       string
		mut        func(*grant.Grant)
		agent      string
		resources  []string
		wantKind   RefusalKind // "" = accept
		wantReason string      // substring the e2e contract pins; "" = skip
	}{
		{"in-grant", nil, "agent:bot-1", []string{"res-a", "res-b"}, "", ""},
		{"wrong agent", nil, "agent:other", []string{"res-a"}, RefusalOutOfGrant, "does not authorize this agent"},
		{"revoked", func(g *grant.Grant) { g.Status = grant.StatusRevoked }, "agent:bot-1", []string{"res-a"}, RefusalGrantDead, "not active"},
		{"expired", func(g *grant.Grant) { g.ExpiresAt = time.Now().Add(-time.Minute) }, "agent:bot-1", []string{"res-a"}, RefusalGrantDead, "expired"},
		{"verb not granted", func(g *grant.Grant) { g.Verbs = []string{"read"} }, "agent:bot-1", []string{"res-a"}, RefusalOutOfGrant, ""},
		{"resource out of grant", nil, "agent:bot-1", []string{"res-a", "res-x"}, RefusalOutOfGrant, ""},
		{"too many steps", nil, "agent:bot-1", []string{"res-a", "res-b", "res-a", "res-b"}, RefusalOutOfGrant, "maximum steps"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := base
			if c.mut != nil {
				c.mut(&g)
			}
			r := validatePlanAgainstGrant(g, c.agent, c.resources)
			if c.wantKind == "" {
				if r != nil {
					t.Fatalf("want accept, got refusal %+v", r)
				}
				return
			}
			if r == nil {
				t.Fatal("want refusal, got accept")
			}
			if r.Kind != c.wantKind {
				t.Fatalf("kind = %q, want %q (reason %q)", r.Kind, c.wantKind, r.Reason)
			}
			if c.wantReason != "" && !strings.Contains(r.Reason, c.wantReason) {
				t.Fatalf("reason %q does not carry the contract substring %q", r.Reason, c.wantReason)
			}
		})
	}
}
