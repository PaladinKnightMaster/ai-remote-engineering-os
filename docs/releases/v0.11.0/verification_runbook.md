# v0.11.0 — verification runbook

How to verify the v0.11.0 "Operator Experience" release candidate end to end,
with the recorded results from the T-110 Phase A verification run
(2026-07-27). Commands assume the repo root on a Windows host with Docker
Desktop + WSL2 (prefix Docker invocations with `MSYS_NO_PATHCONV=1` under
git-bash); a Linux host drops the prefix. The host in this run lacked
`go`/`python3`; every Go step below ran inside `golang:1.23`.

Recorded RC state: `main` = `dev` at `c9ad30b` before this ticket's commits;
local tags `v0.1.0..v0.10.0`, no remote. Baseline `v0.10.0` → `e0fee10`.

## Step 0 — sync the RC

```bash
git status --short          # clean
git rev-parse main dev      # identical
```

## Step 1 — OI-18 zero install-wiring diff (independently verified pre-ticket)

```bash
git diff v0.10.0 HEAD -- install.sh systemd/ Caddyfile
```

Recorded: **EMPTY** — verified by the War Room Commander before T-110 began;
cited here, not re-derived. `infra/offline/` is otherwise untouched by the
v0.11.0 ladder (T-105..T-109 touch only `services/`, `apps/web/`,
`tests/e2e/`, `docs/`).

## Step 2 — DOC-22 doc-check (paths resolve)

```bash
python infra/scripts/doc_check.py
```

Recorded: **OK** — every in-repo path referenced in CANONICAL +
the ADRs resolves.

## Step 3 — VER-2 codegen drift zero

```bash
task codegen
git diff --stat            # empty — gen.go (api + audit-service) + openapi.ts
                            # byte-deterministic
```

Recorded at T-106 (`e4a970b`): two consecutive `task codegen` runs, byte-
identical output.

## Step 4 — GATE-1 the full WD-1..10 regression, both stacks

### 4a. Compose (`task e2e`)

```bash
MSYS_NO_PATHCONV=1 task e2e
```

Recorded: **PASS — 346.824s**. Every WD test green: `TestGovernedEgress*`,
`TestDelegatedLaunch*`, `TestSSHCockpitEndToEnd`, `TestBrokerExecEgress`,
`TestGatewayGovernedExec*`, `TestRunEngine*`, `TestWD5*`, `TestWD10*` (the
happy path — including leg 4's fixed `context.read` assertion, see the T-110
Task 1 note below — plus N1/N2/N3/N4/N6), `TestKeylessGatewayCannotForge
ForeignEdge`, `TestWD6*`, `TestWD7*`, `TestWD8*`, `TestWD9*`. Final line:
`audit chain intact: 613 event(s) verified`.

**T-110 Task 1 note (the WD-10 flake, diagnosed not papered over).** Before
this run, `wd10_test.go`'s leg 4 had failed once in a prior verification pass
with `context.read count for composer %s = 2, want exactly 1`, then passed on
rerun. Diagnosis: leg 2's `waitForSearchHit` polls `context.search` under the
**same composer actor** while it waits for the index sweep, and
`SearchForAgent` chains exactly one `context.read` event **per call, hit or
miss** — the original assertion counted *all* `context.read` events for the
composer since the actor was first used, so it depended on timing (whether
leg 4's own newly-enqueued event had been outbox-relayed by the time the poll
helper's first check ran), not on the SEC-33 per-call invariant. Scenario (a)
from the T-110 brief: a harness over-count, not a chain defect. Fixed by
baselining the count immediately before leg 4's own `context.search` call and
asserting the count grows by exactly `baseline + 1` — commit `034ba3e`.
Recorded per the brief's instruction: any change to a ratified v0.10 WD-10
gate assertion must be explicit, never a silent edit; see
`release_notes_v0_11_0.md`'s as-built section for the full diagnosis.

### 4b. Installed (`task wd1`) — ⚠ rebuild the bundle from HEAD first

```bash
# 1. Rebuild the bundle FROM HEAD as 0.11.0 (the T-033 lesson — never run wd1
#    on a stale bundle). Delete any older core tarball first:
rm -f dist/offline/aireos-offline-*.tar.gz dist/offline/aireos-offline-*/  -r
# host lacks go -> cross-compile inside golang:1.23 (has bash/curl/tar/
# sha256sum/sha512sum out of the box, unlike node:22-alpine):
CI=true pnpm install --frozen-lockfile
pnpm --filter @aireos/web build
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/work -w /work \
  -e BUNDLE_VERSION=0.11.0 golang:1.23 bash infra/offline/build-bundle.sh
# Recorded: 116 MB unpacked / 42 MB gzipped — the OI-4 caps (500/150 MB)
# hold with wide margin (v0.10.0 baseline: 116/41 MB).

# 2. The installed walkthrough (systemd-in-Docker):
MSYS_NO_PATHCONV=1 task wd1
```

Recorded: **GREEN, exit 0**. Wall clock (bring-up + walkthrough + chain
assertions): ~11m33s (18:30:12 → 18:41:45). The go-test leg itself:
`ok aireos.local/tests/e2e 651.655s` against the **1500s** ceiling (**43%**).
`TestWD9InstalledStack` (256.79s) + `TestWD10InstalledStack` (100.52s) +
`TestWD10ConfinedRootInstalled` (60.21s) all PASS. SEC-20 keyless-gateway
assertion OK. Final line: `audit chain intact: 222 event(s) verified`,
followed by the full WD-4..10 PASS marker naming every governed plane. No
`FAIL`/`panic`/error anywhere in the run log.

## Step 5 — GATE-2 + OI-19/OI-20

### 5a. Browser smoke (`task smoke`)

```bash
MSYS_NO_PATHCONV=1 task smoke
```

Recorded: **14/14 PASSED (2.7m)** — `audit.smoke.spec.ts` (newest-first +
preset disclosure + server-side filtering, the new v0.11.0 assertions),
`context-sources.smoke.spec.ts`, `governed-egress.smoke.spec.ts`,
`grants.smoke.spec.ts`, `invocation-approval.smoke.spec.ts`,
`memories.smoke.spec.ts` (×2), `runs.smoke.spec.ts` (×4),
`workstreams.smoke.spec.ts` (×2, including the FE-30 zero-audit-events-
while-polling assertion, T-108). No flakes, no retries.

### 5b. OI-19 — bundle size vs the v0.10.0 baseline

Already rebuilt at Step 4b: **116 MB unpacked / 42 MB gzipped** vs the
v0.10.0 baseline **116 / 41 MB** — the OI-4 caps (500 MB / 150 MB) hold with
wide margin; the 1 MB gzip delta is noise-scale (Caddy/Go toolchain patch
versions), not a size regression from v0.11.0's own (docs+test+read-path)
diff.

### 5c. OI-20 — zero new third-party dependency

```bash
# Go half (host lacks go -> Dockerized):
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/work -w /work golang:1.23 bash -c '
  ALLOWED="MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0,LGPL-2.1,LGPL-3.0,Unlicense,0BSD,CC0-1.0,Python-2.0"
  for mod in services/api services/audit-service services/connector-service services/mcp-gateway services/policy-service services/session-broker; do
    (cd "$mod" && GOWORK=off go run github.com/google/go-licenses@v1.6.0 check ./... --allowed_licenses="$ALLOWED" --ignore aireos.local)
  done'
# JS half (host has node/pnpm):
node infra/scripts/check-js-licenses.mjs "MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0,LGPL-2.1,LGPL-3.0,Unlicense,0BSD,CC0-1.0,Python-2.0"
# Independent lockfile-diff confirmation:
git diff v0.10.0 HEAD --stat -- '**/go.sum' go.work.sum pnpm-lock.yaml
```

Recorded: all six Go modules and the JS production dependency set pass
clean (`js-licenses OK — all production dependency licenses allowed (ISC,
MIT)`); the lockfile diff against v0.10.0 is **empty** — zero new
third-party dependency, two independent ways.

## Step 6 — CEO §human GATE-3 walkthrough (the remaining gate)

**Placeholder.** This step is authored separately by the War Room Commander
at Phase B and will script the four operator-visible behaviors from
`v0.11.0-release-criteria.md` §2 GATE-3:

1. `/audit` opens newest-first with the most recent governed action on the
   first screen, no paging, on a stack carrying production-shaped lifecycle
   volume.
2. The preset is disclosed — the operator can see they are on a filtered
   view, see the total chain length, and reach the complete unfiltered
   chain in one interaction.
3. A server-side filter (actor/outcome/edge) returns matches from the whole
   chain, not the loaded page — demonstrated on a value whose matches are
   deep in the chain.
4. An open charter-detail panel refreshes itself as the charter ticks, and
   stops when the panel closes or the tab is hidden.

Driven live in a real host browser (Chrome/Edge at `http://localhost:5173`
via the ADR-0047 dev proxy) — the Windows-host TLS route recorded in the
v0.9.0/v0.10.0 runbook Step 6 applies unchanged. The Commander supplies the
full script text and the recorded result at Phase B; this file is updated in
the closure commit alongside the artifact flip to *released*.

## Step 7 — tag (after sign-off) ⚠ local only, no remote

```bash
git tag -a v0.11.0 -m "v0.11.0 Operator Experience"
# DO NOT push — local annotated tag only (repo doctrine). The Commander tags
# at Phase B, after GATE-3 sign-off — never the implementer.
```
