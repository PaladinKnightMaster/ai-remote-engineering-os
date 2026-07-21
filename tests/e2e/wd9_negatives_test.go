//go:build e2e

// WD-9 negatives (T-092b, docs/releases/v0.9.0 §2) — the containment legs of Adaptive
// Workstreams, all live: N1 cross-grant containment (SEC-32), N2 the share gate (MEM-6),
// N3 revocation kills all three agent-plane surfaces (DUR-10), N4 expiry never wedges
// the charter (PLAN-1). N5 (plan subset grant on the third launch caller) is folded into
// wd9_test.go's happy path (it needed an already-open request the happy path already
// has in flight); N6 (the exact 5-tool tools/list set) is already live in the amended
// WD-8 N2 (wd8_negatives_test.go's TestWD8AgentPlaneRejected) — not duplicated here.
package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestWD9CrossGrant — N1 (the SEC-32 negative): a second charter/grant/composer exists
// with its own accrued memory; the first composer's workstream.poll never lists the
// second charter's request, its memory.read never returns the second charter's rows
// (unfiltered OR filtered to the foreign workstreamId — the latter answers EMPTY, not an
// error, the no-oracle proof), and the read event still chains with the empty Target
// shape ("memory:" with no ids, the T-090 convention).
func TestWD9CrossGrant(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	suffix := w9suffix()

	agentA := "wd9-cg-a-" + suffix
	tokA := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentA, workstreamResourceID)
	composerA := "agent:" + agentA
	agentB := "wd9-cg-b-" + suffix
	tokB := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentB, workstreamResourceID)
	composerB := "agent:" + agentB

	wsA := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd9 cg a " + suffix, resources: []string{workstreamResourceID},
		intervalSeconds: 5, stepBudget: 100, kind: "adaptive", composer: composerA,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, wsA.ID) })
	wsB := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd9 cg b " + suffix, resources: []string{workstreamResourceID},
		intervalSeconds: 5, stepBudget: 100, kind: "adaptive", composer: composerB,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, wsB.ID) })

	reqA := waitForPlanRequest(t, client, mcpURL, tokA, wsA.ID, 30*time.Second)
	reqB := waitForPlanRequest(t, client, mcpURL, tokB, wsB.ID, 30*time.Second)

	// Drive B's charter through at least one accrual so B has rows A must NOT see.
	stepB := map[string]string{"resourceId": workstreamResourceID, "command": "echo WD9-CG-B"}
	runB, statusB, isErrB, textB := submitPlanMCP(t, client, mcpURL, tokB, reqB.RequestID, []map[string]string{stepB})
	if isErrB || statusB != "proposed" {
		t.Fatalf("B's plan.submit failed: isErr=%v status=%q text=%q", isErrB, statusB, textB)
	}
	if _, code := approveRun(t, client, apiURL, session, csrf, runB); code != http.StatusOK {
		t.Fatalf("approve B's run: status %d, want 200", code)
	}
	waitRunStatusHTTP(t, client, apiURL, session, csrf, runB, "completed", 60*time.Second)
	accruedB := waitForMemory(t, client, apiURL, session, csrf, "run", runB, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runB
	})

	// A's poll never lists B's request — unfiltered or filtered to B's charter. The
	// positive control: A's own request IS present (proves the filter is doing real
	// work, not just returning empty for everyone).
	var sawOwn bool
	for _, r := range pollWorkstreamMCP(t, client, mcpURL, tokA, "") {
		if r.WorkstreamID == wsB.ID {
			t.Fatalf("A's unfiltered poll leaked B's charter's request: %+v", r)
		}
		if r.RequestID == reqA.RequestID {
			sawOwn = true
		}
	}
	if !sawOwn {
		t.Fatalf("A's unfiltered poll did not list A's own request %s (positive control)", reqA.RequestID)
	}
	if scoped := pollWorkstreamMCP(t, client, mcpURL, tokA, wsB.ID); len(scoped) != 0 {
		t.Fatalf("A's poll filtered to B's workstreamId = %+v, want empty (no oracle)", scoped)
	}

	// A's unfiltered memory.read never returns B's accrued row.
	for _, it := range readMemoryMCP(t, client, mcpURL, tokA, "") {
		if it.ID == accruedB.ID {
			t.Fatalf("A's unfiltered memory.read leaked B's accrued row %s", accruedB.ID)
		}
	}
	// A's read explicitly scoped to B's charter -> EMPTY, not an error.
	if items := readMemoryMCP(t, client, mcpURL, tokA, wsB.ID); len(items) != 0 {
		t.Fatalf("A's read scoped to B's workstreamId = %+v, want empty (no oracle)", items)
	}

	// Both A memory.read calls chained; at least one carries the empty "memory:" Target
	// shape (A owns nothing of its own here — neither call could return any id).
	readA := pollActorOutcomeCount(t, auditURL, auditTok, composerA, "memory.read", 2, 30*time.Second)
	if len(readA) != 2 {
		t.Fatalf("memory.read count for composer A = %d, want exactly 2", len(readA))
	}
	var sawEmptyTarget bool
	for _, e := range readA {
		if e.Target == "memory:" {
			sawEmptyTarget = true
		}
	}
	if !sawEmptyTarget {
		t.Fatalf("no memory.read event for A carried the empty Target shape 'memory:' (no ids): %+v", readA)
	}

	t.Logf("PASS WD-9 N1 — cross-grant containment holds: A's poll/read never surface B's charter, request, or accrued row; the foreign-scoped read answers empty (no oracle) and still chains")
}

// TestWD9ShareGate — N2 (MEM-6, the share flip): an operator-authored row is invisible
// to memory.read until memory.shared flips it (chained exactly once); after
// memory.unshared it is invisible again (chained exactly once); a re-share-then-re-share
// double call on a second row emits no duplicate memory.shared (idempotent-200, the D1
// flip() precedent applied to the agent-plane share state).
func TestWD9ShareGate(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	suffix := w9suffix()

	agentA := "wd9-sg-" + suffix
	tokA := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentA, workstreamResourceID)
	composerA := "agent:" + agentA
	wsA := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd9 share gate " + suffix, resources: []string{workstreamResourceID},
		intervalSeconds: 5, stepBudget: 100, kind: "adaptive", composer: composerA,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, wsA.ID) })

	// An operator authors a memory into A's workstream scope — unshared by default.
	m := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: wsA.ID, Key: "wd9-sg-" + suffix, Content: "wd9 shared secret " + suffix,
	})
	for _, it := range readMemoryMCP(t, client, mcpURL, tokA, wsA.ID) {
		if it.ID == m.ID {
			t.Fatalf("unshared authored row visible before the share flip: %+v", it)
		}
	}

	shared, code := shareMemory(t, client, apiURL, session, csrf, m.ID)
	if code != http.StatusOK {
		t.Fatalf("share memory: status %d, want 200", code)
	}
	if !shared.AgentVisible {
		t.Fatal("shared memory view has agentVisible=false, want true")
	}
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.shared", 30*time.Second)
	if n := countTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.shared"); n != 1 {
		t.Fatalf("memory.shared count for %s = %d, want exactly 1", m.ID, n)
	}

	var foundShared bool
	for _, it := range readMemoryMCP(t, client, mcpURL, tokA, wsA.ID) {
		if it.ID == m.ID {
			foundShared = true
			if it.Content != "wd9 shared secret "+suffix {
				t.Fatalf("shared row content = %q, want the authored content", it.Content)
			}
		}
	}
	if !foundShared {
		t.Fatalf("shared row %s not visible to memory.read after the share flip", m.ID)
	}

	unshared, code2 := unshareMemory(t, client, apiURL, session, csrf, m.ID)
	if code2 != http.StatusOK {
		t.Fatalf("unshare memory: status %d, want 200", code2)
	}
	if unshared.AgentVisible {
		t.Fatal("unshared memory view has agentVisible=true, want false")
	}
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.unshared", 30*time.Second)
	if n := countTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.unshared"); n != 1 {
		t.Fatalf("memory.unshared count for %s = %d, want exactly 1", m.ID, n)
	}
	for _, it := range readMemoryMCP(t, client, mcpURL, tokA, wsA.ID) {
		if it.ID == m.ID {
			t.Fatalf("shared-then-unshared row still visible: %+v", it)
		}
	}

	// A second row: two share calls in a row are both 200 with NO duplicate event.
	m2 := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: wsA.ID, Key: "wd9-sg2-" + suffix, Content: "idempotent share",
	})
	v1, c1 := shareMemory(t, client, apiURL, session, csrf, m2.ID)
	v2, c2 := shareMemory(t, client, apiURL, session, csrf, m2.ID)
	if c1 != http.StatusOK || c2 != http.StatusOK {
		t.Fatalf("double share: status %d/%d, want 200/200", c1, c2)
	}
	if !v1.AgentVisible || !v2.AgentVisible {
		t.Fatalf("double share views = %+v/%+v, want agentVisible=true on both", v1, v2)
	}
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+m2.ID, "memory.shared", 30*time.Second)
	if n := countTargetOutcome(t, auditURL, auditTok, "memory:"+m2.ID, "memory.shared"); n != 1 {
		t.Fatalf("memory.shared count for %s after a double share = %d, want exactly 1 (idempotent)", m2.ID, n)
	}

	t.Logf("PASS WD-9 N2 — the share flip gates agent-plane visibility (invisible -> shared+content -> unshared+invisible), each flip chained exactly once; a double share is idempotent with no duplicate event")
}

// TestWD9RevokeKillsAll — N3 (DUR-10, the strongest form, D1): grant revocation
// cascades to a 'proposed' Run (canceled, ZERO run.denied — revocation is authority
// withdrawal, not a human deny, D3) and the charter's open plan request (closed); after
// revoke, workstream.poll/plan.submit/memory.read are all refused/empty, and approving
// the now-dead-grant Run is refused (the grant-liveness check fires before the
// not-proposed check, ApproveProposed's ordering).
func TestWD9RevokeKillsAll(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	suffix := w9suffix()

	agentC := "wd9-rk-" + suffix
	tokC := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentC, workstreamResourceID)
	composerC := "agent:" + agentC
	wsC := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd9 revoke kills all " + suffix, resources: []string{workstreamResourceID},
		intervalSeconds: 5, stepBudget: 100, kind: "adaptive", composer: composerC,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, wsC.ID) })

	req1 := waitForPlanRequest(t, client, mcpURL, tokC, wsC.ID, 30*time.Second)
	stepC := map[string]string{"resourceId": workstreamResourceID, "command": "echo WD9-RK"}
	runC, statusC, isErrC, textC := submitPlanMCP(t, client, mcpURL, tokC, req1.RequestID, []map[string]string{stepC})
	if isErrC || statusC != "proposed" {
		t.Fatalf("submit before revoke failed: isErr=%v status=%q text=%q", isErrC, statusC, textC)
	}
	// A fresh open request exists at revoke time (the cascade's second victim).
	req2 := waitForFreshPlanRequest(t, client, mcpURL, tokC, wsC.ID, req1.RequestID, 30*time.Second)

	revokeGrantHTTP(t, client, apiURL, session, csrf, wsC.GrantID)

	pollTargetOutcome(t, auditURL, auditTok, "run:"+runC, "run.canceled", 30*time.Second)
	if n := countTargetOutcome(t, auditURL, auditTok, "run:"+runC, "run.denied"); n != 0 {
		t.Fatalf("run.denied count for a cascade-canceled run = %d, want ZERO (D3: revocation is not a human deny)", n)
	}
	if finalRun := getRun(t, client, apiURL, session, csrf, runC); finalRun.Status != "canceled" {
		t.Fatalf("run status after grant revoke = %q, want canceled", finalRun.Status)
	}

	wsDetail := getWorkstream(t, client, apiURL, session, csrf, wsC.ID)
	var req2Closed bool
	for _, pr := range wsDetail.PlanRequests {
		if pr.ID == req2.RequestID {
			req2Closed = true
			if pr.Status != "closed" {
				t.Fatalf("open plan request after grant revoke = %+v, want status=closed", pr)
			}
		}
	}
	if !req2Closed {
		t.Fatalf("plan request %s missing from the charter's detail history after revoke", req2.RequestID)
	}

	if reqs := pollWorkstreamMCP(t, client, mcpURL, tokC, ""); len(reqs) != 0 {
		t.Fatalf("workstream.poll after grant revoke = %+v, want empty", reqs)
	}
	if _, _, isErrSubmit, textSubmit := submitPlanMCP(t, client, mcpURL, tokC, req2.RequestID, []map[string]string{stepC}); !isErrSubmit {
		t.Fatalf("plan.submit after grant revoke succeeded (want refused): %q", textSubmit)
	}
	if items := readMemoryMCP(t, client, mcpURL, tokC, ""); len(items) != 0 {
		t.Fatalf("memory.read after grant revoke = %+v, want empty", items)
	}
	if _, code := approveRun(t, client, apiURL, session, csrf, runC); code != http.StatusForbidden {
		t.Fatalf("approve on a cascade-canceled/dead-grant run: status %d, want 403", code)
	}

	t.Logf("PASS WD-9 N3 — grant revocation killed all three agent-plane surfaces: the proposed Run was canceled (run.canceled, ZERO run.denied), the open request closed, poll/read answer empty, submit refuses, and approve is forbidden")
}

// TestWD9ExpiryNeverWedges — N4 (PLAN-1/DUR-10): an unanswered plan request expires at
// its TTL (plan.expired chained); the next tick opens a FRESH request (a different id,
// a later expiresAt) and the charter stays active — it never wedges waiting on a
// composer that never answers.
func TestWD9ExpiryNeverWedges(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)
	suffix := w9suffix()

	agentD := "wd9-exp-" + suffix
	tokD := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentD, workstreamResourceID)
	composerD := "agent:" + agentD
	wsD := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective: "wd9 expiry never wedges " + suffix, resources: []string{workstreamResourceID},
		intervalSeconds: 5, stepBudget: 100, kind: "adaptive", composer: composerD,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, wsD.ID) })

	req1 := waitForPlanRequest(t, client, mcpURL, tokD, wsD.ID, 30*time.Second)
	// Ignore it — never submit. It must expire on its own (~ one interval past the
	// claimed fire time, R1) rather than wedge the charter.
	pollTargetOutcome(t, auditURL, auditTok, "plan-request:"+req1.RequestID, "plan.expired", 30*time.Second)

	req2 := waitForFreshPlanRequest(t, client, mcpURL, tokD, wsD.ID, req1.RequestID, 30*time.Second)
	if req2.RequestID == req1.RequestID {
		t.Fatal("the next tick's request reused the expired request's id")
	}
	if !req2.ExpiresAt.After(req1.ExpiresAt) {
		t.Fatalf("fresh request expiresAt = %s, want it after the expired request's %s", req2.ExpiresAt, req1.ExpiresAt)
	}
	if wsAfter := getWorkstream(t, client, apiURL, session, csrf, wsD.ID); wsAfter.Status != "active" {
		t.Fatalf("charter status after an expired request = %q, want active", wsAfter.Status)
	}

	t.Logf("PASS WD-9 N4 — an unanswered plan request expired (plan.expired chained) and the next tick opened a fresh, later-expiring request; the charter never wedged")
}
