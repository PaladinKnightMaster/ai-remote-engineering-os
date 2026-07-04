# v0.6.0 — verification runbook

How to verify + tag v0.6.0 ("Agent-initiated Runs" — the delegation-grant model).
The in-session work is complete and Dockerized-verified per rung; the two heavy
gates (`task e2e`, `task wd1`) were executed green on the authoring host this cycle
— only the CEO §human WD-6 + the tag remain.

Release-candidate commit: the **T-070 Phase A commit** on `main` = `dev`
(`git rev-parse --short HEAD`). Baseline: **v0.5.0** (tag `981cb0e`). No remote;
nothing pushed.

> Toolchain note: this host has Node 22 + pnpm + Docker but **no native Go** (and
> `python`, not `python3`). Go legs run in `golang:1.23` containers (Windows host →
> prefix `MSYS_NO_PATHCONV=1`); JS legs run on the host or `node:22-alpine` with
> `CI=true`; `doc-check` runs as `python infra/scripts/doc_check.py`. The aggregate
> `task release-check` expects one host with the full toolchain — the per-leg
> commands below are the documented offline-box equivalent.

---

## Step 0 — sync the RC

```sh
cd ai-remote-engineering-os
git checkout main && git log --oneline -1     # expect: T-070 ... Phase A
git status --porcelain                        # expect: clean
```

## Step 1 — DOC-14 doc-check (paths resolve)

```sh
python infra/scripts/doc_check.py             # asserts every CANONICAL + ADR path resolves
```
**Expect:** exit 0 (the new `docs/releases/v0.6.0/` row + module-map /
agent-permission-model additions reference only on-disk paths). Recorded green this
cycle.

## Step 2 — VER-17 code gates (Dockerized)

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w golang:1.23 sh -c '
  for m in services/api services/mcp-gateway; do (cd $m && go build ./... && go vet ./... && go test ./...); done'
MSYS_NO_PATHCONV=1 docker run --rm -e CI=true -v "$PWD":/w -w /w node:22-alpine sh -c '
  corepack enable && pnpm install --frozen-lockfile && pnpm -r typecheck && pnpm --filter @aireos/web test && pnpm --filter @aireos/web build && pnpm -r lint'
task sec13-gate    # connector-service stays the sole decrypt authority
task licenses      # GOWORK=off
```
**Expect:** all green; **zero new third-party dependency** (the grant is a table + a
struct — no policy engine/SDK; supply-chain doctrine). Recorded green per-ticket this
cycle (T-063..T-069, incl. web vitest 31/31 + eslint clean at T-068b).

## Step 3 — VER-18 codegen drift zero

```sh
task codegen && git diff --exit-code          # byte-deterministic: empty diff
```
**Expect:** empty diff — the v0.6.0 fragment changes (`/v1/grants*` routes +
`Grant`/`GrantCreate` schemas + `PendingInvocation.principal`/`grantId` +
`Run.actor`/`grantId`) regenerate `gen.go` + `openapi.ts` deterministically.
Recorded green at T-063/T-066b/T-067b/T-068b. (`run.launch` is an MCP surface; the
launch forward + `/v1/steps` are internal service-service RPCs — deliberately NOT in
the fragment.)

## Step 4 — VER-19 live legs

```sh
MSYS_NO_PATHCONV=1 task e2e      # the WD-6 capstone + the full suite + audit-service verify
MSYS_NO_PATHCONV=1 task smoke    # the /grants + delegated /invocations + /runs browser DoD
```
**Recorded this cycle:**
- `task e2e` **GREEN** at the RC tree — `TestWD6HappyPath` (grant → agent
  `run.launch` mixed 3-step: unattended → flagged parks with agent+principal+run
  context → live approve → CONTINUE → completed; evidence packet actor/grantId +
  3 invocation ids; `grant.issued`/`run.launched` actor=agent/`agent-tool`
  edge/`approval.granted`/`run.completed` chained) + `TestWD6RevokeCancelsInFlight`
  (N2 mid-park: `grant.revoked` + `run.canceled`, released step finalized, no later
  dispatch) + N1 out-of-grant / N3 direct-agent→api-401 (`TestDelegatedRunLaunch`) +
  the SEC-21 forge-negative + **WD-5 regression green** + cancel/crash-resume;
  **`audit chain intact: 142 event(s) verified`**.
- `task smoke` **GREEN 7/7** — the `/grants` issue→list-active→revoke leg + the real
  delegated flagged Run rendering **agent · on behalf of operator:admin · grant
  <id>** on `/invocations` (screenshot attached by the suite) + the `/runs`
  "Driven by agent" label.

## Step 5 — OI-13 installed WD-6 (from-HEAD bundle)  ⚠ rebuild-from-HEAD

`dist/offline/` must hold the **HEAD** bundle only (the t033 lesson — remove stale
prior-version bundles so `run-wd1.sh`'s `aireos-offline-*` glob is unambiguous).

```sh
# host node builds the SPA; golang:1.23 cross-compiles + stages the bundle
CI=true pnpm install --frozen-lockfile && CI=true pnpm --filter @aireos/web build
rm -rf dist/offline/aireos-offline-0.5.0*
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w -v aireos-gomod:/go/pkg/mod \
  -e BUNDLE_VERSION=0.6.0 golang:1.23 bash infra/offline/build-bundle.sh
MSYS_NO_PATHCONV=1 CI=true task wd1
```
**Recorded this cycle:** bundle `aireos-offline-0.6.0.tar.gz` built from HEAD —
**115 MB unpacked / 41 MB gz** (OI-4 caps 500 / 150 hold). `task wd1` **GREEN** on
the real privileged-systemd install — all four driver tests passed
(`TestWD6InstalledStack`: delegated 2-step happy with full parked context → approved
→ completed with actor/grant provenance; revoke-mid-park cascaded to canceled with
no later dispatch; direct agent→api forward rejected 401); **`audit chain intact:
58 event(s) verified`** incl. `grant.issued`/`grant.revoked`/`run.canceled` + the
**agent-actor `run.launched`** attribution check. Install wiring: `API_URL` written
to `gateway.env` (the one new wire); the gateway public key **reused** from
`aireos.env` (OI-11) — zero new key material.

## Step 6 — CEO §human WD-6 walkthrough  (the remaining gate)

On a running stack (the smoke stack or the installed stack), through the cockpit UI
(agent-side `run.launch` calls are fired out-of-band on cue, or via the captured
throwaway walkthrough spec — the v0.5.0 mechanics):

1. Log in → open **`/grants`** → issue a grant (agent subject; an ungated + a
   flagged resource; TTL + bounds + budget — note the form's concurrency cap hint
   and the expiry shown on the issued row).
2. The **agent** launches a mixed plan via `run.launch` under that grant (no human
   at launch). Watch the Run appear in **`/runs`** labeled **"Driven by agent X ·
   grant G"**.
3. The ungated step runs **unattended**; the flagged step **parks** on
   **`/invocations`** showing **agent · on behalf of <you> · grant <id> · step k of
   n** beside the verbatim command. **Approve** → the Run continues → **completed**.
4. Launch a second delegated Run; while its flagged step is parked, **revoke the
   grant on `/grants`** → the Run flips to **canceled** on `/runs`; deny-release the
   parked step; confirm no further step ran.
5. Confirm the audit timeline shows `grant.issued` → `run.launched` (agent actor) →
   per-step events → `run.completed`, and `grant.revoked` → `run.canceled` for the
   second Run, hash-intact.

## Step 7 — tag (after sign-off)  ⚠ local only, no remote

```sh
git checkout main
git tag -a v0.6.0 -m "v0.6.0 — Agent-initiated Runs (delegation grants)"
git tag -l                                    # expect v0.1.0..v0.6.0
# DO NOT push — local annotated tag only (repo doctrine).
```
Then flip `status` in `release_notes_v0_6_0.md` + `release_manifest_v0_6_0.json`
from "release-candidate" to "released — tag v0.6.0 → <commit>".
