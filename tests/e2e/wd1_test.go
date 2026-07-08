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
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestWedgeDemoInstalledStack(t *testing.T) {
	// Trust the self-signed local CA (Caddy `tls internal`) for the whole driver.
	// One test per binary (tag wd1), so mutating the default transport is safe.
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	wsBase := strings.Replace(base, "https://", "wss://", 1)
	client := &http.Client{Timeout: 20 * time.Second}

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
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := &http.Client{Timeout: 30 * time.Second}

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
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := &http.Client{Timeout: 60 * time.Second}
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
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	base := env("WD1_BASE_URL", "https://aireos-wd1-host")
	client := &http.Client{Timeout: 90 * time.Second}

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
