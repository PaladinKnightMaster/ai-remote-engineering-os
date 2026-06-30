package server

import (
	"testing"
	"time"

	"aireos.local/services/api/internal/grant"
)

// TestValidatePlanAgainstGrant — T-065a (SEC-29): the plan-vs-grant gate accepts a plan
// within the grant and rejects every out-of-grant case (wrong agent, revoked, expired, verb
// not granted, a resource outside the grant, too many steps). Pure — no HTTP/PG.
func TestValidatePlanAgainstGrant(t *testing.T) {
	base := grant.Grant{
		Agent: "agent:bot-1", Status: grant.StatusActive, ExpiresAt: time.Now().Add(time.Hour),
		Resources: []string{"res-a", "res-b"}, Verbs: []string{"exec"}, MaxStepsPerRun: 3,
	}
	cases := []struct {
		name      string
		mut       func(*grant.Grant)
		agent     string
		resources []string
		wantOK    bool
	}{
		{"in-grant", nil, "agent:bot-1", []string{"res-a", "res-b"}, true},
		{"wrong agent", nil, "agent:other", []string{"res-a"}, false},
		{"revoked", func(g *grant.Grant) { g.Status = grant.StatusRevoked }, "agent:bot-1", []string{"res-a"}, false},
		{"expired", func(g *grant.Grant) { g.ExpiresAt = time.Now().Add(-time.Minute) }, "agent:bot-1", []string{"res-a"}, false},
		{"verb not granted", func(g *grant.Grant) { g.Verbs = []string{"read"} }, "agent:bot-1", []string{"res-a"}, false},
		{"resource out of grant", nil, "agent:bot-1", []string{"res-a", "res-x"}, false},
		{"too many steps", nil, "agent:bot-1", []string{"res-a", "res-b", "res-a", "res-b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := base
			if c.mut != nil {
				c.mut(&g)
			}
			reason := validatePlanAgainstGrant(g, c.agent, c.resources)
			if c.wantOK && reason != "" {
				t.Fatalf("want accept, got reason %q", reason)
			}
			if !c.wantOK && reason == "" {
				t.Fatal("want reject, got accept")
			}
		})
	}
}
