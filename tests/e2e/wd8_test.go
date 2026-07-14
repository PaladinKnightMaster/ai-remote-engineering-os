//go:build e2e

// WD-8 — the v0.8.0 release gate (T-083, docs/releases/v0.8.0 §2): on the real stack
// the platform REMEMBERS and FORGETS under governance. The reference-based accrual
// core rides the SAME shared once-only terminal flip every initiator already commits
// through (F-a), so this capstone adds no new orchestration surface — it proves the
// memory projection that now sits beside the immutable chain.
//
// Happy path (this file, T-083a): a chartered Workstream fires unattended and its
// completed Run accrues a content-free memory pointer with nobody writing it (leg
// 1-2); an operator authors free-text memory on the same scope and reads it back
// (leg 3-4); redaction tombstones it (leg 5); the expiry sweep (a 2s e2e-only cadence,
// AIREOS_MEMORY_SWEEP_SECONDS) tombstones an unpinned row and spares a pinned one
// across further ticks (leg 6). Facet 7 (chain hash-intactness) is the suite's
// trailing `audit-service verify` — not re-asserted here.
//
// The negatives (N1 redaction idempotency, N2 the agent plane, N3 owner-scope, N4
// terminal-outcome completeness) are wd8_negatives_test.go (T-083b). WD-5/6/7 stay
// green in the suite as the elevated regression guard (the accrual hook rides their
// shared terminal-flip seam unchanged).
package e2e

import (
	"net/http"
	"testing"
	"time"
)

// TestWD8HappyPath — WD-8 §2 legs 1-6 (facet 7 is the suite's trailing chain verify).
func TestWD8HappyPath(t *testing.T) {
	apiURL := env("E2E_API_URL", "http://api:8080")
	auditURL := env("E2E_AUDIT_URL", "http://audit:8081")
	client := &http.Client{Timeout: 60 * time.Second}
	session, csrf, status, err := doLogin(client, apiURL)
	if err != nil || status != http.StatusOK {
		t.Fatalf("login: status=%d err=%v", status, err)
	}
	auditTok := mintAuditToken(t)

	// --- Leg 1-2: governed work accrues memory with nobody writing it -----------
	ws := charterWorkstream(t, client, apiURL, session, csrf, charterOpts{
		objective:       "wd8 accrual",
		resources:       []string{workstreamResourceID},
		steps:           []charterStep{{workstreamResourceID, "echo WD8-TICK"}},
		intervalSeconds: 5,
		stepBudget:      100,
	})
	t.Cleanup(func() { retireWorkstream(t, client, apiURL, session, csrf, ws.ID) })

	completed := waitRunsForGrant(t, client, apiURL, session, csrf, ws.GrantID, "completed", 1, 60*time.Second)
	// Retire immediately: the later legs (author/redact/expiry) run on the SAME
	// workstream scope and don't need the ticker still firing — stopping it now keeps
	// the scope's row set legible instead of racing more accruals underneath us.
	retireWorkstream(t, client, apiURL, session, csrf, ws.ID)

	for _, r := range completed {
		items := listMemories(t, client, apiURL, session, csrf, "workstream", ws.ID)
		var matches []memoryView
		for _, m := range items {
			if m.Ref == "run:"+r.ID {
				matches = append(matches, m)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("run %s: %d accrued memory rows with ref=run:%s, want exactly 1", r.ID, len(matches), r.ID)
		}
		m := matches[0]
		if m.HasContent {
			t.Fatalf("accrued memory %s hasContent=true, want false (content-free pointer)", m.ID)
		}
		wantTrace := "run:" + r.ID + ":completed"
		if m.SourceTrace != wantTrace {
			t.Fatalf("accrued memory %s sourceTrace = %q, want %q", m.ID, m.SourceTrace, wantTrace)
		}
		written := pollTargetOutcome(t, auditURL, auditTok, "memory:"+m.ID, "memory.written", 30*time.Second)
		if written.Actor != "workstream:"+ws.ID {
			t.Fatalf("memory.written actor = %q, want workstream:%s", written.Actor, ws.ID)
		}
	}
	t.Logf("PASS WD-8 leg 1-2 — chartered workstream %s fired unattended; each completed run accrued exactly one content-free memory pointer (actor=workstream, principal=operator)", ws.ID)

	// --- Leg 3: an operator authors a memory on the workstream scope ------------
	authorKey := "wd8-note-" + ws.ID[:8]
	authored := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: authorKey, Content: "wd8 authored note",
	})
	if !authored.HasContent || authored.Content == nil || *authored.Content != "wd8 authored note" {
		t.Fatalf("authored memory = %+v, want hasContent=true content=%q", authored, "wd8 authored note")
	}
	authoredWritten := pollTargetOutcome(t, auditURL, auditTok, "memory:"+authored.ID, "memory.written", 30*time.Second)
	if authoredWritten.Actor != "operator:admin" {
		t.Fatalf("authored memory.written actor = %q, want operator:admin", authoredWritten.Actor)
	}
	t.Logf("PASS WD-8 leg 3 — operator-authored memory %s (actor=operator, hasContent=true)", authored.ID)

	// --- Leg 4: a scoped read round-trips the sealed content ---------------------
	roundTrip := waitForMemory(t, client, apiURL, session, csrf, "workstream", ws.ID, 15*time.Second, func(m memoryView) bool {
		return m.ID == authored.ID
	})
	if !roundTrip.HasContent || roundTrip.Content == nil || *roundTrip.Content != "wd8 authored note" {
		t.Fatalf("scoped read of authored memory = %+v, want the content to round-trip", roundTrip)
	}
	t.Logf("PASS WD-8 leg 4 — scoped GET /v1/memories round-trips the authored content")

	// --- Leg 5: redaction is governed forgetting ---------------------------------
	redacted, code := redactMemory(t, client, apiURL, session, csrf, authored.ID)
	if code != http.StatusOK {
		t.Fatalf("redact: status %d, want 200", code)
	}
	if redacted.RedactedAt == nil {
		t.Fatal("redacted memory has no redactedAt")
	}
	if redacted.Content != nil {
		t.Fatalf("redacted memory still carries content: %+v", redacted)
	}
	redactEv := pollTargetOutcome(t, auditURL, auditTok, "memory:"+authored.ID, "memory.redacted", 30*time.Second)
	if redactEv.Actor != "operator:admin" {
		t.Fatalf("memory.redacted actor = %q, want operator:admin", redactEv.Actor)
	}
	var redactedCount int
	for _, e := range listAllEvents(t, auditURL, auditTok) {
		if e.Target == "memory:"+authored.ID && e.Outcome == "memory.redacted" {
			redactedCount++
		}
	}
	if redactedCount != 1 {
		t.Fatalf("memory.redacted count for %s = %d, want 1", authored.ID, redactedCount)
	}
	t.Logf("PASS WD-8 leg 5 — redact tombstones the memory (redactedAt set, content erased, never returned again); memory.redacted chained once")

	// --- Leg 6: expiry and pinning -------------------------------------------------
	pinKey := "wd8-pin-" + ws.ID[:8]
	expireKey := "wd8-expire-" + ws.ID[:8]
	pinned := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: pinKey, Content: "pin me",
	})
	pinMemory(t, client, apiURL, session, csrf, pinned.ID)
	pinnedEv := pollTargetOutcome(t, auditURL, auditTok, "memory:"+pinned.ID, "memory.pinned", 30*time.Second)
	if pinnedEv.Actor != "operator:admin" {
		t.Fatalf("memory.pinned actor = %q, want operator:admin", pinnedEv.Actor)
	}

	expiresAt := time.Now().Add(3 * time.Second)
	expiring := createMemory(t, client, apiURL, session, csrf, memoryCreateInput{
		ScopeType: "workstream", ScopeID: ws.ID, Key: expireKey, Content: "expire me", ExpiresAt: &expiresAt,
	})

	// The sweep (the e2e-only 2s cadence override) tombstones the unpinned row.
	expired := waitForMemory(t, client, apiURL, session, csrf, "workstream", ws.ID, 20*time.Second, func(m memoryView) bool {
		return m.ID == expiring.ID && m.RedactedAt != nil
	})
	if expired.Content != nil {
		t.Fatalf("expired memory still carries content: %+v", expired)
	}
	var expiredCount int
	for _, e := range listAllEvents(t, auditURL, auditTok) {
		if e.Target == "memory:"+expiring.ID && e.Outcome == "memory.expired" {
			expiredCount++
		}
	}
	if expiredCount != 1 {
		t.Fatalf("memory.expired count for %s = %d, want 1", expiring.ID, expiredCount)
	}

	// Hold >= 2 more sweep ticks: the pinned row must stay untouched.
	time.Sleep(5 * time.Second)
	stillPinned := waitForMemory(t, client, apiURL, session, csrf, "workstream", ws.ID, 5*time.Second, func(m memoryView) bool {
		return m.ID == pinned.ID
	})
	if stillPinned.RedactedAt != nil {
		t.Fatalf("pinned memory was expired: %+v, want it spared", stillPinned)
	}
	for _, e := range listAllEvents(t, auditURL, auditTok) {
		if e.Target == "memory:"+pinned.ID && e.Outcome == "memory.expired" {
			t.Fatalf("pinned memory %s has a memory.expired event — pin did not exempt it from the sweep", pinned.ID)
		}
	}
	t.Logf("PASS WD-8 leg 6 — the sweep expired the unpinned row exactly once (memory.expired) and spared the pinned row across >=2 further sweep ticks")

	t.Logf("PASS WD-8 happy path — accrue -> read -> author -> redact -> expire/pin, every governance act chained content-free (facet 7's chain-intact proof is the suite's trailing audit-service verify)")
}
