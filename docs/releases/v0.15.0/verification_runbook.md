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
particular that R1's post-approval half is stated as open, not implied closed, and that SEC-57 is stated
as reasoned-but-untested rather than proven.

```bash
git tag -a v0.15.0 -m "v0.15.0 — Governed Decision Policy"
```

**The tag is local. This repo has no remote — never push, including tags.**

Tag the commit the artifacts were **built from**. If any commit landed after the build, go back to
Step 1 and Step 2 — the artifact you are about to attest would describe a tree that no longer exists.
