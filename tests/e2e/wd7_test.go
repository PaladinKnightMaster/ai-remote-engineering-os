//go:build e2e

// WD-7 — the v0.7.0 release gate (T-076, docs/releases/v0.7.0 §2): on the real stack a
// chartered Workstream fires governed Runs on a SCHEDULE with nobody present at launch.
// This is the third initiator (v0.5 operator → v0.6 agent → v0.7 schedule); the ticker
// enforces nothing itself — it fires through the SAME shared bounded-launch core as the
// gateway forward (SEC-30a), and the existing sequencer drives the Runs, so WD-7 reuses
// the v0.5/v0.6 chain unchanged and proves only the new schedule surface.
//
// The autonomous ticker fires continuously while a charter is active, so every WD-7 test
// (a) uses the DEDICATED d5 (ungated) / d6 (flagged) resources — never d3/d4 — so its
// churn cannot pollute the WD-5/WD-6 polls, and (b) t.Cleanup-retires its charter so the
// ticker stops before the next test (retire flips the charter terminal + revokes its
// grant; the claim predicate then skips it). The e2e stack lowers the schedule floor to
// 5s (AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS, compose) so recurrence proves in seconds.
//
// This file (T-076a) covers the happy path: LIVE recurrence (ungated) + the flagged
// park→approve→continue with the workstream actor context. The negatives (N1 retire-kills
// / N2 pause-resume / N3 budget-window recovery / self-retire) are wd7_negatives_test.go
// (T-076b). WD-5 + WD-6 stay green in the suite as the behavior-preserving regression
// guard (they own no charters; the ticker is isolated by d5/d6 + retire-hygiene).
package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestWD7HappyPathRecurrence — the schedule fires unattended, repeatedly. An ungated
// (d5) template at the 5s floor: assert ≥2 DISTINCT Runs launch and complete with NO
// human present (the autonomy payoff + the LIVE recurrence proof), each attributed to the
// Workstream actor under the charter's grant; the chain carries the charter's lifecycle.
func TestWD7HappyPathRecurrence(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// Charter an ungated recurring objective. A generous budget so recurrence is not
	// budget-limited before we observe ≥2 fires.
	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd7 recurring health",
		resources:       []string{workstreamResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD7-TICK"}},
		intervalSeconds: 5,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	// chartering chained workstream.chartered (actor = the operator, the ONE consent act)
	// + the grant's own grant.issued, both from the same transaction (WS-2 / AUD-18).
	chartered := pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.chartered", 30*time.Second)
	if chartered.Actor != "operator:admin" || chartered.Edge != "human-agent" {
		t.Fatalf("workstream.chartered = {actor:%q edge:%q}, want {operator:admin human-agent}", chartered.Actor, chartered.Edge)
	}
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+ws.GrantID, "grant.issued", 30*time.Second)

	// The load-bearing proof: with NOBODY present, ≥2 distinct Runs fire and complete.
	completed := waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 2, 90*time.Second)
	seen := map[string]bool{}
	for _, r := range completed {
		if r.Actor != "workstream:"+ws.ID {
			t.Fatalf("run %s actor = %q, want workstream:%s (the schedule initiator)", r.ID, r.Actor, ws.ID)
		}
		if r.GrantID != ws.GrantID {
			t.Fatalf("run %s grant = %q, want the charter's grant %s", r.ID, r.GrantID, ws.GrantID)
		}
		seen[r.ID] = true
	}
	if len(seen) < 2 {
		t.Fatalf("recurrence: %d distinct completed runs, want ≥2", len(seen))
	}

	// Each fired Run chained run.launched with actor = the Workstream (delegated
	// provenance, AUD-17). Prove it for one — the hash integrity over the whole chain is
	// the suite's trailing `audit-service verify`.
	var oneRun string
	for id := range seen {
		oneRun = id
		break
	}
	launched := pollTargetOutcome(t, auditURL, auditTok, "run:"+oneRun, "run.launched", 30*time.Second)
	if launched.Actor != "workstream:"+ws.ID {
		t.Fatalf("run.launched actor = %q, want workstream:%s", launched.Actor, ws.ID)
	}
	pollTargetOutcome(t, auditURL, auditTok, "run:"+oneRun, "run.completed", 30*time.Second)

	t.Logf("PASS WD-7 recurrence — charter %s fired %d distinct governed Runs unattended (actor=workstream); provenance chained", ws.ID, len(seen))
}

// TestWD7FlaggedTickParksAndApproves — a flagged tick parks for a LIVE human, carrying the
// workstream context, and continues to completion on approval (GOV-5/6 + FE-21). Uses a
// moderate 10s interval so only one tick is in flight during the capture→approve→retire
// window; operates on the CAPTURED invocation/run ids so a later tick cannot confuse it.
func TestWD7FlaggedTickParksAndApproves(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// A 2-step template: ungated d5 then flagged d6. The ungated step runs unattended;
	// the flagged step parks. 10s interval keeps a single tick in flight per window.
	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd7 flagged tick",
		resources:       []string{workstreamResourceID, workstreamFlaggedResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD7-F1"}, {workstreamFlaggedResourceID, "echo WD7-F2"}},
		intervalSeconds: 10,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	// The flagged step parks (~10s after charter). It carries the delegated context the
	// approving human needs (GOV-6): actor = the Workstream (NOT an interactive agent —
	// FE-21), the delegating principal, the grant, and the Run it belongs to.
	inv := pollPendingInvocation(t, client, apiURL, session, csrf, workstreamFlaggedResourceID, 45*time.Second)
	if inv.Agent != "workstream:"+ws.ID {
		t.Fatalf("parked tick actor = %q, want workstream:%s (FE-21 scheduled-by)", inv.Agent, ws.ID)
	}
	if inv.Principal != "operator:admin" {
		t.Fatalf("parked tick principal = %q, want operator:admin (FE-18/GOV-6)", inv.Principal)
	}
	if inv.GrantID != ws.GrantID {
		t.Fatalf("parked tick grant = %q, want the charter's grant %s (GOV-6)", inv.GrantID, ws.GrantID)
	}
	if inv.Command != "echo WD7-F2" {
		t.Fatalf("parked tick command = %q, want the verbatim template command", inv.Command)
	}
	// The park itself is the GOV-5 proof: policy-subject = the Workstream, so ADR-0054
	// same-operator self-approve cannot resolve it under the absent principal — a live
	// human must. Capture the run id BEFORE approving (a later tick must not confuse us).
	runID := inv.RunID

	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "approve")
	view := waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "completed", 60*time.Second)
	if view.Actor != "workstream:"+ws.ID || view.GrantID != ws.GrantID {
		t.Fatalf("completed tick provenance = {actor:%q grant:%q}, want {workstream:%s %s}", view.Actor, view.GrantID, ws.ID, ws.GrantID)
	}

	// The flagged tick's step bracket edge is agent-tool (ADR-0062 §4, derived from the
	// principal being set — a non-human actor under delegated authority, ADR-0063 §6); the
	// live human's approval is anchored to the parked invocation.
	var flaggedInv string
	for _, s := range view.Steps {
		if s.Status == "completed" && s.InvocationID != "" {
			flaggedInv = s.InvocationID // step 1 (the flagged one) completed after approval
		}
	}
	granted := pollEvidenceOutcome(t, auditURL, auditTok, "session:"+inv.ID, "approval.granted", 30*time.Second)
	if granted.Actor != "operator:admin" {
		t.Fatalf("approval.granted actor = %q, want operator:admin (a live human approved)", granted.Actor)
	}
	_ = flaggedInv

	t.Logf("PASS WD-7 flagged tick — charter %s fired, flagged step parked (workstream+principal+grant+run context), live approve → CONTINUE → completed", ws.ID)
}
