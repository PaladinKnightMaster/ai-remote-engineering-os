# v0.10.0 — verification runbook

How to verify the v0.10.0 "Live/Context Index" release candidate end to end, with
the recorded results from the RC verification. Commands assume the repo root on a
Windows host with Docker Desktop + WSL2 (prefix Docker invocations with
`MSYS_NO_PATHCONV=1` under git-bash); a Linux host drops the prefix.

Recorded RC state: `main` = `dev`, tree clean, local tags `v0.1.0..v0.9.0` (the
`v0.10.0` tag lands at T-104 closure). No remote exists; nothing is ever pushed.

## Step 0 — sync the RC

```bash
git status --short          # clean
git rev-parse main dev      # identical
```

## Step 1 — DOC-18 doc-check (paths resolve)

```bash
python infra/scripts/doc_check.py     # host python (not python3) on this box
```

Recorded: OK — every in-repo path referenced in CANONICAL + the ADRs resolves
(doc-check scans CANONICAL_SOURCE_OF_TRUTH.md + docs/adr/*.md).

## Step 2 — VER-29 code gates (Dockerized)

```bash
# Go (explicit per-module paths — `go.work` means a bare `./...` from the repo
# root fails with "directory prefix . does not contain modules listed in
# go.work"; list every workspace module, mirroring .github/workflows/ci.yml's
# explicit-path pattern — this fix landed at T-103, correcting the v0.8.0/
# v0.9.0 runbooks' bare-`./...` bug too):
GOMOD='./packages/go/auth/... ./packages/go/middleware/... ./packages/go/migrate/... ./packages/go/outbox/... ./services/api/... ./services/audit-service/... ./services/connector-service/... ./services/mcp-gateway/... ./services/policy-service/... ./services/session-broker/...'
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c \
  "go build $GOMOD && go vet $GOMOD && go vet -tags integration $GOMOD && gofmt -l ."
docker run --rm -v "$PWD":/w -w /w/services/api golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./...
# (repeat the lint line with -w /w/services/mcp-gateway — both modules changed at v0.10)
# JS (node:22-alpine; per-workspace):
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && CI=true pnpm install --frozen-lockfile && \
   pnpm --filter @aireos/shared-types exec tsc --noEmit && \
   pnpm --filter @aireos/ui exec tsc --noEmit && \
   pnpm --filter @aireos/web exec tsc --noEmit && \
   pnpm -r lint && pnpm --filter @aireos/web test'
```

Recorded: clean at every ticket (T-096..T-102); vitest green incl. the new
context-sources cases (71/71 on `apps/web`, 19 new/updated Workstreams cases).
`task licenses` shows zero new third-party dependency (N5 — the native
Postgres FTS posture holds).

## Step 3 — VER-30 codegen drift zero

```bash
task codegen
git diff --stat            # empty — gen.go + openapi.ts byte-deterministic
```

Recorded: byte-identical outputs (two consecutive codegen runs, identical
hashes, proven at T-100a). The agent-plane `context.search` tool + its internal
route are MCP + internal RPC — deliberately NOT in the fragment (the
ADR-0056/0058 internal-RPC rule).

## Step 4 — VER-31 live legs (compose)

```bash
task e2e     # the full live suite incl. the WD-10 capstone
task smoke   # 12 Playwright specs incl. the new context-sources leg
```

Recorded e2e: **PASS — 366.8s**. The WD-10 legs (`TestWD10HappyPath`-style
coverage): a charter-bound context source is registered → the in-process
indexer sweeps it deterministically (native FTS, no network) → the source
shows an up-to-date staleness signal → the tick publishes → the composer's
chained `memory.read` **and** `context.search` reads (each chaining its own
content-free event) → the composer composes from the retrieved chunk and
submits → the plan parks as a `proposed` Run with the full plan visible on
the operator GET → approve → the unchanged engine drives it to completion →
exactly one accrual → **a live edit to the source file is reflected on the
very next `context.search` without a full rebuild** (the staleness signal
flips then clears) → `audit-service verify`: chain intact. Plus the six live
negatives: **N1** cross-grant/foreign-source containment (a foreign
`workstreamId` answers empty, never an error); **N2** the confined-root guard
(a symlink/`../` escape inside a registered source is neither indexed nor
read); **N3** staleness honesty (an edited-but-unswept source surfaces
**stale** to both composer and operator; the next sweep clears it); **N4**
per-read chaining, fail-loud, including zero-result calls; **N5** no
embedding/egress/network/new dependency (`task licenses` clean; the index is
native PG FTS only); **N6** a plan grounded in indexed content still **parks**
(the ADR-0065 always-park invariant carries — indexed content is untrusted).
WD-5/6/7/8/9 all green **byte-unchanged** (the elevated regression guard).
`audit-service verify`: **chain intact — 616 events** this run.
`AIREOS_CONTEXT_INDEX_SECONDS=6` is set in the e2e compose environment only
(the `AIREOS_MEMORY_SWEEP_SECONDS` precedent) so the sweep proves in seconds.

> **Chain counts are observed values, not exact assertions, since v0.9's
> precedent extends to v0.10**: schedule-driven and sweep-driven legs emit
> tick-timing-dependent events. Integrity + per-leg exact counts are the
> assertions; never gate on a global N.

Recorded smoke: **12/12 GREEN** — 11 pre-existing specs plus the new
context-sources spec (the register/remove flow with the GOV-11 confirm copy
visible in both dialogs, the staleness badge, the section render on the
Workstreams charter detail).

## Step 5 — OI-17 installed WD-10 (from-HEAD bundle) ⚠ rebuild-from-HEAD

```bash
# 1. Rebuild the bundle FROM HEAD as 0.10.0 (the t033 lesson — never run wd1 on
#    a stale bundle). Delete any older core tarball first:
rm -f dist/offline/aireos-offline-*.tar.gz
# host lacks the linux rollup binary -> build the SPA in node:22-alpine, then:
BUNDLE_VERSION=0.10.0 task offline-bundle
# Recorded: 116 MB unpacked / 41 MB gz — the OI-4 caps hold (identical to
# v0.8/v0.9). Migrations 0014+0015 ride the embedded migrate loop.

# 2. Independently confirm the zero-install-wiring-diff claim (OI-17):
git diff v0.9.0 HEAD -- infra/offline/install.sh infra/offline/systemd/ infra/offline/Caddyfile
# Recorded: EMPTY. The only infra/offline/ change since v0.9.0 is run-wd1.sh
# (test-harness wiring — a new harness-owned Docker volume standing in for
# the compose e2e bind-mount, since the installed api runs as a systemd
# service with no repo checkout to bind-mount from).

# 3. The installed walkthrough (systemd-in-Docker on WSL2):
task wd1
```

Recorded: **GREEN, exit 0** — `TestWD10InstalledStack` (101.46s: the full
happy path, legs 1–8, including the freshness edit reflected on the next
sweep, and **double governance live**: the plan-level park plus a flagged
resource's invocation-level park inside the approved plan) +
`TestWD10ConfinedRootInstalled` (60.19s; N2, the cheap negative pick per the
criteria's "one key negative" allowance) — both at the **TRUE production
posture** (the 60s adaptive-schedule floor AND the 60s context-indexer sweep
floor, **neither overridden**). Whole suite **651.6s against the 1500s**
go-test timeout (43%). Chain intact — **222 events** this run, including
`context.source.registered`/`context.read` in the WANTS list alongside the
full v0.9 vocabulary. **Zero install-wiring diff this release** — migrations
0014+0015 ride the migrate loop; the indexer rides the already-running api
goroutine; `context.search` rides the already-installed gateway binary +
Caddy `/mcp` route.

Notes (harness, this release): the installed test mounts a new
harness-owned Docker volume (`aireos-wd1-context`) at the same path on both
`$HOST` and the Go test-runner container (`WD1_CONTEXT_ROOT`) so the
installed api process — a systemd service with no repo checkout — has a real
host directory to index, standing in for the compose e2e bind-mount that
only exists there. This is wd1 test-harness wiring only (never
`install.sh`/systemd/Caddy/env) and does not touch the OI-17 claim (see Step
5.2 above).

## Step 6 — CEO §human WD-10 walkthrough (the remaining gate)

Through the rendered cockpit on a live stack (the smoke stack inherits the
e2e env — a 6s indexer floor + 5s tick interval — recommended for a
human-paced session; a from-scratch stack charters at the real 60s floor for
calmer pacing but a longer wait for the first sweep/tick). The composer side
needs agent machinery the browser cannot provide: the same small
**agent-driver helper** introduced at v0.9 (minting a composer token and
driving `workstream.poll`/`plan.submit`/`memory.read` against the stack's
`/mcp`), extended to also call `context.search` — the human drives everything
else in the browser.

> **Windows-host browser access (recorded during the v0.9 §human session —
> read BEFORE exposing a stack to a host browser; this route is unchanged
> for v0.10).** Docker Desktop's Windows/WSL2 HTTPS port-forwarding corrupted
> the TLS handshake for every OS-native client (Chrome, Edge, `curl.exe`/
> schannel all failed with `ERR_SSL_PROTOCOL_ERROR`-class errors) while
> in-network clients and minimal TLS stacks (`openssl s_client`, the
> Playwright container) worked fine — the stack itself was healthy
> throughout. Also observed on the same host: IPv6 loopback (`::1`) TCP
> connects fail generally, and a stale `wslrelay.exe` can squat a published
> port on `[::1]`. **The working §human access route** (no TLS on the host
> path at all): publish `api:8080` + `broker:8082` plain-HTTP via a compose
> override, then run the SPA's own dev proxy natively on the host —
> `pnpm exec vite --host 127.0.0.1` in `apps/web` (the ADR-0047 §10 dev
> posture; bind IPv4 explicitly) — and browse `http://localhost:5173`. Two
> helper pitfalls carried from v0.9: `POST /v1/agent-tokens` auto-prefixes
> the subject as `agent:<agentId>` — pass the BARE id when minting for a
> composer chartered as `agent:<name>`; and the composer token's 5-minute
> TTL needs a re-mint before each human-paced step.

1. `/workstreams` — charter an **adaptive** Workstream (or reuse one from a
   prior session): name the composer, read the consent copy.
2. The charter detail — register a **context source**: point it at a
   local/mounted directory of docs/runbooks (e.g. a `docs/` subtree checked
   out on the host); read the **GOV-11** consent copy plainly stating that
   this charter's composer agent will be able to read the source's content
   (the prompt-injection risk named honestly), and that removal stops future
   reads but cannot recall past ones. Confirm.
3. Watch the **staleness signal**: initially "never indexed / pending," then
   flips to an up-to-date state once the in-process indexer's next sweep
   completes (the sweep floor governs the wait — shorter on the smoke/e2e
   env, the real 60s floor on a from-scratch install).
4. The charter detail — **a plan request opens** (the tick, unattended).
5. The agent-driver answers it: `memory.read` (v0.9) **and `context.search`**
   (v0.10) — a query that should surface a chunk from the registered source —
   then `plan.submit` → `/runs` shows a **Proposed** Run.
6. Open it — **the informed preview**: every step's resource + verbatim
   command, composed by the agent, readable before anything runs. **Approve**
   (the confirm restates whose plan this is) → watch the unchanged engine
   drive it.
7. **Freshness proof**: edit the registered source file on the host (append a
   line, save) — the composer's *next* `context.search` (a further
   agent-driver call, or the next tick's cycle) returns the edited content
   **without waiting for a full rebuild**; the source's staleness signal on
   the charter detail flips to stale immediately after the edit, then clears
   after the next sweep.
8. `/audit` — the full loop vocabulary, attributed and content-free:
   `context.source.registered` (Step 2) → `plan.requested` → `memory.read` /
   `context.read` (both chained, one event per call) → `plan.submitted` →
   `run.launched` (at approve) → `run.*` → `memory.written`.
9. A negative to observe live if time allows: register a **second** charter's
   context source, then confirm the first charter's `context.search` never
   surfaces a chunk from it (N1, no oracle — the query simply returns
   whatever the caller's own charter is entitled to).

**Recorded result: PASSED — driven live and hands-on by the CEO, 2026-07-24, and
signed off.** All nine legs on the rendered cockpit:

1. **Adaptive charter** `wd10-context-demo` — composer `agent:wd10-composer`, scope
   `e2e-target-workstream`, interval 5s; the consent copy naming *who composes* and the
   always-parks promise read before the click.
2. **Context source** `/work/docs/architecture` registered behind the **GOV-11 dialog**
   (prompt-injection + exfiltration risk named plainly); `context.source.registered`
   chained content-free (`operator:admin` · `human-agent`, evidence anchor
   `workstream:<id>`, the root path **never** in the event).
3. **Staleness → indexed**: the in-process indexer swept it into **76 chunks across 11
   files** (native PG FTS, no network, no embedding).
4. **Tick** opened plan requests continuously (TTL = interval; expired ones superseded by
   fresh ones — the never-wedges behavior visible live).
5. **Composer read + submit**: `context.search "audit chain"` → **5 ranked chunks** from
   3 real docs, chaining exactly one **content-free `context.read`** on the
   **`agent-workspace`** edge (target = chunk ids only; the query text and document
   content never entered the chain). `plan.submit` created a **`proposed`** Run —
   verified inert: step `pending`, and **no `run.launched` / no `remote.exec`** at that
   point.
6. **Informed preview + approval**: the operator read the agent's **verbatim command**
   and the "nothing runs until you decide / Deny is equally first-class" copy, then
   approved → `pending` → the unchanged sequencer drove it to **`completed`**; the
   terminal Run accrued **one content-free pointer** (`content IS NULL`).
   **`run.launched` chained at approval (seq 516), well after `plan.submitted` (seq
   458)** — the deferred-launch semantics proven in the chain.
7. **Freshness, both directions**: a new file in the registered source was returned by
   the very next `context.search` **~5s later with no rebuild call**; deleting it removed
   it from the index on the next sweep.
8. **Audit**: the full loop present, attributed, content-free — `context.source.registered`
   → `plan.requested` → `context.read` → `plan.submitted` → `run.launched` →
   `tool.invoked` → `credential.leased` → `remote.exec` (`sha256:…` stdout anchor, never
   the output) → `tool.result` → `run.completed` → `memory.written`.
9. **N1 cross-grant containment — proven with a positive control** (a second charter B
   with its own composer and its own bound source): **B reads its own token (1 chunk)**;
   **A gets empty for B's content**; **A gets empty — not an error — when passing B's own
   `workstreamId`** (no oracle: A cannot even learn B's charter exists); **B gets empty
   for A's content** (symmetric). All **9 `context.read` events chained**, including the
   zero-result ones (`target = "context:"`, no ids) — fail-loud per-read auditing holds
   even when nothing is returned.

Fixtures created during the walkthrough (a freshness probe file and charter B's source
directory) were deleted; the repo tree was clean and `main == dev` unchanged throughout.

**Findings raised (three, none a v0.10 defect, none blocking sign-off)** — the native
`window.confirm` consent modals should become a design-system modal; the charter-detail
lists should auto-refresh; and **`/audit` needs a newest-first default + column sorters,
flagged v1.0-blocking** (measured live: 635 events, 601 of them ticker noise, newest
entries 13 pages deep). Full detail and implementation notes in
`release_notes_v0_10_0.md` §"§human findings".

> **Browser-pane caveat discovered during this session (record it — the v0.9-TLS-note
> class).** The Claude Desktop **Browser pane auto-dismisses `window.confirm` (returns
> `false`)**, so every confirm-gated consent action silently cancels there — zero
> `/v1/context-sources` requests reached the api while it was in use, with no error shown.
> Drive the §human walkthrough in a **real host browser** (Chrome/Edge at
> `http://localhost:5173`), where the dialog renders and the action completes.

## Step 7 — tag (after sign-off) ⚠ local only, no remote

```bash
git tag -a v0.10.0 -m "v0.10.0 Live/Context Index"
# DO NOT push — local annotated tag only (repo doctrine).
```
