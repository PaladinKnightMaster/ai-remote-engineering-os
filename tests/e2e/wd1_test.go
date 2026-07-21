//go:build wd1

// TestWedgeDemoInstalledStack — the WD-2 wedge demo against the INSTALLED stack
// through Caddy HTTPS (T-021d-b walkthrough, extended to the governed-egress
// wedge by T-033b). The seeded resource is requires_approval-flagged, so the
// walkthrough proves the approval primitive on the production wire: login →
// resource picker → create (parked pending_approval) → operator APPROVE via
// POST /v1/sessions/{id}/resolve → broker WS over wss → run a command + read
// its output → end → DENY a second session (blocked + chained) → logout.
//
// The audit chain (integrity + the expected events, incl. policy.decision /
// approval.* / credential.leased) is asserted host-internally by run-wd1.sh,
// because audit-service binds to localhost in the install and is not
// Caddy-routed. The operator audit-timeline VIEW is asserted here (T-024).
//
// Shared harness helpers live in harness_test.go (build tag e2e || wd1).
package e2e

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWedgeDemoInstalledStack(t *testing.T) {
	// Trust Caddy's local CA (`tls internal`) via a per-test client (newWD1TLSClient,
	// harness_test.go) instead of mutating the shared http.DefaultTransport.
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	wsBase := strings.Replace(base, "https://", "wss://", 1)
	client := newWD1TLSClient(t, 20*time.Second)

	// 1. login over Caddy HTTPS (retry while the stack settles). Secure cookies
	//    work normally here (real HTTPS) — no plaintext cookie workaround.
	var sess, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, base)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sess, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded over Caddy HTTPS (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("login ok over Caddy HTTPS")

	// 2. resource picker: GET /v1/resources shows the seeded ssh resource (WD-1 step 3).
	assertResourceVisible(t, client, base, sess, csrf)

	// 3. create against the flagged resource → parked pending_approval (the
	//    ADR-0054 gate on the production wire) → operator approves → 'pending',
	//    then open the broker WS over wss through Caddy.
	sessionID := createSessionFor(t, client, base, sess, csrf, resourceID, "pending_approval")
	t.Log("session parked pending_approval (requires_approval resource)")
	if got := resolveSession(t, client, base, sessionID, sess, csrf, "approve"); got != "pending" {
		t.Fatalf("approve: session status = %q, want pending", got)
	}
	t.Log("approved — session released to the broker")
	wsURL := wsBase + "/v1/sessions/" + sessionID + "/channel"
	ws := dialWS(t, wsURL, http.Header{"Cookie": []string{sessionCookieName + "=" + sess}})
	defer ws.CloseNow()
	assertWSRejectsWithoutAuth(t, wsURL) // ADR-0050 §2 guard, now over wss

	// 4. run a command + read its output back over the live PTY (SSH round-trip).
	marker := "WD1OK-" + hex.EncodeToString(randBytes(t, 6))
	if err := ws.Write(context.Background(), websocket.MessageBinary, []byte("echo "+marker+"\r")); err != nil {
		t.Fatalf("ws write: %v", err)
	}
	if !readUntilMarker(ws, marker, 20*time.Second) {
		t.Fatal("command output never returned over the PTY — the SSH round-trip failed")
	}
	t.Log("command executed; marker observed in PTY output")

	// 5. end the session + close the WS.
	endSession(t, client, base, sessionID, sess, csrf)
	_ = ws.Close(websocket.StatusNormalClosure, "wd1 done")

	// 5b. deny leg (WD-2.3's other half): a second parked session is DENIED —
	//     api answers closed (approval_denied); approval.denied:operator is
	//     asserted host-internally by run-wd1.sh. The blocked-attach runtime
	//     proof ran live in the compose e2e (T-033a).
	deniedID := createSessionFor(t, client, base, sess, csrf, resourceID, "pending_approval")
	if got := resolveSession(t, client, base, deniedID, sess, csrf, "deny"); got != "closed" {
		t.Fatalf("deny: session status = %q, want closed", got)
	}
	t.Logf("denied session %s closed (approval_denied)", deniedID)

	// 6. operator visits the audit timeline through Caddy -> api read-proxy ->
	//    audit-service (T-024). Proves the in-cockpit read path on real wires:
	//    the operator reads their own chained events. Closes the "operator
	//    visits the audit timeline" half of WD-1 step 8 (chain *integrity* is
	//    asserted host-internally by run-wd1.sh).
	assertAuditTimelineReadable(t, client, base, sess, csrf)

	// 7. logout (emits auth.logout).
	logout(t, client, base, sess, csrf)

	t.Log("PASS — WD-2 operator walkthrough complete through Caddy HTTPS (login → resource → parked create → approve → session → command → end → deny leg → audit timeline → logout). Chain integrity is asserted by run-wd1.sh.")
}

// TestWD5InstalledStack — OI-12: WD-5 on the INSTALLED stack through Caddy HTTPS. It
// proves the Run plane works on the real install (migrations applied, GATEWAY_URL wired,
// the in-process sequencer driving the installed gateway): an operator launches a
// multi-step governed Run, each step PARKS for approval and is approved on /invocations,
// the sequencer CONTINUES to the next step, and the Run completes. The seeded resource is
// requires_approval-flagged (run-wd1.sh), so a 2-step plan on it parks twice → the
// multi-step governed drive end-to-end. run.launched / run.completed landing in the chain
// (via api's in-process outbox relay) are asserted host-internally by run-wd1.sh. The full
// negative matrix (denied-abort/cancel/crash-resume) is compose-proven by T-060; HTTP-only
// here (the external driver has no DB, only Caddy HTTPS).
func TestWD5InstalledStack(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 30*time.Second)

	// login over Caddy HTTPS (retry while the stack settles).
	var sess, csrf string
	deadline := time.Now().Add(120 * time.Second)
	for {
		s, c, status, err := doLogin(client, base)
		if err == nil && status == http.StatusOK && s != "" && c != "" {
			sess, csrf = s, c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("login never succeeded over Caddy HTTPS (last status=%d err=%v)", status, err)
		}
		time.Sleep(2 * time.Second)
	}

	// Launch a 2-step governed Run on the flagged resource (the only installed resource).
	runID, st := launchRun(t, client, base, sess, csrf, []stepSeed{
		{resourceID, "echo WD5-INSTALLED-1"},
		{resourceID, "echo WD5-INSTALLED-2"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch run: status %d, want 201", st)
	}
	t.Logf("launched 2-step run %s on the installed stack", runID)

	// Each step parks for approval; approve in turn — the sequencer continues past each.
	for i := 0; i < 2; i++ {
		inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 90*time.Second)
		resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
		t.Logf("approved parked step %d (invocation %s)", i, inv.ID)
	}

	// Poll the operator GET route until the Run completes (HTTP-only — no DB here).
	runDeadline := time.Now().Add(180 * time.Second)
	for {
		got := getRun(t, client, base, sess, csrf, runID)
		if got.Status == "completed" {
			if len(got.Steps) != 2 {
				t.Fatalf("completed run has %d steps, want 2", len(got.Steps))
			}
			break
		}
		if got.Status == "failed" || got.Status == "aborted" || got.Status == "canceled" {
			t.Fatalf("run %s ended %q, want completed", runID, got.Status)
		}
		if time.Now().After(runDeadline) {
			t.Fatalf("run %s never completed (last status %q)", runID, got.Status)
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("PASS — WD-5 on the installed stack: a 2-step governed Run drove through the INSTALLED gateway; each step parked→approved→continued→completed. run.launched/run.completed in the chain are asserted by run-wd1.sh.")
}

// TestWD6InstalledStack — OI-13 (T-069): WD-6 on the INSTALLED stack through Caddy
// HTTPS. Proves the delegated-launch plane works on the production wiring: the api
// migrations (delegation_grants + runs actor/grant_id/launch_key) applied by
// install.sh's migrate loop, the NEW gateway API_URL wire, and api verifying the
// gateway's forward with the aireos.env AIREOS_GATEWAY_PUBLIC_KEY (the OI-11 key,
// reused — SEC-27). Runs AFTER TestWD5InstalledStack (source order): the install has
// ONE flagged resource, so parked invocations must stay one-at-a-time per test.
//
// Legs (all HTTP via Caddy; the delegated e2e capstone wd6_test.go is the full gate —
// this re-proves it on the install): (1) happy — grant → agent run.launch → each step
// parks WITH the delegated context (agent + principal + grant + run, GOV-6) → approve
// → completed with actor/grantId provenance; (2) N2 — revoke mid-park cascades to
// cancel, the released step finalizes, no later step dispatches; (3) N3 — a direct
// agent→api forward through Caddy /v1 is rejected (the key-holding api is not
// agent-reachable on the deployed topology). Chain presence (grant.issued/revoked,
// run.canceled, agent-actor run.launched) is asserted host-internally by run-wd1.sh.
func TestWD6InstalledStack(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 60*time.Second)
	const agentSubject = "agent:wd6"

	sess, csrf, status, err := doLogin(client, base)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	// --- Leg 1: the delegated happy path on the install -----------------------
	grantID := issueGrant(t, client, base, sess, csrf, agentSubject, []string{resourceID}, 50)
	tok := mintAgentTokenViaAPI(t, client, base, sess, csrf, "wd6", resourceID)
	runID := callRunLaunchOK(t, client, base, tok, grantID, []map[string]string{
		{"resourceId": resourceID, "command": "echo WD6-INSTALLED-1"},
		{"resourceId": resourceID, "command": "echo WD6-INSTALLED-2"},
	})
	t.Logf("agent launched delegated run %s via Caddy /mcp on the installed stack", runID)

	// Both steps park (the installed resource is flagged); the FIRST parked row must
	// carry the full GOV-6 delegated context for the approving human.
	for i := 0; i < 2; i++ {
		inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 120*time.Second)
		if inv.Agent != agentSubject || inv.Principal != "operator:admin" {
			t.Fatalf("parked delegated step %d = {agent:%q principal:%q}, want {%s operator:admin} (GOV-6)", i, inv.Agent, inv.Principal, agentSubject)
		}
		if inv.RunID != runID || inv.GrantID != grantID {
			t.Fatalf("parked delegated step %d = {run:%q grant:%q}, want {%s %s} (GOV-6)", i, inv.RunID, inv.GrantID, runID, grantID)
		}
		resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
		t.Logf("approved delegated parked step %d with full context (agent · principal · grant · run)", i)
	}
	view := waitRunStatusHTTP(t, client, base, sess, csrf, runID, "completed", 240*time.Second)
	if view.Actor != agentSubject || view.GrantID != grantID {
		t.Fatalf("completed run provenance = {actor:%q grant:%q}, want {%s %s}", view.Actor, view.GrantID, agentSubject, grantID)
	}
	t.Log("PASS installed WD-6 happy path — delegated 2-step run completed with actor/grant provenance")

	// --- Leg 2: N2 — revoke cascades to the in-flight parked run --------------
	grant2 := issueGrant(t, client, base, sess, csrf, agentSubject, []string{resourceID}, 50)
	run2 := callRunLaunchOK(t, client, base, tok, grant2, []map[string]string{
		{"resourceId": resourceID, "command": "echo WD6-INSTALLED-R1"},
		{"resourceId": resourceID, "command": "echo WD6-INSTALLED-R2"},
	})
	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 120*time.Second)
	revokeGrantHTTP(t, client, base, sess, csrf, grant2)
	waitRunStatusHTTP(t, client, base, sess, csrf, run2, "canceled", 30*time.Second)
	// Release the parked step BEFORE any settle wait (the TestRunEngineCancel ordering).
	resolveInvocation(t, client, base, inv.ID, sess, csrf, "deny")
	deadline := time.Now().Add(60 * time.Second)
	for {
		v := getRun(t, client, base, sess, csrf, run2)
		if len(v.Steps) == 2 && v.Steps[0].Status == "failed" {
			if v.Status != "canceled" {
				t.Fatalf("run after release = %q, want canceled (the FinishRun guard)", v.Status)
			}
			if v.Steps[1].Status != "pending" || v.Steps[1].InvocationID != "" {
				t.Fatalf("post-revoke step 1 = %q/%q, want pending with NO invocation id", v.Steps[1].Status, v.Steps[1].InvocationID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("released step never finalized on the install: %+v", v.Steps)
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("PASS installed WD-6 N2 — revoke canceled the in-flight run; released step finalized; no later dispatch")

	// --- Leg 3: N3 — the key-holding api is not agent-reachable on the install --
	body, _ := json.Marshal(map[string]any{
		"agentSubject": agentSubject, "grantId": grantID, "idempotencyKey": "wd6-n3",
		"steps": []map[string]string{{"resourceId": resourceID, "command": "echo X"}},
	})
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/internal/delegated-runs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok) // the agent's own token, not the gateway's
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("N3 direct call: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("direct agent→api forward on the install = %d, want 401/403 (SEC-27)", resp.StatusCode)
	}
	t.Logf("PASS installed WD-6 N3 — direct agent→api forward rejected (%d); chain presence asserted by run-wd1.sh", resp.StatusCode)
}

// TestWD7InstalledStack — OI-14 (T-077): the chartered-Workstream schedule fires on the
// REAL install via Caddy HTTPS. Proves the in-process ticker runs on the production
// wiring — migration 0011 (workstreams/workstream_steps + budget_window_seconds) applied
// by install.sh's migrate loop; the ticker started because the installed api already
// loads GATEWAY_URL from aireos.env (the OI-13 wire, reused — NO new env); the schedule
// floor is the 60s main.go default (the install ships the production default, criteria
// F5 posture — NO AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS written).
//
// Installed-scope = one fire + park/approve + retire (F5 split; LIVE recurrence stays
// compose-proven — wd7_test.go — because the 60s floor makes a second-fire leg too slow
// here). Runs AFTER the WD-5/WD-6 installed legs (source order): the install has ONE
// flagged resource, so a single tick parks one-at-a-time; retire stops the ticker after.
// Reuses workstream_driver.go (tagged e2e||wd1). Chain presence (workstream.chartered/
// retired + workstream-actor run.launched) is asserted host-internally by run-wd1.sh.
func TestWD7InstalledStack(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 90*time.Second)

	sess, csrf, status, err := doLogin(client, base)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	// Charter a single flagged-step objective at the 60s floor minimum (a lower interval
	// 422s — proving the production floor is live). The install's one resource is flagged,
	// so the tick parks; the charter auto-issues its own grant scoped to it (WS-2).
	ws := charterWorkstream(t, client, base, sess, csrf, charterOpts{
		objective:       "wd7 installed health",
		resources:       []string{resourceID},
		steps:           []charterStep{{resourceID, "echo WD7-INSTALLED"}},
		intervalSeconds: 60,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, base, sess, csrf, ws.ID) })
	t.Logf("chartered workstream %s (grant %s) via Caddy on the installed stack; awaiting the first schedule fire (~60s floor)", ws.ID, ws.GrantID)

	// The first tick fires at ~charter+60s and the flagged step parks, carrying the
	// workstream context the approving human needs (FE-21/GOV-6): actor = the Workstream
	// (a non-human scheduled actor, not an interactive agent), principal, grant, run.
	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 120*time.Second)
	if inv.Agent != "workstream:"+ws.ID || inv.Principal != "operator:admin" {
		t.Fatalf("parked tick = {agent:%q principal:%q}, want {workstream:%s operator:admin} (FE-21/GOV-6)", inv.Agent, inv.Principal, ws.ID)
	}
	if inv.RunID == "" || inv.GrantID != ws.GrantID {
		t.Fatalf("parked tick = {run:%q grant:%q}, want a run under the charter's grant %s", inv.RunID, inv.GrantID, ws.GrantID)
	}
	runID := inv.RunID

	// A live human approves → the tick's Run continues to completion with the workstream
	// provenance (AUD-17/18).
	resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
	view := waitRunStatusHTTP(t, client, base, sess, csrf, runID, "completed", 120*time.Second)
	if view.Actor != "workstream:"+ws.ID || view.GrantID != ws.GrantID {
		t.Fatalf("completed tick provenance = {actor:%q grant:%q}, want {workstream:%s %s}", view.Actor, view.GrantID, ws.ID, ws.GrantID)
	}

	// Retire (the operator kill switch): the charter flips terminal + its grant is revoked;
	// the schedule stops. The Run already completed, so there is nothing in flight to
	// cascade — a clean terminal retire.
	if code := workstreamVerb(t, client, base, sess, csrf, ws.ID, "retire"); code != http.StatusOK {
		t.Fatalf("retire: status %d, want 200", code)
	}
	if got := getWorkstream(t, client, base, sess, csrf, ws.ID); got.Status != "retired" {
		t.Fatalf("charter status after retire = %q, want retired", got.Status)
	}
	t.Logf("PASS installed WD-7 — charter %s fired a governed Run on the schedule (unattended launch), the flagged tick parked with workstream context, live approve → completed, retire stopped the schedule", ws.ID)
}

// TestWD8InstalledStack — OI-15 (T-084): the FULL WD-8 Persistent Scoped Memory gate on
// the INSTALLED stack through Caddy HTTPS (C4 — release-criteria §9). Everything memory
// ships inside services/api: migration 0012 rides install.sh's existing migrate loop and
// the expiry sweep rides the in-process reaper goroutine that starts unconditionally at
// api boot — NO new unit, Caddy route, key material, or required env. The installed
// stack asserts the DEFAULT posture (no AIREOS_MEMORY_SWEEP_SECONDS written by
// install.sh), so the expiry leg runs at the production ~2-minute reaper cadence — the
// v0.7 60s-schedule-floor precedent applied to the sweep.
//
// Legs (the full WD-8 scope, C4): (a) accrual with nobody present — a chartered
// Workstream's approved tick completes and accrues a content-free pointer with
// actor=workstream; (b) author → scoped read round-trip → redact, + N1 (a second redact
// is a silent 200 no-op, exactly ONE memory.redacted); (c) N2/SEC-31 — a VALID agent
// token is rejected on /v1/memories* (wrong plane, not a signature failure); (d) N3 —
// redact/pin/unpin on a well-formed unknown id 404; (e) expiry AND pinning in ONE
// bounded wait at the production sweep default — the unpinned row tombstones exactly
// once, the pinned row is spared at the same pass; (f) N4 lean — an operator-CANCELED
// Run accrues exactly one pointer with the :canceled trace (the failed-Run terminal leg
// stays compose-proven — wd8_negatives_test.go). Chain presence of
// memory.written/redacted/expired is ALSO asserted host-internally by run-wd1.sh
// (WANTS); per-id actor + exactly-once counts are asserted here through the operator
// audit read-proxy (the T-024 wire the cockpit timeline uses — the wd1 driver has no
// host-internal audit reach). Runs AFTER TestWD7InstalledStack (source order): the
// install has ONE flagged resource, so parked invocations stay one-at-a-time per test;
// time-derived keys keep a dirty stack re-runnable.
func TestWD8InstalledStack(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 90*time.Second)

	sess, csrf, status, err := doLogin(client, base)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}

	// --- Leg a: governed work accrues memory with nobody writing it ---------------
	// Charter a single flagged-step objective at the 60s production floor (the
	// TestWD7InstalledStack pattern, same seeded flagged resource).
	ws := charterWorkstream(t, client, base, sess, csrf, charterOpts{
		objective:       "wd8 installed accrual",
		resources:       []string{resourceID},
		steps:           []charterStep{{resourceID, "echo WD8-INSTALLED"}},
		intervalSeconds: 60,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, base, sess, csrf, ws.ID) })
	t.Logf("chartered workstream %s on the installed stack; awaiting the tick (~60s floor)", ws.ID)

	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 120*time.Second)
	if inv.Agent != "workstream:"+ws.ID || inv.RunID == "" {
		t.Fatalf("parked tick = {agent:%q run:%q}, want {workstream:%s <run>}", inv.Agent, inv.RunID, ws.ID)
	}
	runID := inv.RunID
	resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
	waitRunStatusHTTP(t, client, base, sess, csrf, runID, "completed", 120*time.Second)
	// Retire NOW (the compose WD-8 pattern): the later legs run on this same scope and
	// must not race further ticks — a second parked invocation at ~+120s would collide
	// with leg f's pending-invocation poll on the single installed resource.
	retireWorkstream(t, client, base, sess, csrf, ws.ID)

	accrued := waitForMemory(t, client, base, sess, csrf, "workstream", ws.ID, 30*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runID
	})
	if accrued.HasContent {
		t.Fatalf("accrued memory %s hasContent=true, want false (content-free pointer)", accrued.ID)
	}
	if want := "run:" + runID + ":completed"; accrued.SourceTrace != want {
		t.Fatalf("accrued memory sourceTrace = %q, want %q", accrued.SourceTrace, want)
	}
	written := pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+accrued.ID, "memory.written", 60*time.Second)
	if written.Actor != "workstream:"+ws.ID {
		t.Fatalf("memory.written actor = %q, want workstream:%s", written.Actor, ws.ID)
	}
	t.Logf("PASS installed WD-8 leg a — the completed tick accrued exactly the content-free pointer %s (actor=workstream, nobody wrote it)", accrued.ID)

	// --- Leg b: author → scoped read → redact (+ N1 idempotency) ------------------
	authored := createMemory(t, client, base, sess, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: "wd8-inst-" + wd1MemSuffix(), Content: "wd8 installed note",
	})
	roundTrip := waitForMemory(t, client, base, sess, csrf, "workstream", ws.ID, 15*time.Second, func(m memoryView) bool {
		return m.ID == authored.ID
	})
	if !roundTrip.HasContent || roundTrip.Content == nil || *roundTrip.Content != "wd8 installed note" {
		t.Fatalf("scoped read of authored memory = %+v, want the sealed content to round-trip", roundTrip)
	}
	redacted, code := redactMemory(t, client, base, sess, csrf, authored.ID)
	if code != http.StatusOK {
		t.Fatalf("redact: status %d, want 200", code)
	}
	if redacted.RedactedAt == nil || redacted.Content != nil {
		t.Fatalf("redacted memory = %+v, want redactedAt set + content erased", redacted)
	}
	// N1: the second redact is a silent no-op — still 200, tombstone unchanged.
	second, code2 := redactMemory(t, client, base, sess, csrf, authored.ID)
	if code2 != http.StatusOK {
		t.Fatalf("second redact: status %d, want 200 (idempotent)", code2)
	}
	if second.RedactedAt == nil || !second.RedactedAt.Equal(*redacted.RedactedAt) {
		t.Fatalf("redactedAt changed across the no-op redact: first=%v second=%v", redacted.RedactedAt, second.RedactedAt)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+authored.ID, "memory.redacted", 60*time.Second)
	if n := countProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+authored.ID, "memory.redacted"); n != 1 {
		t.Fatalf("memory.redacted count for %s = %d, want exactly 1 across two redacts (N1)", authored.ID, n)
	}
	t.Log("PASS installed WD-8 leg b — author round-tripped, redact tombstoned (content gone), second redact a silent no-op with ONE memory.redacted")

	// --- Leg c: N2/SEC-31 — the agent plane cannot touch memory -------------------
	agentTok := mintAgentTokenViaAPI(t, client, base, sess, csrf, "wd8", resourceID)
	getReq, _ := http.NewRequest(http.MethodGet, base+"/v1/memories?scopeType=workstream&scopeId="+ws.ID, nil)
	getReq.Header.Set("Authorization", "Bearer "+agentTok)
	getResp, err := client.Do(getReq)
	if err != nil {
		t.Fatalf("agent-token GET /v1/memories: %v", err)
	}
	_ = getResp.Body.Close()
	if getResp.StatusCode != http.StatusUnauthorized && getResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent-token GET /v1/memories: status %d, want 401/403 (SEC-31)", getResp.StatusCode)
	}
	postReq, _ := http.NewRequest(http.MethodPost, base+"/v1/memories",
		strings.NewReader(`{"scopeType":"operator","scopeId":"admin","content":"should never land"}`))
	postReq.Header.Set("Authorization", "Bearer "+agentTok)
	postReq.Header.Set("Content-Type", "application/json")
	postResp, err := client.Do(postReq)
	if err != nil {
		t.Fatalf("agent-token POST /v1/memories: %v", err)
	}
	_ = postResp.Body.Close()
	if postResp.StatusCode != http.StatusUnauthorized && postResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent-token POST /v1/memories: status %d, want 401/403 (SEC-31)", postResp.StatusCode)
	}
	t.Logf("PASS installed WD-8 leg c — a VALID agent token is rejected on the memory plane (GET %d / POST %d, SEC-31)", getResp.StatusCode, postResp.StatusCode)

	// --- Leg d: N3 — forgetting verbs on a well-formed unknown id 404 -------------
	const unknownID = "01900000-0000-7000-8000-0000000000ff" // well-formed, never seeded
	for _, verb := range []string{"redact", "pin", "unpin"} {
		if _, vcode := memoryVerb(t, client, base, sess, csrf, unknownID, verb); vcode != http.StatusNotFound {
			t.Fatalf("%s on an unknown memory id: status %d, want 404", verb, vcode)
		}
	}
	t.Log("PASS installed WD-8 leg d — redact/pin/unpin on an unknown id all answer 404")

	// --- Leg e: expiry + pinning at the PRODUCTION sweep default ------------------
	// No AIREOS_MEMORY_SWEEP_SECONDS on the install → the sweep ticks at the 2-minute
	// reaper cadence. ONE bounded wait: the unpinned row (expires_at ≈ now+5s)
	// tombstones within ≤150s (worst case one full tick after expiry), and at that
	// SAME pass the pinned row must be untouched.
	pinned := createMemory(t, client, base, sess, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: "wd8-inst-pin-" + wd1MemSuffix(), Content: "pin me",
	})
	pinMemory(t, client, base, sess, csrf, pinned.ID)
	expiresAt := time.Now().Add(5 * time.Second)
	expiring := createMemory(t, client, base, sess, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: "wd8-inst-exp-" + wd1MemSuffix(), Content: "expire me", ExpiresAt: &expiresAt,
	})
	sweepDeadline := time.Now().Add(150 * time.Second)
	var expired memoryView
	for {
		var pinnedNow memoryView
		sweptThisPass := false
		for _, m := range listMemories(t, client, base, sess, csrf, "workstream", ws.ID) {
			switch m.ID {
			case expiring.ID:
				if m.RedactedAt != nil {
					expired, sweptThisPass = m, true
				}
			case pinned.ID:
				pinnedNow = m
			}
		}
		if sweptThisPass {
			if pinnedNow.ID == "" || pinnedNow.RedactedAt != nil {
				t.Fatalf("pinned memory %s not spared at the expiry pass: %+v", pinned.ID, pinnedNow)
			}
			break
		}
		if time.Now().After(sweepDeadline) {
			t.Fatalf("unpinned memory %s never expired within 150s at the production sweep cadence (DUR-9)", expiring.ID)
		}
		time.Sleep(5 * time.Second)
	}
	if expired.Content != nil {
		t.Fatalf("expired memory still carries content: %+v", expired)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+expiring.ID, "memory.expired", 60*time.Second)
	if n := countProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+expiring.ID, "memory.expired"); n != 1 {
		t.Fatalf("memory.expired count for %s = %d, want exactly 1", expiring.ID, n)
	}
	if n := countProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+pinned.ID, "memory.expired"); n != 0 {
		t.Fatalf("pinned memory %s has %d memory.expired event(s) — pin did not exempt it from the sweep", pinned.ID, n)
	}
	t.Log("PASS installed WD-8 leg e — the production-cadence sweep expired the unpinned row exactly once and spared the pinned row at the same pass")

	// --- Leg f: N4 lean — an operator-CANCELED Run accrues exactly one pointer ----
	// Cancel mid-flight while the flagged first step is parked (the setRunTerminal
	// path, NOT FinishRun). The failed-Run terminal leg stays compose-proven
	// (wd8_negatives_test.go TestWD8TerminalCompleteness).
	runF, st := launchRun(t, client, base, sess, csrf, []stepSeed{
		{resourceID, "echo WD8-INSTALLED-CANCEL-1"},
		{resourceID, "echo WD8-INSTALLED-CANCEL-2"},
	})
	if st != http.StatusCreated {
		t.Fatalf("launch cancel-leg run: status %d, want 201", st)
	}
	invF := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 90*time.Second)
	if invF.RunID != runF {
		t.Fatalf("parked step belongs to run %q, want %q", invF.RunID, runF)
	}
	if ccode := cancelRun(t, client, base, sess, csrf, runF); ccode != http.StatusOK {
		t.Fatalf("cancel: status %d, want 200", ccode)
	}
	// Release the parked step so the held call returns; the Run stays canceled.
	resolveInvocation(t, client, base, invF.ID, sess, csrf, "deny")
	waitRunStatusHTTP(t, client, base, sess, csrf, runF, "canceled", 60*time.Second)
	memF := waitForMemory(t, client, base, sess, csrf, "run", runF, 30*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runF
	})
	if want := "run:" + runF + ":canceled"; memF.SourceTrace != want {
		t.Fatalf("canceled run accrual sourceTrace = %q, want %q", memF.SourceTrace, want)
	}
	var accrualCount int
	for _, m := range listMemories(t, client, base, sess, csrf, "run", runF) {
		if m.Ref == "run:"+runF {
			accrualCount++
		}
	}
	if accrualCount != 1 {
		t.Fatalf("canceled run %s: %d accrued memory rows, want exactly 1 (N4)", runF, accrualCount)
	}
	t.Logf("PASS installed WD-8 — accrue(actor=workstream) → author/read → redact(+N1) → agent-plane 401/403(N2) → unknown-id 404(N3) → production-cadence expire+pin(one wait) → canceled-Run accrual ×1(N4 lean). memory.* chain presence is asserted by run-wd1.sh")
}

// TestWD9InstalledStack — OI-16 (T-093): the FULL WD-9 Adaptive-Workstreams gate on the
// INSTALLED stack through Caddy HTTPS, at the REAL production posture — the 60s schedule
// floor (defaultWorkstreamMinInterval, no env override), no InsecureSkipVerify anywhere.
// Everything WD-9 ships free on the install: migration 0013 rides install.sh's existing
// migrate loop, and the three v0.9 control-plane MCP tools (workstream.poll / plan.submit
// / memory.read) ride the existing gateway binary + the existing Caddy /mcp route — NO
// new unit, key, or env (C5, docs/releases/v0.9.0 §9).
//
// Three adaptive charters, one composer each, so a 60s-floor tick wait for one overlaps
// real wall-clock with work on another:
//
//	A — the loop-closure happy path (charter -> tick1 -> read#1 -> submit -> preview ->
//	    approve -> the flagged resource's SECOND, invocation-level, park+approve ->
//	    completed -> exactly one accrual -> tick2's FRESH request -> read#2 returns the
//	    just-accrued pointer, the loop closes) + the deny cycle on tick2 (submit -> deny
//	    -> canceled, run.denied exactly once, one accrual) + N2 (author -> invisible ->
//	    share -> visible -> unshare -> invisible, cheap, A's scope).
//	B — charter'd right after A so its own tick1 is already open while A's happy path
//	    plays out; N1 cross-grant containment (A never sees B's still-open charter or
//	    request) captured BEFORE B's request is consumed, then N3 revocation: drive B to
//	    'proposed', wait for a FRESH open request (the cascade's second victim), revoke —
//	    the proposed run cascade-cancels with ZERO run.denied, the open request closes,
//	    B's poll/submit/read all go dead, approve is refused.
//	D — a self-contained N4 cycle (C5's expiry-never-wedges leg, budgeted separately at
//	    ~2.5-3 min): charter, grab whatever request is open, deliberately ignore it,
//	    watch it expire (plan.expired chained) at its own ~60s TTL, then watch the next
//	    tick open a fresh one — the charter never wedges on a composer that never
//	    answers.
//
// Only the resource the install seeds (resourceID) exists here, and it is
// requires_approval-flagged — unlike the compose WD-9 suite's ungated
// workstreamResourceID — so leg A's approved plan parks a SECOND time at the invocation
// level (the WD-5/7/8 installed precedent) before the sequencer can complete it.
//
// Both A's and B's initial requests are SUBMITTED promptly (before either's ~120s TTL:
// claimed-fire-time + interval, R1) — the N1 containment read happens first (while B's
// request is still genuinely open), then both plans submit back to back, so nothing here
// races an expiry that isn't the deliberate N4 leg.
//
// Chain assertions ride the operator audit read-proxy (listAllEventsViaProxy,
// pollProxyTargetOutcome/countProxyTargetOutcome — the wd1 driver has no host-internal
// audit reach); pollProxyActorOutcomeCount (below) is this test's actor-scoped analogue
// for the composer's memory.read count. Per-leg exact counts are asserted here; chain
// INTEGRITY + presence of the new outcome vocabulary (plan.requested/submitted/expired,
// memory.read/shared/unshared, run.denied) is asserted host-internally by run-wd1.sh
// (WANTS) — NOT a global exact chain length, since tick timing makes that
// non-deterministic across re-runs (the T-093 report records the observed count for one
// run instead).
//
// Token TTL sizing (T-093): auth.AgentTTL is 5 minutes; a full pass at the 60s
// production floor spans several ticks and can outlive one mint. wd9TokenSource
// (wd9_driver.go) re-mints ahead of any call that might land past it — the TTL itself is
// untouched (no widening).
func TestWD9InstalledStack(t *testing.T) {
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := newWD1TLSClient(t, 90*time.Second)

	sess, csrf, status, err := doLogin(client, base)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	suffix := wd1MemSuffix()

	// --- Charter A + Charter B, staggered: B's own tick is already cooking while A's -
	// happy path plays out below.
	agentA := "wd9-inst-a-" + suffix
	tsA := newWD9TokenSource(t, client, base, sess, csrf, agentA, resourceID)
	composerA := "agent:" + agentA
	wsA := charterWorkstream(t, client, base, sess, csrf, charterOpts{
		objective: "wd9 installed a " + suffix, resources: []string{resourceID},
		intervalSeconds: 60, stepBudget: 100, kind: "adaptive", composer: composerA,
	})
	t.Cleanup(func() { retireWorkstream(t, client, base, sess, csrf, wsA.ID) })

	agentB := "wd9-inst-b-" + suffix
	tsB := newWD9TokenSource(t, client, base, sess, csrf, agentB, resourceID)
	composerB := "agent:" + agentB
	wsB := charterWorkstream(t, client, base, sess, csrf, charterOpts{
		objective: "wd9 installed b " + suffix, resources: []string{resourceID},
		intervalSeconds: 60, stepBudget: 100, kind: "adaptive", composer: composerB,
	})
	t.Cleanup(func() { retireWorkstream(t, client, base, sess, csrf, wsB.ID) })
	t.Logf("chartered A (%s, composer %s) and B (%s, composer %s) at the 60s production floor", wsA.ID, composerA, wsB.ID, composerB)

	// --- tick1 for both, read#1 for A, N1 containment (B's request still genuinely open) -
	req1 := waitForPlanRequest(t, client, base, tsA.token(), wsA.ID, 150*time.Second)
	if req1.WorkstreamID != wsA.ID {
		t.Fatalf("A's plan request workstreamId = %q, want %s", req1.WorkstreamID, wsA.ID)
	}
	reqB := waitForPlanRequest(t, client, base, tsB.token(), wsB.ID, 150*time.Second)
	if reqB.WorkstreamID != wsB.ID {
		t.Fatalf("B's plan request workstreamId = %q, want %s", reqB.WorkstreamID, wsB.ID)
	}

	readMemoryMCP(t, client, base, tsA.token(), wsA.ID) // baseline read#1, chained below
	read1 := pollProxyActorOutcomeCount(t, client, base, sess, csrf, composerA, "memory.read", 1, 30*time.Second)
	if len(read1) != 1 || read1[0].Edge != "agent-memory" {
		t.Fatalf("composer A memory.read #1 = %+v, want exactly one with edge=agent-memory", read1)
	}
	t.Logf("PASS leg A1 — tick1 opened %s; baseline memory.read #1 chained (edge=agent-memory)", req1.RequestID)

	for _, r := range pollWorkstreamMCP(t, client, base, tsA.token(), "") {
		if r.WorkstreamID == wsB.ID {
			t.Fatalf("A's unfiltered poll leaked B's charter's request: %+v", r)
		}
	}
	if scoped := pollWorkstreamMCP(t, client, base, tsA.token(), wsB.ID); len(scoped) != 0 {
		t.Fatalf("A's poll filtered to B's workstreamId = %+v, want empty (no oracle)", scoped)
	}
	if items := readMemoryMCP(t, client, base, tsA.token(), wsB.ID); len(items) != 0 {
		t.Fatalf("A's read scoped to B's workstreamId = %+v, want empty (no oracle)", items)
	}
	t.Log("PASS leg B N1 — cross-grant containment holds installed: A's poll/read never surface B's still-open charter or request")

	// --- submit both plans promptly (inside each request's ~120s TTL) ----------------
	stepA := map[string]string{"resourceId": resourceID, "command": "echo WD9-INSTALLED-A1"}
	runA1, planStatusA, isErrA, textA := submitPlanMCP(t, client, base, tsA.token(), req1.RequestID, []map[string]string{stepA})
	if isErrA || planStatusA != "proposed" {
		t.Fatalf("A's plan.submit failed: isErr=%v status=%q text=%q", isErrA, planStatusA, textA)
	}
	stepB := map[string]string{"resourceId": resourceID, "command": "echo WD9-INSTALLED-B1"}
	runB, planStatusB, isErrB, textB := submitPlanMCP(t, client, base, tsB.token(), reqB.RequestID, []map[string]string{stepB})
	if isErrB || planStatusB != "proposed" {
		t.Fatalf("B's plan.submit failed: isErr=%v status=%q text=%q", isErrB, planStatusB, textB)
	}

	// --- leg A: preview -> approve -> the flagged resource's invocation-level park+ ---
	// approve -> completed -> exactly one accrual.
	preview := getRun(t, client, base, sess, csrf, runA1)
	if preview.Status != "proposed" || len(preview.Steps) != 1 || preview.Steps[0].Command != "echo WD9-INSTALLED-A1" || preview.Steps[0].ResourceID != resourceID {
		t.Fatalf("preview run %+v, want proposed with the sealed command+resource visible (T-091 preview gate, installed)", preview)
	}
	t.Log("PASS leg A2 — the operator's GET on the proposed run carries the exact sealed command (preview gate holds installed)")

	approved, acode := approveRun(t, client, base, sess, csrf, runA1)
	if acode != http.StatusOK || approved.Status != "pending" {
		t.Fatalf("approve A's run: status %d, view %+v, want 200/pending", acode, approved)
	}
	inv := pollPendingInvocation(t, client, base, sess, csrf, resourceID, 90*time.Second)
	if inv.RunID != runA1 {
		t.Fatalf("parked invocation belongs to run %q, want %q", inv.RunID, runA1)
	}
	resolveInvocation(t, client, base, inv.ID, sess, csrf, "approve")
	final := waitRunStatusHTTP(t, client, base, sess, csrf, runA1, "completed", 60*time.Second)
	if final.Actor != composerA {
		t.Fatalf("completed run actor = %q, want %s", final.Actor, composerA)
	}
	accrued := waitForMemory(t, client, base, sess, csrf, "run", runA1, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runA1
	})
	if accrued.HasContent {
		t.Fatalf("accrued memory %s hasContent=true, want false", accrued.ID)
	}
	var accrualCount int
	for _, m := range listMemories(t, client, base, sess, csrf, "run", runA1) {
		if m.Ref == "run:"+runA1 {
			accrualCount++
		}
	}
	if accrualCount != 1 {
		t.Fatalf("run %s: %d accrued memory rows, want exactly 1", runA1, accrualCount)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+accrued.ID, "memory.written", 30*time.Second)
	t.Logf("PASS leg A3 — the invocation-level park+approve drove the plan to completed (actor=%s); exactly one content-free accrual %s", composerA, accrued.ID)

	// --- leg B: N3 — wait for B's FRESH open request (the cascade's second victim), --
	// then revoke.
	reqB2 := waitForFreshPlanRequest(t, client, base, tsB.token(), wsB.ID, reqB.RequestID, 150*time.Second)
	revokeGrantHTTP(t, client, base, sess, csrf, wsB.GrantID)

	pollProxyTargetOutcome(t, client, base, sess, csrf, "run:"+runB, "run.canceled", 30*time.Second)
	if n := countProxyTargetOutcome(t, client, base, sess, csrf, "run:"+runB, "run.denied"); n != 0 {
		t.Fatalf("run.denied count for a cascade-canceled run = %d, want ZERO (revocation is not a human deny)", n)
	}
	if v := getRun(t, client, base, sess, csrf, runB); v.Status != "canceled" {
		t.Fatalf("B's run status after revoke = %q, want canceled", v.Status)
	}
	wsBDetail := getWorkstream(t, client, base, sess, csrf, wsB.ID)
	var req2Closed bool
	for _, pr := range wsBDetail.PlanRequests {
		if pr.ID == reqB2.RequestID {
			req2Closed = true
			if pr.Status != "closed" {
				t.Fatalf("B's open request after revoke = %+v, want status=closed", pr)
			}
		}
	}
	if !req2Closed {
		t.Fatalf("plan request %s missing from B's detail history after revoke", reqB2.RequestID)
	}
	if reqs := pollWorkstreamMCP(t, client, base, tsB.token(), ""); len(reqs) != 0 {
		t.Fatalf("B's poll after grant revoke = %+v, want empty", reqs)
	}
	if _, _, isErrSubmit, textSubmit := submitPlanMCP(t, client, base, tsB.token(), reqB2.RequestID, []map[string]string{stepB}); !isErrSubmit {
		t.Fatalf("B's plan.submit after grant revoke succeeded (want refused): %q", textSubmit)
	}
	if items := readMemoryMCP(t, client, base, tsB.token(), ""); len(items) != 0 {
		t.Fatalf("B's read after grant revoke = %+v, want empty", items)
	}
	if _, rcode := approveRun(t, client, base, sess, csrf, runB); rcode != http.StatusForbidden {
		t.Fatalf("approve on B's cascade-canceled/dead-grant run: status %d, want 403", rcode)
	}
	t.Log("PASS leg B N3 — grant revocation killed all three agent-plane surfaces installed: the proposed run canceled (ZERO run.denied), the open request closed, poll/read empty, submit refused, approve forbidden")

	// --- N2 (cheap): the share flip on A's scope --------------------------------------
	m := createMemory(t, client, base, sess, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: wsA.ID, Key: "wd9-inst-sg-" + suffix, Content: "wd9 installed shared " + suffix,
	})
	for _, it := range readMemoryMCP(t, client, base, tsA.token(), wsA.ID) {
		if it.ID == m.ID {
			t.Fatalf("unshared authored row visible before the share flip: %+v", it)
		}
	}
	shared, scode := shareMemory(t, client, base, sess, csrf, m.ID)
	if scode != http.StatusOK || !shared.AgentVisible {
		t.Fatalf("share memory: status %d view %+v, want 200/agentVisible=true", scode, shared)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+m.ID, "memory.shared", 30*time.Second)
	var foundShared bool
	for _, it := range readMemoryMCP(t, client, base, tsA.token(), wsA.ID) {
		if it.ID == m.ID {
			foundShared = true
			if it.Content != "wd9 installed shared "+suffix {
				t.Fatalf("shared row content = %q, want the authored content", it.Content)
			}
		}
	}
	if !foundShared {
		t.Fatalf("shared row %s not visible to memory.read after the share flip", m.ID)
	}
	unshared, ucode := unshareMemory(t, client, base, sess, csrf, m.ID)
	if ucode != http.StatusOK || unshared.AgentVisible {
		t.Fatalf("unshare memory: status %d view %+v, want 200/agentVisible=false", ucode, unshared)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "memory:"+m.ID, "memory.unshared", 30*time.Second)
	for _, it := range readMemoryMCP(t, client, base, tsA.token(), wsA.ID) {
		if it.ID == m.ID {
			t.Fatalf("shared-then-unshared row still visible: %+v", it)
		}
	}
	t.Log("PASS N2 — the share flip gates agent-plane visibility installed: invisible -> shared+content -> unshared+invisible")

	// --- leg A: tick2's FRESH request -> read#2 sees the accrued pointer -> the loop --
	// closes -> the deny cycle.
	req2 := waitForFreshPlanRequest(t, client, base, tsA.token(), wsA.ID, req1.RequestID, 150*time.Second)
	items2 := readMemoryMCP(t, client, base, tsA.token(), wsA.ID)
	var sawAccrued bool
	for _, it := range items2 {
		if it.ID == accrued.ID {
			sawAccrued = true
			if it.Content != "" || it.Ref != "run:"+runA1 {
				t.Fatalf("accrued row read back = %+v, want content-free with ref=run:%s", it, runA1)
			}
		}
	}
	if !sawAccrued {
		t.Fatalf("memory.read #2 (items=%+v) never returned the just-accrued pointer %s — the loop did not close installed", items2, accrued.ID)
	}
	// NOT an exact-2 count here (unlike the compose TestWD9HappyPath, which isolates one
	// composer to exactly its own two happy-path reads): this installed test PACKS N1's
	// out-of-scope read and N2's three share-gate reads onto the SAME composer A token
	// for the wd1 time budget (T-093's ratified shape), so composer A's total
	// memory.read count is a superset of "just the loop-closure beats" and is not a
	// meaningful per-leg signal on its own — the content check above (accrued.ID present,
	// content-free, correct ref) IS the loop-closure proof. Assert only the lower bound
	// (>=2: baseline + this call) and record the observed total for the report.
	read2 := pollProxyActorOutcomeCount(t, client, base, sess, csrf, composerA, "memory.read", 2, 30*time.Second)
	t.Logf("PASS leg A4 — the loop closed installed: tick2's memory.read #2 returned tick1's accrued pointer %s (composer A's cumulative memory.read count across all packed legs so far: %d)", accrued.ID, len(read2))

	stepA2 := map[string]string{"resourceId": resourceID, "command": "echo WD9-INSTALLED-A2"}
	runA2, planStatusA2, isErrA2, textA2 := submitPlanMCP(t, client, base, tsA.token(), req2.RequestID, []map[string]string{stepA2})
	if isErrA2 || planStatusA2 != "proposed" {
		t.Fatalf("A's second plan.submit failed: isErr=%v status=%q text=%q", isErrA2, planStatusA2, textA2)
	}
	denied, dcode := denyRun(t, client, base, sess, csrf, runA2)
	if dcode != http.StatusOK || denied.Status != "canceled" {
		t.Fatalf("deny A's second run: status %d view %+v, want 200/canceled", dcode, denied)
	}
	pollProxyTargetOutcome(t, client, base, sess, csrf, "run:"+runA2, "run.canceled", 30*time.Second)
	if n := countProxyTargetOutcome(t, client, base, sess, csrf, "run:"+runA2, "run.denied"); n != 1 {
		t.Fatalf("run.denied count for %s = %d, want exactly 1", runA2, n)
	}
	accrued2 := waitForMemory(t, client, base, sess, csrf, "run", runA2, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runA2
	})
	if accrued2.HasContent {
		t.Fatalf("denied-run accrual %s hasContent=true, want false", accrued2.ID)
	}
	var accrual2Count int
	for _, m := range listMemories(t, client, base, sess, csrf, "run", runA2) {
		if m.Ref == "run:"+runA2 {
			accrual2Count++
		}
	}
	if accrual2Count != 1 {
		t.Fatalf("denied run %s: %d accrued memory rows, want exactly 1", runA2, accrual2Count)
	}
	t.Logf("PASS leg A5 — the deny cycle installed: proposed->canceled with a distinct run.denied (exactly once) beside run.canceled; the denied plan still accrued exactly one content-free pointer")

	// --- leg D: N4 — an unanswered request expires and the charter never wedges ------
	agentD := "wd9-inst-d-" + suffix
	tsD := newWD9TokenSource(t, client, base, sess, csrf, agentD, resourceID)
	wsD := charterWorkstream(t, client, base, sess, csrf, charterOpts{
		objective: "wd9 installed d " + suffix, resources: []string{resourceID},
		intervalSeconds: 60, stepBudget: 100, kind: "adaptive", composer: "agent:" + agentD,
	})
	t.Cleanup(func() { retireWorkstream(t, client, base, sess, csrf, wsD.ID) })
	reqD1 := waitForPlanRequest(t, client, base, tsD.token(), wsD.ID, 150*time.Second)
	// Ignore it deliberately — it must expire on its own rather than wedge the charter.
	pollProxyTargetOutcome(t, client, base, sess, csrf, "plan-request:"+reqD1.RequestID, "plan.expired", 90*time.Second)
	reqD2 := waitForFreshPlanRequest(t, client, base, tsD.token(), wsD.ID, reqD1.RequestID, 90*time.Second)
	if !reqD2.ExpiresAt.After(reqD1.ExpiresAt) {
		t.Fatalf("D's fresh request expiresAt = %s, want it after the expired request's %s", reqD2.ExpiresAt, reqD1.ExpiresAt)
	}
	if wsDAfter := getWorkstream(t, client, base, sess, csrf, wsD.ID); wsDAfter.Status != "active" {
		t.Fatalf("charter D status after an expired request = %q, want active", wsDAfter.Status)
	}
	t.Logf("PASS leg D N4 — an unanswered plan request expired (plan.expired chained) and the next tick opened a fresh, later-expiring request; charter %s never wedged", wsD.ID)

	t.Logf("PASS WD-9 INSTALLED — charter->tick->read->compose/submit->preview->approve->(flagged park)->complete->accrue->the next tick's read closes the loop (A); cross-grant containment + revoke-kills-all (B, N1+N3); the share flip (N2); the deny cycle (A tick2); expiry never wedges (D, N4) — all through Caddy HTTPS at the 60s production floor")
}

// pollProxyActorOutcomeCount waits until at least `want` events matching (actor,
// outcome) have reached the chain as read back through the operator audit proxy — the
// installed-stack analogue of wd9_driver.go's pollActorOutcomeCount (which reads
// audit-service directly; the external wd1 driver has no host-internal audit reach).
func pollProxyActorOutcomeCount(t *testing.T, client *http.Client, base, sess, csrf, actor, outcome string, want int, within time.Duration) []auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var matched []auditEvent
		for _, e := range listAllEventsViaProxy(t, client, base, sess, csrf) {
			if e.Actor == actor && e.Outcome == outcome {
				matched = append(matched, e)
			}
		}
		if len(matched) >= want {
			return matched
		}
		if time.Now().After(deadline) {
			t.Fatalf("actor %q outcome %q: got %d event(s) via the audit proxy, want >=%d within %s", actor, outcome, len(matched), want, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// listAllEventsViaProxy pages the operator audit read-proxy (GET /v1/audit/events,
// T-024) through Caddy: the external wd1 driver has no host-internal audit-service
// reach, so per-id actor + exactly-once assertions ride the same wire the cockpit
// timeline uses (chain INTEGRITY stays host-internal in run-wd1.sh).
func listAllEventsViaProxy(t *testing.T, client *http.Client, base, sess, csrf string) []auditEvent {
	t.Helper()
	var out []auditEvent
	cursor := ""
	for {
		u := base + "/v1/audit/events?limit=200"
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Cookie", cookieHeader(sess, csrf))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("list audit events via proxy: %v", err)
		}
		var page eventPage
		derr := json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list audit events via proxy: status %d", resp.StatusCode)
		}
		if derr != nil {
			t.Fatalf("decode audit events via proxy: %v", derr)
		}
		out = append(out, page.Items...)
		if page.Page.NextCursor == "" {
			return out
		}
		cursor = page.Page.NextCursor
	}
}

// pollProxyTargetOutcome waits for an event with the given target + outcome to surface
// on the operator audit timeline — api's outbox relay drains asynchronously, so chain
// assertions poll before counting (the house pattern).
func pollProxyTargetOutcome(t *testing.T, client *http.Client, base, sess, csrf, target, outcome string, within time.Duration) auditEvent {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		for _, e := range listAllEventsViaProxy(t, client, base, sess, csrf) {
			if e.Target == target && e.Outcome == outcome {
				return e
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("event %q on %q never surfaced on the audit timeline within %s", outcome, target, within)
		}
		time.Sleep(2 * time.Second)
	}
}

// countProxyTargetOutcome counts target+outcome events on the timeline — the
// exactly-once assertions (N1 redact, the expiry flip) after the poll has proven
// delivery.
func countProxyTargetOutcome(t *testing.T, client *http.Client, base, sess, csrf, target, outcome string) int {
	t.Helper()
	n := 0
	for _, e := range listAllEventsViaProxy(t, client, base, sess, csrf) {
		if e.Target == target && e.Outcome == outcome {
			n++
		}
	}
	return n
}

// wd1MemSuffix returns a time-derived suffix so re-runs against a dirty installed
// stack never collide on an authored memory key (the T-073 fresh-key lesson). Named
// distinctly from the compose suite's m8suffix so a combined `e2e wd1` tag set
// still compiles.
func wd1MemSuffix() string {
	return time.Now().UTC().Format("150405.000000000")
}

func assertResourceVisible(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/resources", nil)
	req.Header.Set("Cookie", cookieHeader(sess, csrf))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list resources: status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), resourceID) {
		t.Fatalf("resource picker missing the seeded resource %s: %s", resourceID, string(b))
	}
}

// assertAuditTimelineReadable drives the operator audit-timeline read through
// Caddy -> services/api read-proxy -> audit-service (T-024-a). It polls because
// api delivers auth.* events to the chain asynchronously through its outbox
// relay (T-022); the operator's own auth.login.success must appear in the page
// read back through the proxy.
func assertAuditTimelineReadable(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, base+"/v1/audit/events?limit=100", nil)
		req.Header.Set("Cookie", cookieHeader(sess, csrf))
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("read audit timeline: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("audit timeline (Caddy->api->audit): status %d body=%s", resp.StatusCode, string(body))
		}
		s := string(body)
		// the operator's own login event, chained + read back through the proxy.
		if strings.Contains(s, "auth.login.success") && strings.Contains(s, "operator:admin") {
			t.Log("operator read the audit timeline through Caddy -> api proxy -> audit-service; own auth.login.success present")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit timeline never showed the operator's auth.login.success (relay lag?): %s", s)
		}
		time.Sleep(2 * time.Second)
	}
}

func logout(t *testing.T, client *http.Client, base, sess, csrf string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/auth/logout", nil)
	req.Header.Set("Cookie", cookieHeader(sess, csrf))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: status %d", resp.StatusCode)
	}
}
