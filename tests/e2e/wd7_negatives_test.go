//go:build e2e

// WD-7 negatives (T-076b, docs/releases/v0.7.0 §2) — the containment legs of the
// chartered-Workstream gate, live: N1 retire kills an in-flight tick + stops the
// schedule, N2 pause/resume gates the schedule, N3 the windowed step budget refuses and
// then recovers, and a dead grant self-retires the charter. All use the dedicated d5/d6
// resources and t.Cleanup-retire (the WD-7 isolation posture, wd7_test.go).
package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestWD7RetireKillsInFlight — N1 (the kill switch): retiring a charter with an in-flight
// (parked) tick CASCADES — the Run is canceled and no further tick ever fires. The WD-6
// N2 revoke-cascade re-aimed at the charter verb (retire = charter flip + grant revoke +
// the DUR-7 cascade, one path).
func TestWD7RetireKillsInFlight(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// A 3-step template (ungated → flagged → ungated) so a tick parks on the flagged
	// middle step AND there is a post-park step 2 that must never dispatch after retire
	// (the "no egress after the kill" proof, the WD-6 N2 shape). 10s interval keeps a
	// single tick in flight.
	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd7 retire kill",
		resources: []string{workstreamResourceID, workstreamFlaggedResourceID},
		steps: []charterStep{
			{workstreamResourceID, "echo WD7-N1a"},
			{workstreamFlaggedResourceID, "echo WD7-N1b"},
			{workstreamResourceID, "echo WD7-N1c"},
		},
		intervalSeconds: 10,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	inv := pollPendingInvocation(t, client, apiURL, session, csrf, workstreamFlaggedResourceID, 45*time.Second)
	runID := inv.RunID

	// Retire: the charter flips terminal + its grant is revoked + the cascade cancels the
	// in-flight Run (run.canceled). The parked step is released (deny) so the held
	// /v1/steps call returns; the Run STAYS canceled (the setRunTerminal guard), and the
	// post-park step is NEVER dispatched (no egress after retire).
	if code := workstreamVerb(t, client, apiURL, session, csrf, ws.ID, "retire"); code != http.StatusOK {
		t.Fatalf("retire: status %d, want 200", code)
	}
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "canceled", 30*time.Second)
	resolveInvocation(t, client, apiURL, inv.ID, session, csrf, "deny")

	// The chain carries the retire (plain workstream.retired, actor = operator — the
	// operator verb, distinct from the machine self-retire :grant-dead qualifier) + the
	// grant.revoked + the run.canceled (actor = the workstream, delegated attribution).
	retired := pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.retired", 30*time.Second)
	if retired.Actor != "operator:admin" {
		t.Fatalf("workstream.retired actor = %q, want operator:admin (the operator verb)", retired.Actor)
	}
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+ws.GrantID, "grant.revoked", 30*time.Second)
	pollTargetOutcome(t, auditURL, auditTok, "run:"+runID, "run.canceled", 30*time.Second)

	// The load-bearing N1 assertions: the Run stayed canceled through the release race,
	// the post-park step never dispatched, and — the kill-switch proof — NO NEW Run fires
	// after retire (the charter is terminal; the claim predicate skips it). Count runs
	// now, wait past two intervals, count again: unchanged.
	view := getRun(t, client, apiURL, session, csrf, runID)
	if view.Status != "canceled" {
		t.Fatalf("run status after release = %q, want canceled", view.Status)
	}
	if view.Steps[2].Status != "pending" || view.Steps[2].InvocationID != "" {
		t.Fatalf("post-retire step 2 = %q/%q, want pending with NO invocation id", view.Steps[2].Status, view.Steps[2].InvocationID)
	}
	before := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID))
	time.Sleep(25 * time.Second) // > 2 intervals
	if after := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID)); after != before {
		t.Fatalf("retired charter still firing: run count %d → %d (want no new fires)", before, after)
	}
	if got := getWorkstream(t, client, apiURL, session, csrf, ws.ID); got.Status != "retired" {
		t.Fatalf("charter status = %q, want retired", got.Status)
	}
	t.Logf("PASS WD-7 N1 — retire cascaded to the in-flight tick (run.canceled), released step finalized, no later step dispatched, schedule STOPPED")
}

// TestWD7PauseResume — N2: pausing gates the schedule (no tick fires while paused);
// resuming re-arms next_fire_at = now + interval and the schedule resumes. Ungated (d5)
// so fires complete unattended and the proof is a clean run-count delta.
func TestWD7PauseResume(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd7 pause resume",
		resources:       []string{workstreamResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD7-N2"}},
		intervalSeconds: 5,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	// Let it fire at least once, then PAUSE.
	waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 1, 45*time.Second)
	if code := workstreamVerb(t, client, apiURL, session, csrf, ws.ID, "pause"); code != http.StatusOK {
		t.Fatalf("pause: status %d, want 200", code)
	}
	pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.paused", 30*time.Second)

	// Paused: across ≥2 intervals, the run count does NOT grow (the claim predicate skips
	// a non-active charter — proven at the store level in T-073, here end-to-end).
	paused := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID))
	time.Sleep(14 * time.Second)
	if grew := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID)); grew != paused {
		t.Fatalf("paused charter still firing: run count %d → %d (want no new fires while paused)", paused, grew)
	}

	// RESUME → the schedule re-arms and fires again (count grows past the paused level).
	if code := workstreamVerb(t, client, apiURL, session, csrf, ws.ID, "resume"); code != http.StatusOK {
		t.Fatalf("resume: status %d, want 200", code)
	}
	pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.resumed", 30*time.Second)
	waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", paused+1, 45*time.Second)
	t.Logf("PASS WD-7 N2 — paused charter fired nothing across 2+ intervals; resume re-armed the schedule and it fired again")
}

// TestWD7BudgetWindowRecovers — N3: the WINDOWED step budget refuses a tick that would
// exceed it, without wedging the charter, and RECOVERS in a later window. window =
// 2×interval, budget = 1 step: tick1 fires+consumes, tick2 is refused (run.launch.denied
// audited, charter stays active), and once tick1's Run rolls out of the window a later
// tick fires again. Asserts the robust form (a denial occurs AND recovery eventually
// happens) — not a timing-precise recovery tick (the strict-window boundary lands at
// tick3 or tick4 depending on sub-second created_at).
func TestWD7BudgetWindowRecovers(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd7 budget window",
		resources:       []string{workstreamResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD7-N3"}},
		intervalSeconds: 5,
		stepBudget:      1,  // one step per window
		budgetWindowSec: 10, // 2× interval
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	// tick1 fires and completes (consumes the 1-step window budget).
	waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 1, 45*time.Second)

	// A subsequent in-window tick is REFUSED at plan-vs-grant — the budget is exhausted —
	// and the denial is audited (run.launch.denied on the grant). The charter stays active
	// (never wedged); the refusal proves the windowed budget bounds the autonomous fires.
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+ws.GrantID, "run.launch.denied", 30*time.Second)
	if got := getWorkstream(t, client, apiURL, session, csrf, ws.ID); got.Status != "active" {
		t.Fatalf("charter status after a budget refusal = %q, want active (never wedged)", got.Status)
	}

	// RECOVERY: once tick1's Run leaves the rolling window, a later tick fires again —
	// a SECOND completed Run for the grant. Generous deadline (the boundary is ~2 ticks).
	waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 2, 60*time.Second)
	t.Logf("PASS WD-7 N3 — windowed budget refused an over-budget tick (run.launch.denied, charter stayed active) and recovered in a later window")
}

// TestWD7SelfRetireOnDeadGrant — the F1 self-retire: if a charter's grant dies (here a
// direct operator revoke via /v1/grants) while the charter is still active, the ticker
// RETIRES the charter on its next tick (workstream.retired:grant-dead, actor = the
// workstream — the machine transition) instead of spinning run.launch.denied forever.
// Ungated so no parked step complicates the revoke.
func TestWD7SelfRetireOnDeadGrant(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd7 self retire",
		resources:       []string{workstreamResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD7-SR"}},
		intervalSeconds: 5,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	// Let it fire once (prove it was live), then directly revoke its grant out from under
	// it (the operator owns the charter's grant — it is a normal /v1/grants row).
	waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 1, 45*time.Second)
	revokeGrantHTTP(t, client, apiURL, session, csrf, ws.GrantID)

	// The next tick's launch hits a revoked grant → RefusalGrantDead → the ticker
	// self-retires the charter (the machine transition, actor = the workstream).
	selfRetired := pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.retired:grant-dead", 45*time.Second)
	if selfRetired.Actor != "workstream:"+ws.ID {
		t.Fatalf("self-retire actor = %q, want workstream:%s (the machine transition)", selfRetired.Actor, ws.ID)
	}
	if got := getWorkstream(t, client, apiURL, session, csrf, ws.ID); got.Status != "retired" {
		t.Fatalf("charter status = %q, want retired (self-retired on the dead grant)", got.Status)
	}

	// And the schedule STOPPED — no new fires after the self-retire.
	before := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID))
	time.Sleep(14 * time.Second)
	if after := len(runsForGrant(t, client, apiURL, session, csrf, ws.GrantID)); after != before {
		t.Fatalf("self-retired charter still firing: run count %d → %d", before, after)
	}
	t.Logf("PASS WD-7 self-retire — a directly-revoked charter grant self-retired the charter (workstream.retired:grant-dead) and stopped the schedule")
}
