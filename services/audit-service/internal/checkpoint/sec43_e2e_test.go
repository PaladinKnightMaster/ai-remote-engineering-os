//go:build e2e && sec43

// sec43_e2e_test.go is the T-127b SEC-43/VER-16 G1 capstone: it proves the
// negative test ADR-0069 §Security posture pins as binding — a forged audit
// chain, repaired end to end so a pure ordering/hash walk sees nothing
// wrong, is still caught by BOTH real verifiers (the DB-backed
// `audit-service verify` operator CLI and the offline, third-party
// `aireos-verify` binary, T-126b).
//
// # Why this file requires "sec43" STACKED ON "e2e", not "e2e" alone
//
// This test DESTROYS the live chain's integrity in place — after it runs,
// nothing else in this suite can assume an intact chain again. Had it lived
// inside checkpoint_e2e_test.go, or carried "e2e" alone, `go test -tags e2e
// ./services/audit-service/internal/checkpoint/...`
// (docker-compose.e2e.yml's tester command, its SECOND step) would pick it
// up incidentally alongside TestLiveChainHeadSweepSignsRealHeads /
// TestLiveExportIsIndependentlyVerifiable / TestWD126GAireosVerifyBinaryProvesAllThreeLegs,
// and whichever of those Go happened to schedule AFTER it would fail against
// a chain THIS test tampered — an order-dependent failure with no test-name
// signal pointing at the actual cause. Requiring "sec43" on top of "e2e"
// makes that structurally impossible: this file only compiles into a build
// that explicitly asks for it. docker-compose.e2e.yml's tester command runs
// it as its OWN, EXPLICITLY-SEQUENCED FINAL STEP — `-tags e2e,sec43 -run
// TestSEC43...` — strictly after every other assertion in the suite,
// including the T-127a INTACT+ATTESTED capstone. A naming convention
// ("runs last because it sorts last") was rejected as one future test away
// from breaking (T-127b brief); a build-tag-plus--run gate cannot drift that
// way by accident.
//
// # The two legs (ADR-0069 §Security posture, binding on this ticket)
//
// LEG A proves the precondition is real, not asserted: with
// audit_events_no_mutate ARMED, an UPDATE against audit_events — issued over
// the SAME `cockpit` connection the live services use, the role that OWNS
// the table (ADR-0069 §Context's corrected threat statement) — is rejected.
// The identical statement is then shown succeeding once the trigger is
// disabled, confirming the trigger (not some unrelated permission) is what
// blocked it. This is what makes "the attack needs owner access, not mere
// write access" something this ticket RAN, not something inferred from
// reading the migration.
//
// LEG B proves the forgery is caught anyway, after clearing that
// precondition: disable the trigger (itself evidence — an owner-privilege
// action the migration itself is never touched by), force a checkpoint and
// note its seq, tamper one event's actor field STRICTLY BELOW that signed
// head, then REPAIR the chain forward from the tamper point using
// chain.Compute — updating every subsequent row's prev_hash/chain_hash —
// so a bare chain.Verify walk (ordering + hash recompute, no signatures)
// reports INTACT. That "intact" result is the vacuous-looking green
// ADR-0069's test-design constraint warns about; asserting it here is not
// the proof, it is the SETUP the proof needs in order to mean anything.
// What actually catches the forgery is the signature: both `audit-service
// verify` (DB-backed, ADR-0069 D6's two-step proof) and `aireos-verify` (the
// offline third-party binary) are built and run as REAL SUBPROCESSES — never
// simulated by calling internal/verify.Chain in-test — first against the
// clean pre-tamper state (must PASS, VER-16's "shown passing on the
// untampered input"), then again against the tampered state (must FAIL and
// name the break).
//
// Neither migrations/0001_init.up.sql nor internal/chain, internal/verify,
// internal/export or internal/anchor are touched by this ticket — this file
// only calls chain.Compute/chain.Verify and the export.Exporter exactly as
// every other test in this package already does.
package checkpoint

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/services/audit-service/internal/anchor"
	"aireos.local/services/audit-service/internal/audit"
	"aireos.local/services/audit-service/internal/chain"
	"aireos.local/services/audit-service/internal/export"
	"aireos.local/services/audit-service/internal/store"
)

func TestSEC43ForgedAuditChainCaughtByBothVerifiers(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}

	sk, err := chain.SecretKeyFromHex(chainSignKeyHex(t))
	if err != nil {
		t.Fatalf("parse fixture secret key: %v", err)
	}
	pub, ok := sk.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("fixture key has no ed25519 public half")
	}
	pubHex := hex.EncodeToString(pub)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	defer pool.Close()
	st := store.New(pool)

	anchorPath := os.Getenv("AIREOS_AUDIT_ANCHOR_PATH")
	if anchorPath == "" {
		t.Fatal("AIREOS_AUDIT_ANCHOR_PATH not set — docker-compose.e2e.yml must configure the tester with the same anchor path as the audit service (ADR-0069 D4)")
	}

	// Build BOTH real binaries once, reused for the before/after checks on
	// both legs — this ticket's whole point is that "the tool a third party
	// runs still caught it", so neither verifier may be simulated by a
	// direct in-process call into the packages they wrap.
	auditBin := filepath.Join(t.TempDir(), "audit-service")
	buildAudit := exec.Command("go", "build", "-o", auditBin, "aireos.local/services/audit-service")
	buildAudit.Stdout = os.Stdout
	buildAudit.Stderr = os.Stderr
	if err := buildAudit.Run(); err != nil {
		t.Fatalf("build audit-service: %v", err)
	}
	verifyBin := filepath.Join(t.TempDir(), "aireos-verify")
	buildVerify := exec.Command("go", "build", "-o", verifyBin, "aireos.local/services/audit-service/cmd/aireos-verify")
	buildVerify.Stdout = os.Stdout
	buildVerify.Stderr = os.Stderr
	if err := buildVerify.Run(); err != nil {
		t.Fatalf("build aireos-verify: %v", err)
	}

	// ---- establish a clean, fully-attested baseline: force checkpoints
	// until the signed head covers the whole chain (same retry idiom
	// TestWD126GAireosVerifyBinaryProvesAllThreeLegs uses, for the same
	// reason — the live audit-service's own cadence sweep may not yet have
	// caught up to the very last events this suite generated). ----
	sw := &Sweeper{Store: st, Key: sk, Anchor: anchor.New(anchorPath)}
	var head chain.Head
	var events []audit.Event
	established := false
	for attempt := 1; attempt <= 8; attempt++ {
		if _, err := sw.Checkpoint(ctx); err != nil {
			t.Fatalf("force checkpoint (attempt %d): %v", attempt, err)
		}
		h, ok, err := st.LastHead(ctx)
		if err != nil {
			t.Fatalf("last head (attempt %d): %v", attempt, err)
		}
		if !ok {
			t.Fatal("no chain head exists yet — nothing for SEC-43 to attest around")
		}
		events = listWholeChain(t, ctx, st)
		if len(events) == 0 {
			t.Fatal("audit_events is empty — nothing for SEC-43 to tamper")
		}
		if h.Seq == events[len(events)-1].Seq {
			head = h
			established = true
			break
		}
		t.Logf("attempt %d: head at seq %d does not yet cover the tip at seq %d, retrying", attempt, h.Seq, events[len(events)-1].Seq)
		time.Sleep(1 * time.Second)
	}
	if !established {
		t.Fatal("could not establish a fully-attested baseline after 8 forced-checkpoint retries — SEC-43 cannot proceed without one")
	}
	t.Logf("SEC-43 baseline: signed head at seq %d covers the whole live chain (%d events)", head.Seq, len(events))

	// ---- pre-tamper positive control (VER-16): both verifiers must PASS on
	// the untampered chain, or their later failure would prove nothing. ----
	preVerifyOut, preVerifyCode := runBinary(t, auditBin, "verify")
	if preVerifyCode != 0 || !strings.Contains(preVerifyOut, "INTACT+ATTESTED") {
		t.Fatalf("pre-tamper baseline: `audit-service verify` did not report a clean INTACT+ATTESTED pass (exit %d):\n%s", preVerifyCode, preVerifyOut)
	}
	t.Logf("pre-tamper control PASSED: audit-service verify — exit 0: %s", strings.TrimSpace(preVerifyOut))

	cleanPath := filepath.Join(t.TempDir(), "clean.ndjson")
	if err := os.WriteFile(cleanPath, exportNow(t, ctx, st, anchorPath), 0o600); err != nil {
		t.Fatalf("write clean export: %v", err)
	}
	preAVOut, preAVCode := runBinary(t, verifyBin, "--pubkey", pubHex, cleanPath)
	if preAVCode != 0 || !strings.Contains(preAVOut, "INTACT+ATTESTED") {
		t.Fatalf("pre-tamper baseline: aireos-verify did not report a clean pass (exit %d):\n%s", preAVCode, preAVOut)
	}
	t.Logf("pre-tamper control PASSED: aireos-verify — exit 0: %s", strings.TrimSpace(preAVOut))

	// ---- LEG A: the trigger is a real barrier. ----
	tipSeq := events[len(events)-1].Seq
	_, err = pool.Exec(ctx, `UPDATE audit_events SET actor = $1 WHERE seq = $2`, "sec43-leg-a-should-be-rejected", tipSeq)
	if err == nil {
		t.Fatalf("LEG A FAILED: UPDATE against audit_events (seq %d) succeeded while audit_events_no_mutate should be armed — the append-only barrier did not hold", tipSeq)
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("LEG A: UPDATE was rejected, but not by the expected trigger (error: %v)", err)
	}
	t.Logf("LEG A PASSED: UPDATE against audit_events (seq %d) REJECTED while audit_events_no_mutate is armed: %v", tipSeq, err)

	// ---- LEG B, step 1: disable the trigger — itself evidence of the
	// ADR-0069 threat precondition (owner privilege, not mere write access).
	// The migration file is untouched; this disables the trigger AT RUNTIME
	// on this disposable e2e stack only. ----
	if _, err := pool.Exec(ctx, `ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_mutate`); err != nil {
		t.Fatalf("LEG B: disable audit_events_no_mutate (owner privilege): %v", err)
	}
	t.Logf("LEG B: audit_events_no_mutate DISABLED (owner privilege exercised — this step is itself the ADR-0069 precondition evidence)")

	// VER-16's "both directions" for LEG A: the SAME statement LEG A ran,
	// now succeeding, confirms the trigger — not some unrelated permission —
	// was what blocked it. A no-op value assignment, so it changes nothing
	// the hash covers.
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET actor = actor WHERE seq = $1`, tipSeq); err != nil {
		t.Fatalf("LEG A control: the SAME UPDATE still fails after disabling the trigger (%v) — LEG A's earlier rejection was not actually caused by audit_events_no_mutate", err)
	}
	t.Logf("LEG A control PASSED: the identical UPDATE now succeeds with the trigger disabled — confirms LEG A's rejection was the trigger, not an unrelated permission")

	// ---- LEG B, step 2: tamper an event STRICTLY BELOW the signed head —
	// the test-design constraint ADR-0069 §Security posture pins as binding.
	// events[0] is the lowest seq in the whole chain. The check below is
	// STRICT (>=, not >): equal-to-the-head is the head event itself, not
	// below it, and tampering it (or anything above it) would make this a
	// vacuous proof (ADR-0069 §Security posture) — only a degenerate
	// single-event chain could ever trip this, which the e2e suite's real
	// traffic volume never produces, but the check stays strict regardless.
	target := events[0]
	if target.Seq >= head.Seq {
		t.Fatalf("test precondition violated: target seq %d is not STRICTLY BELOW signed head seq %d — tampering at or above the head would make this a vacuous proof (ADR-0069 §Security posture)", target.Seq, head.Seq)
	}
	forgedActor := target.Actor + "-SEC43-FORGED-BELOW-HEAD"
	if _, err := pool.Exec(ctx, `UPDATE audit_events SET actor = $1 WHERE seq = $2`, forgedActor, target.Seq); err != nil {
		t.Fatalf("tamper event at seq %d: %v", target.Seq, err)
	}
	t.Logf("LEG B: tampered actor at seq %d (signed head covers through seq %d) — actor %q -> %q", target.Seq, head.Seq, target.Actor, forgedActor)

	// ---- LEG B, step 3: repair the chain forward using chain.Compute —
	// exactly the attack ADR-0069 describes: an owner rewrites history AND
	// recomputes the chain around the edit so a pure ordering walk is fully
	// satisfied. ----
	prev := chain.Genesis
	for i, e := range events {
		if i == 0 {
			e.Actor = forgedActor
		}
		newHash := chain.Compute(prev, e)
		if _, err := pool.Exec(ctx, `UPDATE audit_events SET prev_hash = $1, chain_hash = $2 WHERE seq = $3`, prev, newHash, e.Seq); err != nil {
			t.Fatalf("repair chain at seq %d: %v", e.Seq, err)
		}
		prev = newHash
	}
	t.Logf("LEG B: chain repaired forward from seq %d through seq %d using chain.Compute — every prev_hash/chain_hash after the tamper point rewritten to match", target.Seq, events[len(events)-1].Seq)

	// ---- LEG B, step 4: prove the forgery is INVISIBLE to a pure
	// ordering/hash walk — the vacuous-looking green ADR-0069's test-design
	// constraint warns about is the SETUP this proof needs, not the proof
	// itself. If this assertion does not hold, the test below proves
	// nothing and must be treated as a failure of THIS test's design. ----
	eventsAfter := listWholeChain(t, ctx, st)
	if idx := chain.Verify(eventsAfter); idx != -1 {
		t.Fatalf("test setup FAILED: chain.Verify reports broken at index %d — the repair did not fully satisfy the ordering walk, so this test cannot prove what it claims (ADR-0069 §Security posture's vacuous-test warning)", idx)
	}
	t.Logf("LEG B: chain.Verify (ordering + hash recompute ONLY, no signatures) returns INTACT on the forged chain — %d events, the forgery is invisible to that check alone", len(eventsAfter))

	// ---- LEG B, step 5: BOTH real verifiers still catch it. ----
	postVerifyOut, postVerifyCode := runBinary(t, auditBin, "verify")
	if postVerifyCode == 0 {
		t.Fatalf("LEG B FAILED: audit-service verify exited 0 against the forged chain:\n%s", postVerifyOut)
	}
	if !strings.Contains(postVerifyOut, "BROKEN") {
		t.Fatalf("LEG B FAILED: audit-service verify did not report BROKEN (exit %d):\n%s", postVerifyCode, postVerifyOut)
	}
	t.Logf("LEG B PASSED: audit-service verify (DB-backed) reports BROKEN, exit %d: %s", postVerifyCode, strings.TrimSpace(postVerifyOut))

	tamperedPath := filepath.Join(t.TempDir(), "tampered.ndjson")
	if err := os.WriteFile(tamperedPath, exportNow(t, ctx, st, anchorPath), 0o600); err != nil {
		t.Fatalf("write tampered export: %v", err)
	}
	postAVOut, postAVCode := runBinary(t, verifyBin, "--pubkey", pubHex, tamperedPath)
	if postAVCode == 0 {
		t.Fatalf("LEG B FAILED: aireos-verify exited 0 against the tampered export:\n%s", postAVOut)
	}
	if !strings.Contains(postAVOut, "BROKEN") {
		t.Fatalf("LEG B FAILED: aireos-verify did not report BROKEN (exit %d):\n%s", postAVCode, postAVOut)
	}
	t.Logf("LEG B PASSED: aireos-verify (offline, third-party, exported bytes only, NO database access) reports BROKEN, exit %d: %s", postAVCode, strings.TrimSpace(postAVOut))

	t.Logf("SEC-43 SUMMARY — signed head seq=%d, tampered event seq=%d, chain.Verify=INTACT(-1) on the forged chain, audit-service verify=BROKEN(exit %d), aireos-verify=BROKEN(exit %d)",
		head.Seq, target.Seq, postVerifyCode, postAVCode)
}

// exportNow runs the SAME internal/export.Exporter the `audit-service
// export` subcommand and this package's other G1 tests already use — never
// a second copy of the export logic — against the database's CURRENT state.
func exportNow(t *testing.T, ctx context.Context, st *store.Store, anchorPath string) []byte {
	t.Helper()
	ex := &export.Exporter{Store: st, Anchor: anchor.New(anchorPath)}
	var buf bytes.Buffer
	if err := ex.Export(ctx, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	return buf.Bytes()
}

// runBinary runs a built binary as a REAL subprocess — never a direct Go
// call into the package it wraps, which is the entire point of a
// third-party-verifiable proof (T-127b) — inheriting this test process's
// environment (DATABASE_URL / AIREOS_AUDIT_CHAIN_SIGN_KEY already ride
// docker-compose.e2e.yml's tester service env, matching what this test
// itself connected with), and returns combined stdout+stderr plus the exit
// code.
func runBinary(t *testing.T, binPath string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		return out.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return out.String(), exitErr.ExitCode()
	}
	t.Fatalf("run %s: %v (output so far:\n%s)", filepath.Base(binPath), err, out.String())
	return "", -1
}
