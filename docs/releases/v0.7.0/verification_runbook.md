# v0.7.0 — verification runbook

How to verify + tag v0.7.0 ("Chartered Workstreams" — the schedule-fired third
initiator). The in-session work is complete and Dockerized-verified per rung; the two
heavy gates (`task e2e`, `task wd1`) were executed green on the authoring host this
cycle — only the CEO §human WD-7 + the tag remain.

Release-candidate commit: the **T-078 Phase A commit** on `main` = `dev`
(`git rev-parse --short HEAD`). Baseline: **v0.6.0** (tag `8ba5bfe`). No remote;
nothing pushed.

> Toolchain note: this host has Node 22 + pnpm + Docker but **no native Go** (and
> `python`, not `python3`; the Windows host also lacks the linux rollup native binary,
> so the SPA build + vitest run in `node:22-alpine`). Go legs run in `golang:1.23`
> containers (Windows host → prefix `MSYS_NO_PATHCONV=1`); `doc-check` runs as
> `python infra/scripts/doc_check.py`. The aggregate `task release-check` expects one
> host with the full toolchain — the per-leg commands below are the documented
> offline-box equivalent.

---

## Step 0 — sync the RC

```sh
cd ai-remote-engineering-os
git checkout main && git log --oneline -1     # expect: T-078 ... Phase A
git status --porcelain                        # expect: clean
```

## Step 1 — DOC-15 doc-check (paths resolve)

```sh
python infra/scripts/doc_check.py             # asserts every CANONICAL + ADR path resolves
```
**Expect:** exit 0 (the new `docs/releases/v0.7.0/` row + module-map /
agent-permission-model additions reference only on-disk paths). doc-check scans only
`CANONICAL_SOURCE_OF_TRUTH.md` + `docs/adr/*.md`, so the manifest's
`tests/smoke/test-results/wd7/` reference — created in Phase B — is not path-checked.
Recorded green this cycle.

## Step 2 — VER-20 code gates (Dockerized)

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w golang:1.23 sh -c '
  cd services/api && go build ./... && go vet ./... && go test ./...'
MSYS_NO_PATHCONV=1 docker run --rm -e CI=true -v "$PWD":/w -w /w node:22-alpine sh -c '
  corepack enable && pnpm install --frozen-lockfile && pnpm -r typecheck && pnpm --filter @aireos/web test && pnpm --filter @aireos/web build && pnpm -r lint'
task sec13-gate    # connector-service stays the sole decrypt authority
task licenses      # GOWORK=off
```
**Expect:** all green; **zero new third-party dependency** (the charter is two tables +
a struct + a goroutine — no cron parser/SDK; supply-chain doctrine). Recorded green
per-ticket this cycle (T-071..T-075, incl. web vitest 40/40 + eslint clean at T-075).
Only `services/api` + `apps/web` changed; the gateway is untouched (zero gateway diff).
Note: `pnpm -r typecheck` PATH-races `tsc` on this host — run per-workspace
(`pnpm --filter @aireos/{shared-types,ui,web} exec tsc --noEmit`) to confirm.

## Step 3 — VER-21 codegen drift zero

```sh
task codegen && git diff --exit-code          # byte-deterministic: empty diff
```
**Expect:** empty diff — the v0.7.0 fragment changes (`/v1/workstreams*` routes +
`Workstream`/`WorkstreamCreate`/`WorkstreamStep` schemas, landed T-071/T-074) regenerate
`gen.go` + `openapi.ts` deterministically. No fragment change since T-074, so this is a
formality at the closure tree. Recorded byte-deterministic at T-071/T-074.

## Step 4 — VER-22 live legs

```sh
MSYS_NO_PATHCONV=1 task e2e      # the WD-7 capstone + the full suite + audit-service verify
MSYS_NO_PATHCONV=1 task smoke    # the /workstreams charter->pause->resume->retire browser DoD
```
**Recorded this cycle:**
- `task e2e` **GREEN** at the RC tree (22/22) — `TestWD7HappyPathRecurrence` (an ungated
  charter at the 5s e2e floor fires ≥2 distinct Runs that complete **unattended**, actor
  = `workstream:<id>`; `workstream.chartered` + `grant.issued` in-tx + `run.launched`
  actor=workstream + `run.completed` chained) + `TestWD7FlaggedTickParksAndApproves`
  (flagged tick parks with workstream+principal+grant+run context → live approve →
  continue → completed) + `TestWD7RetireKillsInFlight` (N1: retire cascades to
  `run.canceled`, post-park step never dispatched, schedule stops) +
  `TestWD7PauseResume` (N2) + `TestWD7BudgetWindowRecovers` (N3: `run.launch.denied`,
  charter stays active, recovers next window) + `TestWD7SelfRetireOnDeadGrant`
  (`workstream.retired:grant-dead`) + **WD-5 + WD-6 regression green**;
  **`audit chain intact: 357 event(s) verified`**.
- `task smoke` **GREEN 8/8** — the `/workstreams` charter (GOV-8 consent copy) →
  active-with-schedule → pause → resume → retire lifecycle rendered in Chromium against
  the built bundle (screenshots attached by the suite).

## Step 5 — OI-14 installed WD-7 (from-HEAD bundle)  ⚠ rebuild-from-HEAD

`dist/offline/` must hold the **HEAD** bundle only (the t033 lesson — remove stale
prior-version bundles so `run-wd1.sh`'s `aireos-offline-*` glob is unambiguous).

```sh
rm -rf dist/offline/aireos-offline-0.6.0*
# host lacks the linux rollup binary → build the SPA in node:22-alpine
MSYS_NO_PATHCONV=1 docker run --rm -e CI=true -v "$PWD":/w -w /w node:22-alpine sh -c '
  corepack enable && pnpm install --frozen-lockfile && pnpm --filter @aireos/web build'
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w -v aireos-gomod:/go/pkg/mod \
  -e BUNDLE_VERSION=0.7.0 golang:1.23 bash infra/offline/build-bundle.sh
MSYS_NO_PATHCONV=1 task wd1
```
**Recorded this cycle:** bundle `aireos-offline-0.7.0.tar.gz` built from HEAD —
**116 MB unpacked / 41 MB gz** (OI-4 caps 500 / 150 hold). `task wd1` **GREEN** on the
real privileged-systemd install — `TestWD7InstalledStack` (charter a single-flagged-step
objective at the 60s production floor → the schedule fires with nobody present → the
flagged tick parks with **workstream · principal · grant · run** context → approve →
completed with workstream provenance → retire stops the schedule; ~66s at the 60s
floor) alongside WD-4 + WD-5 + WD-6; chain complete incl. `workstream.chartered` /
`workstream.retired` + the **workstream-actor `run.launched`** attribution check. Install
wiring: **NO install.sh change** — migration 0011 rides the migrate loop, the ticker
rides the `GATEWAY_URL` already on the installed api (OI-13), and the schedule floor is
the 60s `main.go` default (the install ships the production default — no new env, no new
key material). The `run-wd1.sh` go-test timeout was raised 300s→600s (the F5 60s-floor
makes WD-7 the slowest leg).

## Step 6 — CEO §human WD-7 walkthrough  (the remaining gate)

On a running stack (the smoke stack or the installed stack), through the cockpit UI
(the schedule fires on its own; the throwaway capture compose may lower
`AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS` for a brisk walkthrough — the v0.5/0.6
mechanics):

1. Log in → open **`/workstreams`** → **charter** a Workstream (objective; a bounded
   scope with an ungated + a flagged resource; an immutable template mixing them; an
   interval; TTL + bounds + budget window). Note the **GOV-8 consent copy** — chartering
   consents to unattended, scheduled execution of this exact immutable template until
   retire/expiry.
2. Watch the schedule **fire with nobody at launch**: an ungated tick runs **unattended**;
   the flagged tick **parks** on **`/invocations`** showing **scheduled by
   `workstream:<id>` · on behalf of <you> · grant <id> · step k of n** beside the verbatim
   command. **Approve** → the Run continues → **completed**.
3. **Pause** the charter → confirm no new fire across an interval; **resume** → confirm it
   fires again.
4. **Retire** the charter → its grant is revoked and any in-flight Run is canceled; confirm
   no further fire.
5. Confirm the audit timeline shows `workstream.chartered` → `run.launched`
   (`workstream:<id>` actor) → per-step events → `run.completed`, and
   `workstream.retired` on retire, hash-intact.

## Step 7 — tag (after sign-off)  ⚠ local only, no remote

```sh
git checkout main
git tag -a v0.7.0 -m "v0.7.0 — Chartered Workstreams (schedule-fired, grant-bounded Runs)"
git tag -l                                    # expect v0.1.0..v0.7.0
# DO NOT push — local annotated tag only (repo doctrine).
```
Then flip `status` in `release_notes_v0_7_0.md` + `release_manifest_v0_7_0.json`
from "release-candidate" to "released — tag v0.7.0 → <commit>".
