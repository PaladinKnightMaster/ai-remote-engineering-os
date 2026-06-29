# v0.5.0 — verification runbook

How to verify + tag v0.5.0 ("Governed Workstreams" — layer 1: Runs). The in-session
work is complete and Dockerized-verified per rung. Unlike v0.2.0–v0.4.0, the two heavy
gates were **runnable on the authoring host this cycle** (it has Node 22 + python3 +
Docker; Go is supplied via the `golang:1.23` container), so `task e2e` **and**
`task wd1` were both executed green here — only the CEO §human WD-5 + the tag remain.

Release-candidate commit: the **T-062 closure commit** on `main` = `dev`
(`git rev-parse --short HEAD`). Baseline: **v0.4.0** (tag `874db08`). No remote; nothing
pushed.

> Toolchain note: this host has Node 22 + pnpm + python3 but **no native Go**. The Go
> legs run in `golang:1.23` containers (Windows host → prefix `MSYS_NO_PATHCONV=1`); JS
> legs run in `node:22-alpine` or on the host; `doc-check` runs natively. The aggregate
> `task release-check` expects one host with the full toolchain — the per-leg commands
> below are the documented offline-box equivalent.

---

## Step 0 — sync the RC

```sh
cd ai-remote-engineering-os
git checkout main && git log --oneline -1     # expect: T-062 ... release closure
git status --porcelain                        # expect: clean
```

## Step 1 — DOC-13 doc-check (paths resolve)

```sh
python3 infra/scripts/doc_check.py            # asserts every CANONICAL + ADR path resolves
```
**Expect:** exit 0 (the new `docs/releases/v0.5.0/` row + module-map/agent-permission
additions reference only on-disk paths).

## Step 2 — VER-14 code gates (Dockerized)

```sh
# lint + test + build + typecheck (golang:1.23 for Go; node:22-alpine for JS)
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w golang:1.23 sh -c '
  for m in services/api services/mcp-gateway; do (cd $m && go build ./... && go vet ./... && go test ./...); done'
MSYS_NO_PATHCONV=1 docker run --rm -e CI=true -v "$PWD":/w -w /w node:22-alpine sh -c '
  corepack enable && pnpm install --frozen-lockfile && pnpm -r typecheck && pnpm --filter @aireos/web test && pnpm --filter @aireos/web build && pnpm -r lint'
# sec13-gate (grep): connector-service is the sole decrypt authority
task sec13-gate
# licenses (GOWORK=off; golang + node)
task licenses
```
**Expect:** all green; **zero new third-party dependency** (the sequencer is
hand-rolled — no workflow/DAG engine; supply-chain doctrine). Recorded green this cycle.

## Step 3 — VER-15 codegen drift zero

```sh
task codegen && git diff --exit-code        # byte-deterministic: empty diff
```
**Expect:** empty diff (the v0.5.0 Run routes regenerate `gen.go` + `openapi.ts`
deterministically). Recorded green this cycle.

## Step 4 — VER-16 live legs

```sh
task e2e      # the WD-5 capstone + the full suite, then `audit-service verify`
task smoke    # the /runs + /invocations browser DoD (Playwright)
```
**Recorded this cycle:**
- `task e2e` **GREEN** — `TestWD5HappyPath` (mixed 3-step park→approve→continue) +
  `TestWD5DeniedAbort` (fail-closed) + cancel + crash-resume; `audit chain intact:
  95 event(s) verified`.
- `task smoke` **GREEN** 5/5 — incl. the `/runs` launch→completed + the parked-step
  Run-context leg on `/invocations`.

## Step 5 — OI-12 installed WD-5 (from-HEAD bundle)  ⚠ rebuild-from-HEAD

`dist/offline/` must hold the **HEAD** bundle (the t033 lesson). Remove any stale
prior-version bundle first so `run-wd1.sh`'s `aireos-offline-*` glob is unambiguous.

```sh
# host node builds the SPA; golang:1.23 cross-compiles the binaries
MSYS_NO_PATHCONV=1 docker run --rm -e CI=true -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && pnpm install --frozen-lockfile && pnpm --filter @aireos/web build'
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD":/w -w /w -v aireos_gomodcache:/go/pkg/mod golang:1.23 sh -c \
  'apt-get update -qq && apt-get install -y -qq curl && BUNDLE_VERSION=0.5.0 bash infra/offline/build-bundle.sh'
rm -rf dist/offline/aireos-offline-0.4.0*      # keep only 0.5.0 + the postgres companion
task wd1
```
**Recorded this cycle:** bundle `aireos-offline-0.5.0.tar.gz` built — **115 MB unpacked /
41 MB gz** (OI-4 caps 500 / 150 hold). `task wd1` **GREEN** on the real
privileged-systemd install — `TestWD5InstalledStack` drove a 2-step governed Run through
the installed gateway with two approvals to completed; `audit chain intact: 33 event(s)
verified` incl. `run.launched`/`run.completed`. (Migrations applied via `install.sh`,
`GATEWAY_URL` already wired — zero config change.)

## Step 6 — CEO §human WD-5 walkthrough  (the remaining gate)

On a running stack (compose `task e2e` stack, or the installed stack), through the
cockpit UI:

1. Log in → open **`/runs`** → compose a plan (≥3 steps mixing an ungated and a
   `requires_approval`-flagged resource).
2. **Review the full plan** (every command verbatim) → **launch**.
3. Watch the ungated step run; the flagged step **parks** and surfaces on
   **`/invocations`** with its **Run context** ("step k of n") + the verbatim command.
4. **Approve** → the held step resumes → the Run **continues** → **completed**.
5. Separately: launch another flagged Run → **deny** the parked step → the Run
   **aborts** fail-closed (no further steps); and/or **cancel** a running Run.
6. Confirm the audit timeline shows `run.launched` → per-step events → terminal
   `run.completed` / `run.aborted` / `run.canceled`, hash-intact.

## Step 7 — tag (after sign-off)  ⚠ local only, no remote

```sh
git checkout main
git tag -a v0.5.0 -m "v0.5.0 — Governed Workstreams (layer 1: Runs)"
git tag -l                                    # expect v0.1.0..v0.5.0
# DO NOT push — local annotated tag only (repo doctrine).
```
Then flip `status` in `release_notes_v0_5_0.md` + `release_manifest_v0_5_0.json` from
"release-candidate" to "released — tag v0.5.0 → <commit>".
