//go:build e2e

// WD-10 — the v0.10.0 release gate (T-101, docs/releases/v0.10.0 §2): on the real
// stack the composer plans grounded in a fresh, governed index of the operator's
// project context (Layer 1, ADR-0066) ALONGSIDE the memory it already reads (Layer 2,
// v0.9) — a human reviews the exact plan before anything runs, the existing engine
// executes it, the outcome accrues, and a live source edit is provably reflected on
// the next read without a full rebuild. This is the read plane's second half:
// remember (L2) + know the current context (L1) -> compose -> govern -> act -> accrue.
//
// This file (T-101a) covers the happy path (legs 1-8, criteria §2). The six live
// negatives are wd10_negatives_test.go (T-101b). WD-5/6/7/8/9 stay green in the
// elevated regression suite (unchanged files, unmodified by this ticket) — the
// composer now ALSO reads context, byte-preserving the v0.9 loop exactly.
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// wd10IntervalSeconds is the adaptive charter's tick interval, and therefore (per
// ticker.go's fireAdaptive: expiresAt = NextFireAt + IntervalSeconds) the plan
// request's TTL (T-127a diagnosis). Leg 4 alone can burn up to ~60s in the worst case
// (two sequential 30s poll budgets: pollActorOutcomeCount for memory.read, then
// pollActorOutcomeCount for context.read) — a TTL that only just covers that gives no
// real headroom. 60s (2x a single 30s poll budget) clears the typical case (these
// polls resolve in low single-digit seconds via the outbox relay; 5s was too tight
// only because it left ~0 slack for ordinary jitter, not because the real work takes
// anywhere near 30s) with comfortable margin.
//
// NOTE (T-127a, caught live re-running this fix): store.go's Create sets the VERY
// FIRST next_fire_at to now()+intervalSeconds (there is no immediate first fire for a
// freshly chartered charter) — so raising the interval also pushes out how long leg 3
// must wait for the FIRST plan request to appear. leg 3's own poll window below is
// sized off this same constant for exactly that reason; a naive interval bump without
// widening leg 3's wait reintroduces a NEW race (leg 3 timing out before the first
// tick ever fires) instead of fixing the original one.
const wd10IntervalSeconds = 60

// TestWD10HappyPath — WD-10 §2 legs 1-8.
func TestWD10HappyPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	mcpURL := env("E2E_MCP_URL", "http://mcp-gateway:8085")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// --- Leg 1: the operator charters an adaptive Workstream and registers a bound
	// context source over a real directory (GOV-11 consent in frame) --------------
	suffix := w9suffix()
	agentID := "wd10-composer-" + suffix
	tok := mintAgentTokenViaAPI(t, client, apiURL, session, csrf, agentID, workstreamResourceID)
	composer := "agent:" + agentID

	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd10 context loop " + suffix,
		resources:       []string{workstreamResourceID},
		intervalSeconds: wd10IntervalSeconds, stepBudget: 100, kind: "adaptive", composer: composer,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	dir := wd10ScratchDir(t)
	term1 := wd10Token()
	writeContextDoc(t, dir, "runbook.md", term1)

	src := registerContextSource(t, client, apiURL, session, csrf, dir, ws.ID)
	if src.ScopeType != "workstream" || src.ScopeID != ws.ID {
		t.Fatalf("context source scope = %s/%s, want workstream/%s", src.ScopeType, src.ScopeID, ws.ID)
	}
	if src.LastIndexedAt != nil {
		t.Fatalf("freshly registered source lastIndexedAt = %v, want nil (not yet swept)", src.LastIndexedAt)
	}
	registered := pollTargetOutcome(t, auditURL, auditTok, "context-source:"+src.ID, "context.source.registered", 30*time.Second)
	if registered.Actor != "operator:admin" || registered.Edge != "human-agent" {
		t.Fatalf("context.source.registered = {actor:%q edge:%q}, want {operator:admin human-agent}", registered.Actor, registered.Edge)
	}
	if registered.EvidencePacketID != "workstream:"+ws.ID {
		t.Fatalf("context.source.registered evidencePacketId = %q, want workstream:%s", registered.EvidencePacketID, ws.ID)
	}
	t.Logf("PASS WD-10 leg 1 — context source %s registered over a real directory, bound to charter %s; context.source.registered chained (human-agent)", src.ID, ws.ID)

	// --- Leg 2: the indexer builds the index — deterministically ----------------------
	indexed := waitForSourceIndexed(t, client, apiURL, session, csrf, ws.ID, src.ID, 30*time.Second)
	if indexed.Status != "active" {
		t.Fatalf("indexed source status = %q, want active", indexed.Status)
	}
	waitForSearchHit(t, client, mcpURL, tok, term1, ws.ID, term1, 30*time.Second)
	t.Logf("PASS WD-10 leg 2 — the in-process indexer swept the source (native PG FTS, no network, no embedding); lastIndexedAt is set (the up-to-date staleness signal), status active")

	// --- Leg 3: the tick publishes (v0.9 machinery, unchanged) ------------------------
	// The wait budget is sized off wd10IntervalSeconds, not a bare 30s: the charter's
	// FIRST next_fire_at is set at creation to now()+intervalSeconds (store.go's
	// Create), so the first plan request cannot appear before roughly one interval has
	// elapsed. +30s covers legs 1-2's own (usually sub-10s) time plus scheduler jitter
	// (the ticker scans every 2s, ticker.go's NewTicker).
	req1 := waitForPlanRequest(t, client, mcpURL, tok, ws.ID, time.Duration(wd10IntervalSeconds)*time.Second+30*time.Second)
	t.Logf("PASS WD-10 leg 3 — the adaptive tick opened plan request %s", req1.RequestID)

	// --- Leg 4: the composer reads memory AND context — both chained -----------------
	_ = readMemoryMCP(t, client, mcpURL, tok, ws.ID)
	memReads := pollActorOutcomeCount(t, auditURL, auditTok, composer, "memory.read", 1, 30*time.Second)
	if len(memReads) != 1 {
		t.Fatalf("memory.read count for composer %s = %d, want exactly 1", composer, len(memReads))
	}

	// T-110 flake diagnosis: leg 2's waitForSearchHit already polled context.search
	// under this SAME composer actor while it waited for the sweep to surface term1
	// (SearchForAgent chains a context.read on every call, hit or miss — store.go's
	// "an empty-result search must still chain its own context.read event" N4 rule).
	// The original "== 1" assertion here counted ALL context.read events for the
	// composer since the actor was first used, so it silently depended on leg 2
	// happening to settle in exactly one poll iteration before this call ran — a
	// harness race (outbox-relay visibility), not a chain defect: baseline against the
	// count immediately before THIS call and assert exactly one NEW event, which is
	// the actual SEC-33 invariant under test ("exactly one context.read per
	// context.search call").
	ctxReadBaseline := countActorOutcome(t, auditURL, auditTok, composer, "context.read")
	chunks := searchContextMCP(t, client, mcpURL, tok, term1, ws.ID)
	if len(chunks) == 0 {
		t.Fatalf("context.search(%q) returned zero chunks", term1)
	}
	var hit contextChunkView
	var sawTerm bool
	for _, c := range chunks {
		if strings.Contains(c.Content, term1) {
			hit = c
			sawTerm = true
			break
		}
	}
	if !sawTerm {
		t.Fatalf("context.search(%q) chunks = %+v, none carried the term", term1, chunks)
	}
	ctxReads := pollActorOutcomeCount(t, auditURL, auditTok, composer, "context.read", ctxReadBaseline+1, 30*time.Second)
	if len(ctxReads) != ctxReadBaseline+1 {
		t.Fatalf("context.read count for composer %s = %d, want exactly %d (baseline %d before this call + the one event this call chains)", composer, len(ctxReads), ctxReadBaseline+1, ctxReadBaseline)
	}
	if ctxReads[0].Edge != "agent-workspace" {
		t.Fatalf("context.read edge = %q, want agent-workspace (SEC-33)", ctxReads[0].Edge)
	}
	if !strings.HasPrefix(ctxReads[0].Target, "context:") {
		t.Fatalf("context.read target = %q, want it to start with context:", ctxReads[0].Target)
	}
	if !strings.Contains(ctxReads[0].Target, hit.ChunkID) {
		t.Fatalf("context.read target = %q, want it to list the returned chunk id %s", ctxReads[0].Target, hit.ChunkID)
	}
	if strings.Contains(ctxReads[0].Target, term1) || strings.Contains(ctxReads[0].EvidencePacketID, term1) {
		t.Fatalf("context.read event leaked the query/content term (content-free violation): %+v", ctxReads[0])
	}
	if ctxReads[0].EvidencePacketID != "workstream:"+ws.ID {
		t.Fatalf("context.read evidencePacketId = %q, want workstream:%s", ctxReads[0].EvidencePacketID, ws.ID)
	}
	t.Logf("PASS WD-10 leg 4 — the composer read memory.read (chained once) AND context.search (chained exactly once, content-free, edge=agent-workspace, target lists the chunk id only)")

	// --- Leg 5: the composer composes grounded in fresh context and submits ----------
	step := map[string]string{"resourceId": workstreamResourceID, "command": "echo WD10-CONTEXT-" + term1}
	runID, planStatus, isErr, text := submitPlanMCP(t, client, mcpURL, tok, req1.RequestID, []map[string]string{step})
	if isErr {
		// T-127a: plan.submit's ONE refusal shape ("the grant does not authorize this
		// agent", adaptive_internal.go's submitPlanRefusalReason) is deliberately
		// identical whether req1 is genuinely foreign/unauthorized OR merely no longer
		// open (ADR-0065 §6 no-oracle: a foreign agent must not be able to tell "denied"
		// from "unknown/expired" from the wire). That ambiguity is correct on the wire —
		// but the operator's audit chain is NOT blinded by it: every expiry chains its
		// own plan.expired event (store.go's expirePlanRequestsDue) targeting
		// "plan-request:<id>". Ask the chain, not the wire, which case this is.
		if countTargetOutcome(t, auditURL, auditTok, "plan-request:"+req1.RequestID, "plan.expired") > 0 {
			t.Fatalf("plan.submit refused %q — this is a HARNESS TIMING race, not an authorization bug: "+
				"plan-request %s's TTL (= this charter's intervalSeconds=%d, ticker.go's fireAdaptive sets "+
				"expiresAt = NextFireAt + IntervalSeconds) expired before leg 4's polling landed the submit; "+
				"the chain confirms a plan.expired event on plan-request:%s. If this recurs, widen "+
				"wd10IntervalSeconds further — do not treat this failure as a grant-enforcement regression.",
				text, req1.RequestID, wd10IntervalSeconds, req1.RequestID)
		}
		t.Fatalf("plan.submit refused (want success, and NO plan.expired event found on plan-request:%s — "+
			"this is a genuine authorization regression, not a TTL race): %q", req1.RequestID, text)
	}
	if planStatus != "proposed" {
		t.Fatalf("plan.submit run status = %q, want proposed", planStatus)
	}
	pollTargetOutcome(t, auditURL, auditTok, "plan-request:"+req1.RequestID, "plan.submitted", 30*time.Second)
	t.Logf("PASS WD-10 leg 5 — plan.submit created proposed run %s, its step grounded in the retrieved chunk's term; plan.submitted chained", runID)

	// --- Leg 6: a human reviews the exact plan, then approves -------------------------
	preview := getRun(t, client, apiURL, session, csrf, runID)
	if preview.Status != "proposed" {
		t.Fatalf("preview run status = %q, want proposed", preview.Status)
	}
	if len(preview.Steps) != 1 || preview.Steps[0].Command != step["command"] || preview.Steps[0].ResourceID != workstreamResourceID {
		t.Fatalf("preview steps = %+v, want one step {resourceId:%s command:%s}", preview.Steps, workstreamResourceID, step["command"])
	}
	approved, code := approveRun(t, client, apiURL, session, csrf, runID)
	if code != http.StatusOK {
		t.Fatalf("approve run: status %d, want 200", code)
	}
	if approved.Status != "pending" {
		t.Fatalf("approved run status = %q, want pending", approved.Status)
	}
	t.Logf("PASS WD-10 leg 6 — the operator reviewed the exact sealed command (grounded in the indexed context) and approved; proposed->pending")

	// --- Leg 7: the existing engine drives it and accrues -----------------------------
	final := waitRunStatusHTTP(t, client, apiURL, session, csrf, runID, "completed", 60*time.Second)
	if final.Actor != composer || final.GrantID != ws.GrantID {
		t.Fatalf("completed run provenance = {actor:%q grant:%q}, want {%s %s}", final.Actor, final.GrantID, composer, ws.GrantID)
	}
	accrued := waitForMemory(t, client, apiURL, session, csrf, "run", runID, 15*time.Second, func(m memoryView) bool {
		return m.Ref == "run:"+runID
	})
	if accrued.HasContent {
		t.Fatalf("accrued memory %s hasContent=true, want false (content-free pointer)", accrued.ID)
	}
	pollTargetOutcome(t, auditURL, auditTok, "memory:"+accrued.ID, "memory.written", 30*time.Second)
	t.Logf("PASS WD-10 leg 7 — the unchanged sequencer drove the approved plan to completed; the terminal Run accrued its content-free pointer (memory.written chained)")

	// --- Leg 8: freshness proves — and the chain proves it ----------------------------
	term2 := wd10Token()
	writeContextDoc(t, dir, "runbook.md", term2) // in-place edit: term1 -> term2 (same file, no new registration, no rebuild call)
	waitForSearchHit(t, client, mcpURL, tok, term2, ws.ID, term2, 30*time.Second)
	if hits := searchContextMCP(t, client, mcpURL, tok, term1, ws.ID); len(hits) != 0 {
		for _, h := range hits {
			if strings.Contains(h.Content, term1) {
				t.Fatalf("context.search(%q) still returned the pre-edit term after the file was overwritten: %+v", term1, h)
			}
		}
	}
	t.Logf("PASS WD-10 leg 8 — a live source edit (term1->term2) was reflected on the very next context.search with no explicit rebuild call; the loop is content-free end-to-end: context.source.registered -> context.read (beside memory.read) -> plan.submitted -> the proposed-Run resolution -> run.* -> memory.written (audit-service verify runs chain-intact at the suite's end)")
}
