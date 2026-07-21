//go:build e2e

// WD-9 — the v0.9.0 release gate (T-092, docs/releases/v0.9.0 §2): on the real stack a
// real external MCP client reads governed memory it is entitled to, composes the next
// tick's plan from it, a human reviews that exact plan before anything runs, the
// governed engine executes it, the outcome accrues — and the NEXT tick's read provably
// sees what the last tick learned. This is the fourth initiator posture (v0.5 operator →
// v0.6 agent → v0.7 schedule → v0.9 agent-composed-under-schedule): the ticker's adaptive
// branch opens an inbound plan request instead of launching, the composer answers it
// through the SAME keyless gateway (three new control-plane tools on the run.launch
// pattern), and every step the plan-preview approves rides the UNCHANGED v0.5/v0.6/v0.7
// sequencer + v0.8 accrual hook.
//
// This file (T-092a) covers the happy path (legs 1-8) plus N5 (plan ⊆ grant still holds
// on the third launch caller) folded in as a short leg at the end — reusing the SAME
// open request the loop-closure leg already discovered, since api never marks a request
// submitted on a refused submission (SubmitPlan's out-of-grant early-return, adaptive_
// internal.go). The negatives N1 (cross-grant containment), N2 (the share gate), N3
// (revocation kills all three surfaces), N4 (expiry never wedges) are wd9_negatives_
// test.go (T-092b). N6 (the exact 5-tool tools/list set) is already live in the amended
// WD-8 N2 (wd8_negatives_test.go's TestWD8AgentPlaneRejected) — not duplicated here.
//
// WD-5/6/7/8 stay green in the suite as the elevated regression guard (F-c's shared
// launch core, F-a's Run-status universe, and the byte-identical operator memory surface
// are all proven by those suites unchanged).
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestWD9HappyPath — WD-9 §2 legs 1-8 + N5 (facet 8's chain-hash-intactness is the
// suite's trailing `audit-service verify`, not re-asserted here).
func TestWD9HappyPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// --- Leg 1: the operator charters an adaptive Workstream in one consent act -------
	suffix := w9suffix()
	agentID := "wd9-composer-" + suffix
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentID, workstreamResourceID)
	composer := "agent:" + agentID

	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd9 loop closure " + suffix,
		resources:       []string{workstreamResourceID},
		intervalSeconds: 5,
		stepBudget:      100,
		kind:            "adaptive",
		composer:        composer,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })
	if ws.Kind != "adaptive" || ws.Composer != composer {
		t.Fatalf("charter kind/composer = %q/%q, want adaptive/%s", ws.Kind, ws.Composer, composer)
	}

	chartered := pollTargetOutcome(t, auditURL, auditTok, "workstream:"+ws.ID, "workstream.chartered", 30*time.Second)
	if chartered.Actor != "operator:admin" || chartered.Edge != "human-agent" {
		t.Fatalf("workstream.chartered = {actor:%q edge:%q}, want {operator:admin human-agent}", chartered.Actor, chartered.Edge)
	}
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+ws.GrantID, "grant.issued", 30*time.Second)
	t.Logf("PASS WD-9 leg 1 — adaptive charter %s consented (grantee=composer %s); workstream.chartered + grant.issued chained as one act", ws.ID, composer)

	// --- Leg 2: the tick publishes, nobody composes yet -------------------------------
	req1 := waitForPlanRequest(t, client, mcpURL, tok, ws.ID, 30*time.Second)
	if req1.WorkstreamID != ws.ID {
		t.Fatalf("plan request workstreamId = %q, want %s", req1.WorkstreamID, ws.ID)
	}
	if len(req1.Resources) != 1 || req1.Resources[0] != workstreamResourceID {
		t.Fatalf("plan request resources = %v, want [%s]", req1.Resources, workstreamResourceID)
	}
	if !contains(req1.Verbs, "exec") {
		t.Fatalf("plan request verbs = %v, want to contain exec", req1.Verbs)
	}
	if req1.MaxStepsPerRun != 10 {
		t.Fatalf("plan request maxStepsPerRun = %d, want 10", req1.MaxStepsPerRun)
	}
	if req1.ExpiresAt.IsZero() {
		t.Fatal("plan request expiresAt is zero")
	}
	requested := pollTargetOutcome(t, auditURL, auditTok, "plan-request:"+req1.RequestID, "plan.requested", 30*time.Second)
	if requested.Actor != "workstream:"+ws.ID {
		t.Fatalf("plan.requested actor = %q, want workstream:%s", requested.Actor, ws.ID)
	}
	t.Logf("PASS WD-9 leg 2 — tick opened plan request %s (objective/resources/verbs/bounds/TTL composition context intact); plan.requested chained", req1.RequestID)

	// --- Leg 3: the agent reads its memory — chained (baseline, likely empty) --------
	_ = readMemoryMCP(t, client, mcpURL, tok, ws.ID)
	read1 := pollActorOutcomeCount(t, auditURL, auditTok, composer, "memory.read", 1, 30*time.Second)
	if len(read1) != 1 {
		t.Fatalf("memory.read count for composer %s after call #1 = %d, want exactly 1", composer, len(read1))
	}
	if read1[0].Edge != "agent-memory" {
		t.Fatalf("memory.read edge = %q, want agent-memory (SEC-32)", read1[0].Edge)
	}
	if !strings.HasPrefix(read1[0].Target, "memory:") {
		t.Fatalf("memory.read target = %q, want it to start with memory:", read1[0].Target)
	}
	t.Logf("PASS WD-9 leg 3 — baseline memory.read #1 chained exactly once (actor=%s, edge=agent-memory)", composer)

	// --- Leg 4: the agent composes and submits ----------------------------------------
	step := map[string]string{"resourceId": workstreamResourceID, "command": "echo WD9-TICK"}
	runID, planStatus, isErr, text := submitPlanMCP(t, client, mcpURL, tok, req1.RequestID, []map[string]string{step})
	if isErr {
		t.Fatalf("plan.submit refused (want success): %q", text)
	}
	if planStatus != "proposed" {
		t.Fatalf("plan.submit run status = %q, want proposed", planStatus)
	}
	submitted := pollTargetOutcome(t, auditURL, auditTok, "plan-request:"+req1.RequestID, "plan.submitted", 30*time.Second)
	if submitted.Actor != composer {
		t.Fatalf("plan.submitted actor = %q, want %s", submitted.Actor, composer)
	}
	wsDetail := getWorkstream(t, client, apiURL, session, csrf, ws.ID)
	var req1Found bool
	for _, pr := range wsDetail.PlanRequests {
		if pr.ID == req1.RequestID {
			req1Found = true
			if pr.Status != "submitted" || pr.RunID != runID {
				t.Fatalf("plan request history entry %+v, want status=submitted runId=%s", pr, runID)
			}
		}
	}
	if !req1Found {
		t.Fatalf("plan request %s missing from the charter's detail history", req1.RequestID)
	}
	t.Logf("PASS WD-9 leg 4 — plan.submit created proposed run %s; plan.submitted chained (actor=%s); request flipped submitted", runID, composer)

	// --- Leg 5: a human reviews the exact plan, then approves -------------------------
	preview := getRun(t, client, apiURL, session, csrf, runID)
	if preview.Status != "proposed" {
		t.Fatalf("preview run status = %q, want proposed", preview.Status)
	}
	if len(preview.Steps) != 1 || preview.Steps[0].Command != "echo WD9-TICK" || preview.Steps[0].ResourceID != workstreamResourceID {
		t.Fatalf("preview steps = %+v, want one step {resourceId:%s command:echo WD9-TICK}", preview.Steps, workstreamResourceID)
	}
	approved, code := approveRun(t, client, apiURL, session, csrf, runID)
	if code != http.StatusOK {
		t.Fatalf("approve run: status %d, want 200", code)
	}
	if approved.Status != "pending" {
		t.Fatalf("approved run status = %q, want pending", approved.Status)
	}
	launched := pollTargetOutcome(t, auditURL, auditTok, "run:"+runID, "run.launched", 30*time.Second)
	if launched.Actor != composer {
		t.Fatalf("run.launched actor = %q, want %s (deferred to approve time, not submit)", launched.Actor, composer)
	}
	t.Logf("PASS WD-9 leg 5 — the plan preview carried the sealed command; approve flipped proposed->pending; run.launched deferred to approve time (actor=%s)", composer)

	// --- Leg 6: the existing engine drives it unchanged -------------------------------
	final := waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "completed", 60*time.Second)
	if final.Actor != composer || final.GrantID != ws.GrantID {
		t.Fatalf("completed run provenance = {actor:%q grant:%q}, want {%s %s}", final.Actor, final.GrantID, composer, ws.GrantID)
	}
	t.Logf("PASS WD-9 leg 6 — the unchanged sequencer drove the approved plan to completed (actor=%s, grant=%s)", composer, ws.GrantID)

	// --- Leg 7: terminal accrues — and the loop closes --------------------------------
	accrued := waitForMemory(t, client, apiURL, session, csrf, "run", runID, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runID
	})
	if accrued.HasContent {
		t.Fatalf("accrued memory %s hasContent=true, want false (content-free pointer)", accrued.ID)
	}
	wantTrace := "run:" + runID + ":completed"
	if accrued.SourceTrace != wantTrace {
		t.Fatalf("accrued memory sourceTrace = %q, want %q", accrued.SourceTrace, wantTrace)
	}
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+accrued.ID, "memory.written", 30*time.Second)
	var accrualCount int
	for _, m := range listMemories(t, client, apiURL, session, csrf, "run", runID) {
		if m.Ref == "run:"+runID {
			accrualCount++
		}
	}
	if accrualCount != 1 {
		t.Fatalf("run %s: %d accrued memory rows, want exactly 1", runID, accrualCount)
	}

	// The loop closes: the NEXT tick's request opens, and this composer's SECOND
	// memory.read sees the row the first tick's Run just accrued.
	req2 := waitForFreshPlanRequest(t, client, mcpURL, tok, ws.ID, req1.RequestID, 30*time.Second)
	items2 := readMemoryMCP(t, client, mcpURL, tok, ws.ID)
	var sawAccrued bool
	for _, it := range items2 {
		if it.ID == accrued.ID {
			sawAccrued = true
			if it.Content != "" {
				t.Fatalf("accrued row read back with content = %q, want content-free", it.Content)
			}
			if it.Ref != "run:"+runID {
				t.Fatalf("accrued row read back ref = %q, want run:%s", it.Ref, runID)
			}
		}
	}
	if !sawAccrued {
		t.Fatalf("memory.read #2 (items=%+v) never returned the just-accrued pointer %s — the loop did not close", items2, accrued.ID)
	}
	read2 := pollActorOutcomeCount(t, auditURL, auditTok, composer, "memory.read", 2, 30*time.Second)
	if len(read2) != 2 {
		t.Fatalf("memory.read count for composer %s after call #2 = %d, want exactly 2", composer, len(read2))
	}
	var sawAccruedInTarget bool
	for _, e := range read2 {
		if strings.Contains(e.Target, accrued.ID) {
			sawAccruedInTarget = true
		}
	}
	if !sawAccruedInTarget {
		t.Fatalf("no memory.read event's target lists the accrued row %s: %+v", accrued.ID, read2)
	}
	t.Logf("PASS WD-9 leg 7 — the loop closed: tick 2's memory.read #2 returned the pointer tick 1's Run accrued, and the read event's target names it")

	// --- N5 (folded in): plan ⊆ grant still refuses an out-of-grant resource ---------
	// req2 stays OPEN across this refusal — SubmitPlan's LaunchBoundedProposed returns
	// early on a refusal (adaptive_internal.go), so MarkPlanRequestSubmitted is never
	// called; the SAME req2 is reused for leg 8 below.
	badStep := map[string]string{"resourceId": resourceID, "command": "echo WD9-OUT-OF-GRANT"}
	_, _, badErr, badText := submitPlanMCP(t, client, mcpURL, tok, req2.RequestID, []map[string]string{badStep})
	if !badErr {
		t.Fatalf("plan.submit with an out-of-grant resource succeeded (want a SEC-29 refusal): %q", badText)
	}
	if !strings.Contains(badText, "refused") {
		t.Fatalf("out-of-grant refusal text = %q, want it to say refused", badText)
	}
	pollTargetOutcome(t, auditURL, auditTok, "grant:"+ws.GrantID, "run.launch.denied", 30*time.Second)
	t.Logf("PASS WD-9 N5 — an out-of-grant plan.submit was refused (SEC-29, chained run.launch.denied); no Run created, req2 stays open")

	// --- Leg 8: the deny cycle (C1 live) ----------------------------------------------
	runID2, planStatus2, isErr2, text2 := submitPlanMCP(t, client, mcpURL, tok, req2.RequestID, []map[string]string{step})
	if isErr2 {
		t.Fatalf("plan.submit (valid, second request) refused: %q", text2)
	}
	if planStatus2 != "proposed" {
		t.Fatalf("second plan.submit run status = %q, want proposed", planStatus2)
	}
	denied, code2 := denyRun(t, client, apiURL, session, csrf, runID2)
	if code2 != http.StatusOK {
		t.Fatalf("deny run: status %d, want 200", code2)
	}
	if denied.Status != "canceled" {
		t.Fatalf("denied run status = %q, want canceled", denied.Status)
	}
	pollTargetOutcome(t, auditURL, auditTok, "run:"+runID2, "run.canceled", 30*time.Second)
	if n := countTargetOutcome(t, auditURL, auditTok, "run:"+runID2, "run.denied"); n != 1 {
		t.Fatalf("run.denied count for %s = %d, want exactly 1", runID2, n)
	}
	accrued2 := waitForMemory(t, client, apiURL, session, csrf, "run", runID2, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runID2
	})
	if accrued2.HasContent {
		t.Fatalf("denied-run accrual %s hasContent=true, want false", accrued2.ID)
	}
	wantTrace2 := "run:" + runID2 + ":canceled"
	if accrued2.SourceTrace != wantTrace2 {
		t.Fatalf("denied-run accrual sourceTrace = %q, want %q", accrued2.SourceTrace, wantTrace2)
	}
	var accrual2Count int
	for _, m := range listMemories(t, client, apiURL, session, csrf, "run", runID2) {
		if m.Ref == "run:"+runID2 {
			accrual2Count++
		}
	}
	if accrual2Count != 1 {
		t.Fatalf("denied run %s: %d accrued memory rows, want exactly 1", runID2, accrual2Count)
	}
	t.Logf("PASS WD-9 leg 8 — deny flipped proposed->canceled with a distinct run.denied (exactly once) beside run.canceled; the denied plan still accrued exactly one content-free pointer (C1)")

	t.Logf("PASS WD-9 happy path — charter -> tick -> read -> compose/submit -> preview/approve -> execute -> accrue -> the next tick's read proves the loop closed; plus N5 (plan subset grant holds unchanged)")
}

// contains reports whether s contains v (small local helper — avoids importing slices
// solely for this one string-slice membership check).
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
