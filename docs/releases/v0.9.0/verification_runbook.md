# v0.9.0 — verification runbook

How to verify the v0.9.0 "Adaptive Workstreams" release candidate end to end, with the
recorded results from the RC verification. Commands assume the repo root on a Windows
host with Docker Desktop + WSL2 (prefix Docker invocations with `MSYS_NO_PATHCONV=1`
under git-bash); a Linux host drops the prefix.

Recorded RC state: `main` = `dev`, tree clean, local tags `v0.1.0..v0.8.0` (the
`v0.9.0` tag lands at T-095 closure). No remote exists; nothing is ever pushed.

## Step 0 — sync the RC

```bash
git status --short          # clean
git rev-parse main dev      # identical
```

## Step 1 — DOC-17 doc-check (paths resolve)

```bash
python infra/scripts/doc_check.py     # host python (not python3) on this box
```

Recorded: OK — every in-repo path referenced in CANONICAL + the ADRs resolves.

## Step 2 — VER-26 code gates (Dockerized)

```bash
# Go (explicit per-module paths — `go.work` means a bare `./...` from the repo
# root fails with "directory prefix . does not contain modules listed in
# go.work"; list every workspace module, mirroring .github/workflows/ci.yml's
# explicit-path pattern):
GOMOD='./packages/go/auth/... ./packages/go/middleware/... ./packages/go/migrate/... ./packages/go/outbox/... ./services/api/... ./services/audit-service/... ./services/connector-service/... ./services/mcp-gateway/... ./services/policy-service/... ./services/session-broker/...'
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c \
  "go build $GOMOD && go vet $GOMOD && go vet -tags integration $GOMOD && gofmt -l ."
docker run --rm -v "$PWD":/w -w /w/services/api golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./...
# (repeat the lint line with -w /w/services/mcp-gateway — both modules changed at v0.9)
# JS (node:22-alpine; per-workspace):
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && CI=true pnpm install --frozen-lockfile && \
   pnpm --filter @aireos/shared-types exec tsc --noEmit && \
   pnpm --filter @aireos/ui exec tsc --noEmit && \
   pnpm --filter @aireos/web exec tsc --noEmit && \
   pnpm -r lint && pnpm --filter @aireos/web test'
```

Recorded: clean at every ticket (T-087..T-093); vitest green incl. the new
Memories/Workstreams/Runs cases (the informed-preview component is fixture-covered —
its browser evidence is Step 6's, since only an agent-plane `plan.submit` can produce
a `proposed` Run).

## Step 3 — VER-27 codegen drift zero

```bash
task codegen
git diff --stat            # empty — gen.go + openapi.ts byte-deterministic
```

Recorded: byte-identical outputs proven at T-088 and T-091. The agent-plane tools
(`workstream.poll`/`plan.submit`/`memory.read`) are MCP + internal routes —
deliberately NOT in the fragment (the ADR-0056/0058 internal-RPC rule).

## Step 4 — VER-28 live legs (compose)

```bash
task e2e     # the full live suite incl. the WD-9 capstone
task smoke   # 11 Playwright specs incl. the two new v0.9 legs
```

Recorded e2e: **GREEN — 32 tests** (297.5s). The WD-9 legs: `TestWD9HappyPath` (the
closed loop — adaptive charter → the tick opens a plan request with nobody present →
the composer's chained `memory.read` → `plan.submit` → the `proposed` park with the
full-plan preview on the operator GET → approve → the deferred `run.launched` → the
unchanged engine completes it → exactly one accrual → **the NEXT tick's read returns
the accrued pointer** — plus the deny cycle: `run.denied` + `canceled` + exactly one
accrual, and the SEC-29 out-of-grant refusal) + `TestWD9CrossGrant` (no-oracle
containment — a foreign `workstreamId` answers EMPTY, never an error) +
`TestWD9ShareGate` (invisible → shared → visible with content → unshared →
invisible; no duplicate events) + `TestWD9RevokeKillsAll` (the strongest form: the
cascade cancels the `proposed` Run with `run.canceled` and ZERO `run.denied`; the
open request closes; poll/submit/read all die; approve is refused) +
`TestWD9ExpiryNeverWedges`. WD-5/6/7/8 all green **byte-unchanged** (the elevated
guard; WD-8's N2 now asserts the exact five-tool set — no memory tool other than
`memory.read`). `audit-service verify`: **chain intact** — 518 events this run.

> **Chain counts are range-stable, not exact, since v0.9**: schedule-driven legs
> emit tick-timing-dependent `plan.requested`/`plan.expired` events (observed 518
> and 515 across two independent green runs). Integrity + per-leg exact counts are
> the assertions; never gate on a global N.

> **Concurrency note (recorded during T-092):** two `task e2e` runs sharing the
> default compose project name race on shared fixture ids and produce failures that
> look like regressions. Serialize verification runs, or isolate with
> `docker compose -p <unique>`.

Recorded smoke: **11/11 GREEN** — incl. the share flip (author → share with the
GOV-10 confirm copy visible → the badge → unshare) and the adaptive charter leg (the
charter form with the composer consent copy → **the first tick opens a plan request
with nobody present**, rendered on the detail at the real 60s floor).

## Step 5 — OI-16 installed WD-9 (from-HEAD bundle)  ⚠ rebuild-from-HEAD

```bash
# 1. Rebuild the bundle FROM HEAD as 0.9.0 (the t033 lesson — never run wd1 on a
#    stale bundle). Delete any older core tarball first:
rm -f dist/offline/aireos-offline-*.tar.gz
# host lacks the linux rollup binary → build the SPA in node:22-alpine, then:
BUNDLE_VERSION=0.9.0 task offline-bundle
# Recorded: 116 MB unpacked / 41 MB gz — the OI-4 caps hold (identical to v0.8).

# 2. The installed walkthrough (systemd-in-Docker on WSL2):
task wd1
```

Recorded: **GREEN, exit 0** — `TestWD9InstalledStack` at the TRUE production posture
(the 60s schedule floor, no env overrides): three staggered adaptive charters prove
the loop-closure happy path — including **double governance live** (the plan-level
park PLUS a flagged resource's invocation-level park inside the approved plan) — the
deny cycle, N1+N3 packed (cross-grant, then revoke-kills-all), N2, and the full N4
expiry leg (C5). WD-9 leg 252.6s; whole suite 487.7s against the **1500s** go-test
timeout (raised from 900s this release — WD-9's production-floor legs add
wall-clock). Chain intact (156 events this run) + the full WANTS list including
`plan.requested`/`plan.submitted`/`plan.expired`/`memory.read`/`memory.shared`/
`memory.unshared`/`run.denied`. **Zero install-wiring diff this release** —
migration 0013 rides the migrate loop; the three tools ride the already-installed
gateway binary + Caddy `/mcp` route.

Notes (harness, this release): composer legs at the 60s floor can outlive the
5-minute agent-token TTL — `wd9TokenSource` re-mints ahead of expiry (the TTL itself
is untouched; the pattern for any long-running installed test). The installed test
deliberately packs N1/N2's reads onto one composer identity for the time budget, so
composer read *counts* are recorded, not gated — the loop-closure proof is the
accrued pointer's content check. Do not "fix" that back to an exact count.

## Step 6 — CEO §human WD-9 walkthrough  (the remaining gate)

Through the rendered cockpit on a live stack (the smoke stack inherits the e2e env —
5s interval floor — recommended for a human-paced session; the recorded session
chartered at a 60s interval for calmer pacing). The composer side needs agent
machinery the browser cannot provide: a small **agent-driver helper** (minting a
composer token and driving `workstream.poll`/`plan.submit`/`memory.read` against the
stack's `/mcp`) — the human drives everything else in the browser.

> **Windows-host browser access (recorded during the §human session — read BEFORE
> exposing the smoke stack to a host browser).** Docker Desktop's Windows/WSL2 HTTPS
> port-forwarding corrupted the TLS handshake for every OS-native client (Chrome,
> Edge, `curl.exe`/schannel all failed with `ERR_SSL_PROTOCOL_ERROR`-class errors)
> while in-network clients and minimal TLS stacks (`openssl s_client`, the Playwright
> container) worked fine — the stack itself was healthy throughout. Also observed on
> the same host: IPv6 loopback (`::1`) TCP connects fail generally, and a stale
> `wslrelay.exe` can squat a published port on `[::1]`. **The working §human access
> route** (no TLS on the host path at all): publish `api:8080` + `broker:8082`
> plain-HTTP via a compose override, then run the SPA's own dev proxy natively on the
> host — `pnpm exec vite --host 127.0.0.1` in `apps/web` (the ADR-0047 §10 dev
> posture; bind IPv4 explicitly) — and browse `http://localhost:5173`. Two helper
> pitfalls: `POST /v1/agent-tokens` auto-prefixes the subject as `agent:<agentId>` —
> pass the BARE id when minting for a composer chartered as `agent:<name>`; and the
> composer token's 5-minute TTL needs a re-mint before each human-paced step.

1. `/workstreams` — charter an **adaptive** Workstream: the kind picker hides the
   step editor; name the composer; read the consent copy (this agent will author
   each tick's plan; plans always park for your review; it can read this charter's
   accrued memory + anything you explicitly share).
2. The charter detail — **a plan request opens with nobody present** (the tick).
3. The agent-driver answers it (poll → read → submit) → `/runs` shows a
   **Proposed** Run.
4. Open it — **the informed preview**: every step's resource + verbatim command,
   composed by the agent, readable before anything runs. **Approve** (the confirm
   restates whose plan this is) → watch the unchanged engine drive it.
5. The accrued content-free pointer appears on `/memories` scoped to **`run:<runID>`**
   (an adaptive charter's Run accrues at `run` scope — its actor is the composer, not
   `workstream:<id>`, so the charter detail's workstream-scoped widget shows authored
   rows, not these accruals; the composer's read plane unions both scopes by design);
   the agent-driver's next read (visible in its output) **returns it — the loop**.
6. `/memories` — author a note on the charter's scope → the agent-driver's read
   does NOT see it → **Share** (the confirm names the injection/exfiltration risk)
   → the next read sees it → **Unshare**.
7. A second cycle: let the driver submit again → **Deny** in the preview → the Run
   cancels; `/audit` shows `run.denied` beside the terminal event.
8. `/audit` — the full loop vocabulary, attributed and content-free:
   `plan.requested` → `memory.read` → `plan.submitted` → `run.launched` (at
   approve) → `run.*` → `memory.written`.

**Recorded result (2026-07-21): SIGNED OFF.** The CEO drove the walkthrough LIVE and
hands-on (browser via the dev-proxy route above; the composer driven on cue by the
throwaway agent-driver script), beyond the pre-captured screenshots (which remain at
`tests/smoke/test-results/wd9/`, 8 frames, not git-archived). Observed live: the
unattended tick; the informed plan preview + approve; **double governance** (the
flagged resource's invocation-level park firing INSIDE the approved plan — approved
at `/invocations` as a second, separate consent); **four distinct terminal outcomes
each accruing exactly one content-free pointer** — completed, failed (the
invocation-approval window lapsed during human-paced review: fail-closed, by
design), invocation-denied, and plan-denied (`run.denied` + `run.canceled` chained
together, step never dispatched); the loop closure (the composer's next read
returned the accrued pointers); the share gate both directions; the attributed,
content-free audit trail incl. the unattended `plan.requested`/`plan.expired`
cadence continuing safely after the session ended (N4 live). Honest UX observations
recorded for a future ergonomics pass (none release-blocking): the memory page's
scope fields need a manual **Refresh** to refetch (no auto-refetch on scope edit),
and `/audit` still pages oldest-first with no jump-to-newest (the v0.8 observation
carried). Sign-off → this closure commit flips the notes + manifest to **released**
and applies the local annotated tag.

## Step 7 — tag (after sign-off)  ⚠ local only, no remote

```bash
git tag -a v0.9.0 -m "v0.9.0 Adaptive Workstreams"
# DO NOT push — local annotated tag only (repo doctrine).
```
