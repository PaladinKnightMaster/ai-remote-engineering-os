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

GATE-3 is a **spot-check, not a new walkthrough demo** — v0.11 adds no governed
capability (structural rule #2), so no WD-11 is minted. It covers the four
operator-visible behaviors in `v0.11.0-release-criteria.md` §2 GATE-3.

### The walkthrough generates its own defect condition

The finding this release closes was measured at **635 events, 601 (95%)
adaptive-charter lifecycle noise**. That condition is reproducible on demand: an
**adaptive charter nobody answers** opens a plan request each tick and expires it
at its TTL — two content-free events per cycle, indefinitely.

The e2e stack sets `AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS: "5"`, so a 5-second
charter produces ~24 lifecycle events/minute. **No agent driver is needed** —
nobody answering *is* the scenario. (Contrast WD-9/WD-10, which need the
agent-driver helper because the composer must actually answer.)

### Setup

From `infra/docker-compose/`:

```bash
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.e2e.yml -f docker-compose.review.yml up -d --build api audit broker connector policy mcp-gateway sshd
```

Then from `apps/web/`:

```bash
pnpm exec vite --host 127.0.0.1
```

`--host 127.0.0.1` is **required**: Vite defaults to `::1`-only on this host and
IPv6 loopback TCP is broken here. `pnpm --filter @aireos/web dev -- --host` does
**not** forward the flag. Vite's defaults (`http://localhost:8080` /
`http://localhost:8082`) already match the review overlay's published ports, so no
env vars are needed.

Browse **`http://localhost:5173`** in a **real host browser** (Chrome/Edge).
Login: **`admin` / `e2e-operator-pw`**.

> **Windows-host access (v0.9 lesson, unchanged).** Docker Desktop's WSL2 HTTPS
> port-forwarding corrupts the TLS handshake for OS-native clients
> (`ERR_SSL_PROTOCOL_ERROR`) while in-network clients work. The review overlay +
> dev-proxy route above keeps TLS off the host path entirely.
>
> **Do not drive this in the Claude Desktop Browser pane** — it auto-dismisses
> `window.confirm` (returns false), so consent-gated actions silently cancel.

### Step A — start the noise clock (first)

`/workstreams` → charter an **adaptive** workstream:

| Field | Value |
|---|---|
| Label | `gate3-noise` |
| Objective | `spot-check the audit read surface` |
| Kind | **adaptive** |
| Composer | `gate3-composer` |
| Resource (scope) | `e2e-target` (= `01900000-0000-7000-8000-0000000000e2`) |
| **Interval seconds** | **`5`** |
| Rest | defaults |

Leave it ticking. Give it **10 minutes minimum** before Step B, and spend that
window on Step D — which needs the charter live anyway, so the wait is absorbed
rather than added.

### Step D — live detail refresh (during Step A's accumulation)

Select the charter to open its detail panel. Open DevTools → **Network**, filter
`v1/`.

- **D1** — the **"Updated Xs ago"** label counts up and resets to "just now"
  every **10 seconds**.
- **D2** — new plan requests appear every ~5s unattended; each expires at TTL and
  a fresh one opens. That loop is what generates the Step B volume.
- **D3** — switch to a **different browser tab** ~30s and return. The Network log
  shows a **clean gap** (nothing polled while hidden) and **one immediate burst**
  on return, with "Updated" reading "just now" — not "30s ago".
- **D4** — deselect the charter (click its name; it toggles). Polling stops —
  **zero** further `/v1/` rows.

The poll signature is **exactly 4 requests per burst**, ~10s apart:
`workstreams/{id}` · `memories?scopeType=workstream&scopeId=…` ·
`context-sources?workstreamId=…` · `runs` — all from consecutive `Workstreams.tsx`
initiator lines (one `Promise.all`). **No `workstreams` list fetch and no `grants`
fetch** — that absence is FE-27's "targeted, never `loadAll`" holding. Don't
confuse it with the header **Refresh** button, which calls `loadAll` and fires a
different, larger 3-request signature.

### Step B — the audit surface opens usable

`/audit`, after ~10 minutes of ticking.

- **B1** — the most recent governed action is on the **first screen**, no "Load
  more". *(Before v0.11: 13 pages deep.)*
- **B2** — the banner names the preset **`governed-actions@1`** and reports the
  chain size as **"N positions"**. "Positions", not "events", is deliberate:
  it is `MAX(seq)`, and `seq` is `BIGSERIAL` with a non-transactional `nextval`,
  so a rolled-back append leaves a gap. Calling it a row count would overstate it.
- **B3** — **"Show all events"** (one interaction) floods the noise back and the
  banner disappears. Nothing was ever suppressed. Toggle back on.

### Step C — filtering reaches the whole chain

- **C1** — Outcome → `auth.login.success`, **Enter** (filters commit on blur or
  Enter, or the **Apply** button — not per keystroke). Login events are among the
  oldest in the chain, far past the 50 rows loaded; they return anyway.
- **C2** — the banner echoes the filter back **from the response**, so it can only
  report what the server actually did.
- **C3** — Actor → `operator:admin`.
- **C4** — Edge → `agent-repo` (a reserved edge, zero rows by construction). List
  empties, banner still reports every applied filter and the chain size, and the
  table reads **"No events match this filter."** — not the unfiltered
  "No audit events yet." copy (T-111).
- **C5** — toggle direction and back. There are **no per-column sorters**; this is
  intentional (EVD-8/AUD-27), not missing.

### Tear down

```bash
MSYS_NO_PATHCONV=1 docker compose -f docker-compose.e2e.yml -f docker-compose.review.yml down -v
```

Screenshots are captured at the CEO's discretion; per v0.9/v0.10 precedent they
are **not** git-archived. The recorded result is in
`release_notes_v0_11_0.md` §"§human findings".

## Step 7 — tag (after sign-off) ⚠ local only, no remote

```bash
git tag -a v0.11.0 -m "v0.11.0 Operator Experience"
# DO NOT push — local annotated tag only (repo doctrine). The Commander tags
# at Phase B, after GATE-3 sign-off — never the implementer.
```
