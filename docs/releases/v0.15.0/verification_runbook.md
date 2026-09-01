# v0.15.0 — verification runbook

**Release: "Governed Decision Policy". Gate: WD-1..11 both stacks + WD-15 + smoke + a CEO `§human` pass
(§2 of the release criteria).**

Steps 0–2 are automated and were recorded at **T-154** (capstone) and the accompanying
`v0.15.0-security-review.md`. Step 3 is **yours** — the `§human` pass (VER-32) is this release's first
real contact with the rule surface outside a test harness, and no automated run may perform it. Step 4
(sign + tag) is yours too, once Steps 1–3 read green.

---

## Step 0 — sync the RC, and two environmental rules before you run anything

```bash
git -C /d/1_PROJECT/PRIVATE_WORK/AI-Agentic-OS/ai-remote-engineering-os log --oneline -1
```

`main` and `dev` must be equal and the tree clean before the build in Step 2 — a commit landing after the
build changes every binary's `vcs.revision` and therefore the artifact hash (rebuild before signing if
anything lands).

⚠ **VER-39 — pre-warm the Go module cache BEFORE `task e2e`, every time.** `task e2e`'s own
`docker compose down -v` removes the named `gomodcache`/`gocache` volumes on exit, so the cache does not
persist across runs by itself. On a cold cache, twelve services resolving Go modules concurrently against
`proxy.golang.org` during the compose build can fail the `seed` step on cold-cache contention alone — a
`block` gate failing for network reasons, which is exactly how a real defect gets dismissed as "just the
network." Pre-warm explicitly:

```bash
docker volume create aireos-e2e_gomodcache
docker volume create aireos-e2e_gocache
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd):/work" -w /work \
  -v aireos-e2e_gomodcache:/go/pkg/mod -v aireos-e2e_gocache:/root/.cache/go-build \
  golang:1.26 bash -c '
for m in packages/go/auth packages/go/middleware packages/go/migrate packages/go/outbox \
         services/api services/audit-service services/connector-service services/mcp-gateway \
         services/policy-service services/session-broker tests/e2e \
         tools/forbidden-features-check tools/migrate-lint; do
  (cd "$m" && go mod download)
done'
```

⚠ **VER-40 — every integration leg starts from a FRESH database.** A long-lived Postgres container
silently poisons the integration suite (the T-073 lesson, recurring): `down -v` → `up` → migrate every
service to head, *every time*, before `task test-integration` or an equivalent manual run. A failure in a
package this release never touched should prompt "is the fixture dirty?" — but only *after* checking,
never as a reflex (that reflex is also how a genuine regression gets waved through).

```bash
cd infra/docker-compose
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.mvp.yml down -v
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.mvp.yml up -d
# wait for pg_isready, then migrate every service to head against the SAME db (the
# product's own runtime shape — one shared `cockpit` db across all six services):
for svc in api audit-service session-broker connector-service policy-service mcp-gateway; do
  go run ./services/$svc migrate up   # DATABASE_URL=postgres://cockpit:change-me@<host>:5432/cockpit?sslmode=disable
done
```

---

## Step 1 — the automated gates (recorded at T-154)

| Gate | What | Evidence |
|---|---|---|
| **VER-29** compose | WD-1..11 on `task e2e` | `ok aireos.local/tests/e2e`, **62 PASS / 0 FAIL**, chain `INTACT+ATTESTED through seq 709`. Includes the SEC-43 forged-chain negative proof (LEG A + LEG B both correct). |
| **VER-29** installed | WD-1..11 on `task wd1`, offline bundle + postgres companion **rebuilt from HEAD** (`1cb796e`) first | 8 systemd units `active`, Caddy serves HTTPS; **8 PASS / 0 FAIL**, chain `INTACT+ATTESTED through seq 240`; the installed-stack SEC-43 negative proof (T-127b) also PASS. |
| **VER-30** (WD-15) | A rule-denied invocation: no connector lease, no broker egress, denial chained positively | `TestWD15RuleDeniedInvocation` — see WD-15 runbook below. |
| **VER-31** | `task smoke`, extended with a rule-evaluation leg | **23 passed / 2 skipped (quickstart-a/b, unaffected by this release) / 0 failed.** Includes both `policy-rules.smoke.spec.ts` tests: the existing CRUD leg and the new leg that authors a rule against a REAL resource and watches a real "Start session" attempt get refused where it previously succeeded. |
| **CON-1** | `approval_test.go` + `invocation_approval_test.go` run **unchanged** against an empty rule table | Both ran inside the same `task e2e` invocation as VER-29/VER-30 — `TestGovernedEgressApprovalEndToEnd` and `TestGatewayGovernedExecApprovalPath` both PASS. The compose seed never writes `policy_rules`; WD-15's own rule is authored and deleted within its own test function, so no other test's execution window ever sees a non-empty table. |
| **VER-34** | All four doc-tier gates | `doc-check` (261 references / 81 files), `staleness-check` (14 claims, release 0.14.0 ✓ Taskfile match — not yet bumped, correct pre-tag), `adr-ledger-check` (74/74 ADRs agree), `go-deps-check` (10 deps / 13 modules) — all green. |
| **VER-35** | Every §5 negative proof observed failing on a mutation | Pre-existing from T-149b/T-150/T-151 (named tests: `TestRunGovernedStepDenyFailsClosedSEC58`, the SEC-55 spy-broker suite, `TestSessionCreateApprovalDenied`, `TestEvaluateWildcardDenyBeatsSpecificAllow`) reconfirmed green this session in both the unit and freshly-migrated integration runs. **SEC-57 is the one exception** — see `v0.15.0-security-review.md` §f: it holds by architectural construction but has no dedicated mutation-proven test. |
| Full regression | `task lint` (Go, 11 modules) + JS lint/typecheck/forbidden-deps + unit (Go 27 packages + JS 14 files/126 tests) + integration (6 suites / 203 subtests, fresh DB) + codegen drift (both Go configs + TS) | **All zero-issue / zero-failure / zero-drift.** |

**Reproduce the compose + smoke legs yourself before signing**, against the exact build you are about to
sign — do not accept this record on faith:

```bash
MSYS_NO_PATHCONV=1 task e2e
```
```bash
MSYS_NO_PATHCONV=1 task smoke
```

Assert the **positive** markers, never the absence of failures: `ok aireos.local/tests/e2e`,
`INTACT+ATTESTED`, and Playwright's `N passed` line naming zero failed.

---

## Step 1b — the WD-15 runbook (VER-30)

**What it proves.** Before this release, an unflagged resource ran `remote.exec` completely ungoverned
(ADR-0074's own diagnosis). WD-15 authors an operator rule that denies exactly that action on exactly
that resource, and proves **both halves** the criterion requires — absence alone is not the proof:

1. **Negative (SEC-55).** No `credential.leased` event anchored to this invocation's `session:<uuid>`
   evidence packet; no `remote.exec` event targeting its `invocation:<uuid>`. The broker is never
   reached.
2. **Positive (AUD-36).** `policy.decision:deny` chains, anchored to `session:<uuid>`, actor
   `agent:<agentID>`, edge `agent-remote-session`, target `resource:<id>`. `tool.result:denied` chains
   separately, anchored to `invocation:<uuid>`, same actor. The agent's `tools/call` returns a typed
   error (`isError:true`) — never a park, never a human-approvable prompt (SEC-58).

**Where it lives.** `tests/e2e/wd15_test.go`, `TestWD15RuleDeniedInvocation` — runs automatically as part
of `task e2e` (build tag `e2e`, no `wd1` — this is a compose-only proof; VER-30 does not require the
installed stack). The rule it authors is scoped to a random per-run agent subject and deleted at the end
of the same test function, so it cannot interfere with any other test sharing the same resource.

**To watch it run in isolation:**

```bash
cd infra/docker-compose
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.e2e.yml up -d postgres seed api broker connector policy audit mcp-gateway sshd
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.e2e.yml run --rm tester \
  go test -tags e2e -v -run TestWD15RuleDeniedInvocation ./tests/e2e/...
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.e2e.yml down -v
```

Read the test's own `t.Log` lines in order: the rule it authored, the typed deny the agent received, the
two positively-chained events with their actor/edge/target, and the two absences it checked directly
(never inferred).

---

## Step 1c — T-155: the two missing negative proofs (Commander ruling, CEO-approved 2026-09-01)

Test-only. No production code changed. Both proofs recorded here live, per VER-35's letter: observed
failing on a mutation, with the firing assertion named — not accepted on reasoning.

### (a) SEC-57 — "a rule cannot widen authority"

**Where it lives.** `tests/e2e/sec57_test.go`, `TestSEC57RuleAllowCannotWidenTokenScope` — runs as part
of `task e2e` (build tag `e2e`). Authors a wildcard-target `allow` rule
(`agent:<agentID>` x `agent-remote-session` x `remote.exec` x `*` -> `allow`, subject scoped to the run's
random agent so it cannot interfere with any other e2e test, WD-15's convention) and drives two legs
against it:

1. **CONTROL** — a token scoped to `resourceID` (matches what it calls): the wildcard allow rule actually
   permits execution. Without this, a skeptic could read the PROOF leg's refusal as "maybe the rule
   secretly denies too" instead of what it actually is.
2. **PROOF** — the SAME rule, a token scoped to `flaggedResourceID` (a *different* resource) calling
   `resourceID`. Asserts on the EXACT SEC-16 refusal string emitted at
   `services/mcp-gateway/internal/catalog/catalog.go:884` — `"resource is outside the agent token's
   scope"` — not merely that some error came back.

**Result, observed live 2026-09-01** (`task e2e`, full run, 63 PASS / 0 FAIL, chain
`INTACT+ATTESTED through seq 714`):

```
=== RUN   TestSEC57RuleAllowCannotWidenTokenScope
    sec57_test.go:92:  PASS control — matched scope + wildcard allow rule: call executed (the rule is a live allow, not a no-op)
    sec57_test.go:118: PASS SEC-57 — wildcard allow rule + scope-mismatched token: refused by the SEC-16 gate (catalog.go:884) with the exact message "resource is outside the agent token's scope", before the policy evaluator is ever reached
--- PASS: TestSEC57RuleAllowCannotWidenTokenScope (0.14s)
```

**SEC-57 holds.** The refusal is named precisely: the SEC-16 scope gate in `Service.Invoke`, which runs
before `runGovernedStep` — exactly the code path `v0.15.0-security-review.md` §f reasoned about, now
proven rather than argued. This was not assumed going in: if the PROOF leg had succeeded, or been refused
by any other message, that would have been reported as a live authority-widening defect, per the T-155
brief's explicit stop-and-report instruction.

### (b) A mutation control on `TestWD15RuleDeniedInvocation`'s negative half

The guard gating VER-30 (the release's headline claim) had never been observed failing — re-running a
passing test is a regression check, not a mutation proof (Commander review finding, criteria §17).
Each of its two SEC-55 absence checks was deliberately broken in turn, in isolation
(`cd infra/docker-compose && docker compose -f docker-compose.e2e.yml up -d postgres seed api broker
connector policy audit mcp-gateway sshd`, then `run --rm tester go test -tags e2e -v -run
TestWD15RuleDeniedInvocation ./tests/e2e/...`), observed failing, and restored — confirmed byte-identical
to the committed source via `git diff` (empty) before the final green re-run.

**Mutation A — the `credential.leased` absence check (anchored on `sessAnchor`).** Changed the compared
outcome from `"credential.leased"` (never emitted on this invocation) to `"policy.decision:deny"` — an
event already proven present at the SAME anchor a few lines earlier in the same test (the positive half).
A real event at the checked anchor, so a true mutation, not a vacuous one.

```
    wd15_test.go:180: SEC-55: a rule-denied invocation has a credential.leased: {Actor:agent:wd15-2f599dd1 Edge:agent-remote-session Target:resource:01900000-0000-7000-8000-0000000000e2 Outcome:policy.decision:deny EvidencePacketID:session:01a05d93-4e1f-700d-b124-f660fcb6436c}
--- FAIL: TestWD15RuleDeniedInvocation (2.11s)
```

**Firing assertion:** `tests/e2e/wd15_test.go:180`, `t.Fatalf("SEC-55: a rule-denied invocation has a
credential.leased: %+v", e)`.

**Mutation B — the `remote.exec` absence check (anchored on `e.Target == invAnchor`).** Changed the
compared field+outcome from `e.Target == invAnchor && e.Outcome == "remote.exec"` (never true on this
invocation) to `e.EvidencePacketID == invAnchor && e.Outcome == "tool.result:denied"` — again an event
already proven present at that anchor by the positive half, moments earlier in the same test.

```
    wd15_test.go:183: SEC-55: a rule-denied invocation has a remote.exec (broker egress ran): {Actor:agent:wd15-92f302bd Edge:agent-tool Target:resource:01900000-0000-7000-8000-0000000000e2 Outcome:tool.result:denied EvidencePacketID:invocation:01a05d93-b6c4-79d8-b75c-7617a4b98c82}
--- FAIL: TestWD15RuleDeniedInvocation (0.08s)
```

**Firing assertion:** `tests/e2e/wd15_test.go:183`, `t.Fatalf("SEC-55: a rule-denied invocation has a
remote.exec (broker egress ran): %+v", e)`.

**Restored, reconfirmed green** (`git diff tests/e2e/wd15_test.go` empty before this run):

```
    wd15_test.go:186: PASS negative half — no credential.leased, no remote.exec for this invocation
    wd15_test.go:187: PASS T-154 WD-15 — an operator-authored rule denies an otherwise-ungoverned action: no lease, no egress, and the denial is chained positively (policy.decision:deny + tool.result:denied)
--- PASS: TestWD15RuleDeniedInvocation (2.07s)
```

**The guard is now a guard** — both halves of its negative proof have been observed catching a planted
violation, not merely observed passing.

---

## Step 2 — provenance, with the SAME key as v0.14.0

**No new release keypair is minted this release.** The existing production key (custody per
`docs/devops/offline-install-v3.md` §Release key custody) signs these artifacts too.

```bash
task offline-bundle
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/postgres/build-postgres-companions.sh
```
```bash
task sign-release -- --key /path/to/aireos-release.key
```

Then both provenance gates, against the real key:

```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task provenance-check
```
```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task sec45-check
```

Confirm `task release-check`'s closing summary line says **VERIFIED**, not **SELF-SKIPPED**, and that no
stale `.sig` survived a rebuild (`ls -la dist/offline/*.tar.gz.sig`, mtime not older than its artifact).

⚠ **`BUNDLE_VERSION` in `Taskfile.yml` is still `0.14.0` at the time of this runbook.** That is correct
pre-tag — `staleness-check` asserts it matches `CLAUDE.md`'s "Latest release" claim, which is also still
v0.14.0 until this release ships. Bump both together as part of Step 4, not before — bumping only one
would fail `staleness-check` for a true reason.

---

## Step 3 — GATE, the CEO `§human` pass (VER-32)

**This is the release's first real dogfooding contact with the rule surface.** Drive it as an operator
would, on the installed stack or the WD-1 compose stack with at least one governed SSH resource (the
loopback demo target from `docs/devops/quickstart-first-governed-action.md` works), and record findings
rather than suppressing them.

**3.1 — Author a rule and watch it deny.** From `/policy-rules`, create a rule targeting a resource you
can invoke against — e.g. `subject=*`, `edge=agent-remote-session`, `action=remote.exec`,
`target=resource:<id>`, `outcome=deny`. Confirm the blast-radius note renders **before** you click
"Create rule" (FE-25). Then attempt the governed action (a session-start or an agent `remote.exec` call)
against that resource and confirm it is refused — no approval prompt, a clean denial.

**3.2 — Watch a more-specific `allow` fail to rescue a wildcard `deny` (ADR-0074 §D2).** With the
wildcard `deny` from 3.1 still enabled, author a SECOND rule that matches the exact same action more
specifically (e.g. pin the `subject` to your actual agent id instead of `*`) with `outcome=allow`.
Attempt the action again. **It must still be denied** — severity beats specificity, with no tiebreak.
This is the release's sharpest hazard and the one thing an operator must feel, not just read.

**3.3 — Delete the rule(s) and watch v0.14.0 behavior return.** Delete both rules from 3.1/3.2. Attempt
the same action again; it should now behave exactly as it did before either rule existed — CON-1 as an
operator would experience it, not just as a test asserts it.

**3.4 — Record findings.** Whatever you observe — friction, unclear copy, a surprising failure mode —
record it here rather than letting it go unrecorded.

### VER-32 RESULT — *pending the CEO's pass*

---

## Step 4 — sign-off and tag

Sign off only if Steps 1–3 are green **and** the release notes' non-claims still read as true — in
particular that R1's post-approval half is stated as open, not implied closed. SEC-57 is now
**mutation-proven live** (Step 1c, T-155) — `TestSEC57RuleAllowCannotWidenTokenScope` — so release notes
or criteria text still calling it "reasoned but untested" would themselves be stale; update them to match
before signing.

```bash
git tag -a v0.15.0 -m "v0.15.0 — Governed Decision Policy"
```

**The tag is local. This repo has no remote — never push, including tags.**

Tag the commit the artifacts were **built from**. If any commit landed after the build, go back to
Step 1 and Step 2 — the artifact you are about to attest would describe a tree that no longer exists.
