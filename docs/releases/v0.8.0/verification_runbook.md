# v0.8.0 — verification runbook

How to verify the v0.8.0 "Persistent Scoped Memory" release candidate end to end, with
the recorded results from the RC verification. Commands assume the repo root on a
Windows host with Docker Desktop + WSL2 (prefix Docker invocations with
`MSYS_NO_PATHCONV=1` under git-bash); a Linux host drops the prefix.

Recorded RC state: `main` = `dev`, tree clean, local tags `v0.1.0..v0.7.0` (the
`v0.8.0` tag lands at T-086 closure). No remote exists; nothing is ever pushed.

## Step 0 — sync the RC

```bash
git status --short          # clean
git rev-parse main dev      # identical
```

## Step 1 — DOC-16 doc-check (paths resolve)

```bash
python infra/scripts/doc_check.py     # host python (not python3) on this box
```

Recorded: OK — every in-repo path referenced in CANONICAL + the ADRs resolves
(doc-check scans CANONICAL_SOURCE_OF_TRUTH.md + docs/adr/*.md).

## Step 2 — VER-23 code gates (Dockerized)

```bash
# Go (per module; golang:1.23):
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c \
  'go build ./... && go vet ./... && go vet -tags integration ./... && gofmt -l .'
docker run --rm -v "$PWD":/w -w /w/services/api golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./...
# JS (node:22-alpine; per-workspace — `pnpm -r typecheck` PATH-races on this host):
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && CI=true pnpm install --frozen-lockfile && \
   pnpm --filter @aireos/shared-types exec tsc --noEmit && \
   pnpm --filter @aireos/ui exec tsc --noEmit && \
   pnpm --filter @aireos/web exec tsc --noEmit && \
   pnpm -r lint && pnpm --filter @aireos/web test'
```

Recorded: all clean at every ticket (T-079..T-084); vitest 52/52 (incl. the 11 new
`Memories.test.tsx` cases).

## Step 3 — VER-24 codegen drift zero

```bash
task codegen
git diff --stat            # empty — gen.go + openapi.ts byte-deterministic
```

Recorded: byte-identical outputs proven twice (T-080, T-081 — sha256-compared) and
re-proven at the RC tree.

## Step 4 — VER-25 live legs (compose)

```bash
task e2e     # the full live suite incl. WD-8
task smoke   # 9 Playwright specs incl. memories.smoke.spec.ts
```

Recorded e2e: **GREEN — 24 tests + 2 subtests** (257.6s). The WD-8 legs:
`TestWD8HappyPath` (charter → the Run completes unattended → the accrued content-free
pointer with actor `workstream:<id>` → author → scoped read → redact-once → the
2s-sweep expiry leg: the unpinned row tombstones exactly once, the pinned sibling
survives ≥2 further ticks) + `TestWD8RedactIdempotent` (N1) +
`TestWD8AgentPlaneRejected` (N2 — a valid agent Bearer 401/401 at `/v1/memories*`;
`tools/list` = `remote.exec` + `run.launch` only) + `TestWD8OwnerScope404` (N3) +
`TestWD8TerminalOutcomesAccrue` (N4 — failed + canceled each exactly once). WD-5 +
WD-6 + WD-7 all re-proven in-suite (the elevated regression guard — the accrual hook
rides the shared terminal flip). `audit-service verify`: **chain intact, 434 events**
(do not assert an exact count — the ticker makes it nondeterministic; integrity is
the assertion). The e2e compose sets `AIREOS_MEMORY_SWEEP_SECONDS: "2"` +
`AIREOS_WORKSTREAM_MIN_INTERVAL_SECONDS: "5"`.

Recorded smoke: **9/9 GREEN** — the memories leg authors two keyed notes → content
renders → Redact (confirm accepted) → the tombstone renders with the content gone →
Pin → the badge appears; screenshots in the Playwright HTML report.

## Step 5 — OI-15 installed WD-8 (from-HEAD bundle)  ⚠ rebuild-from-HEAD

```bash
# 1. Rebuild the bundle FROM HEAD as 0.8.0 (the t033 lesson — never run wd1 on a
#    stale bundle). Delete any older core tarball first (single-tarball glob):
rm -f dist/offline/aireos-offline-*.tar.gz
# host lacks the linux rollup binary → build the SPA in node:22-alpine, then:
BUNDLE_VERSION=0.8.0 task offline-bundle
# Recorded: 116 MB unpacked / 41 MB gz — the OI-4 caps (500/150) hold.

# 2. The installed walkthrough (systemd-in-Docker on WSL2):
task wd1
```

Recorded: **GREEN, exit 0** — `TestWedgeDemoInstalledStack` + `TestWD5/6/7/8InstalledStack`
+ `TestInstalledInvocationApprovalWD3`; the WD-8 leg 150.4s at the FULL production
posture (accrual off a 60s-floor schedule tick with nobody present; the expiry leg at
the real 2-minute reaper cadence — no `AIREOS_MEMORY_SWEEP_SECONDS` on the install);
suite 235.3s against the **900s** go-test timeout (raised from 600s this release —
WD-8 adds schedule+sweep wall-clock). `audit chain intact: 103 events` + the full
WANTS list including `memory.written` / `memory.redacted` / `memory.expired`.
**Zero install-wiring diff this release** — migration 0012 rides the migrate loop;
the sweep rides the api reaper goroutine that starts unconditionally.

Note (harness trust, this release): `run-wd1.sh` now exports Caddy's local CA root
from the stack container and the harness **verifies** TLS against it
(`newWD1TLSClient`; `WD1_CA_FILE`) — `InsecureSkipVerify` is gone from `tests/`
entirely. Per-id event assertions (actor / exactly-once) ride the api audit
read-proxy (`GET /v1/audit/events`); chain integrity stays host-internal
(`audit-service verify`).

## Step 6 — CEO §human WD-8 walkthrough  (the remaining gate)

Through the rendered cockpit on a live stack (the smoke stack inherits the e2e env,
incl. the 2s sweep — recommended for a human-paced session):

1. `/workstreams` — charter a Workstream (the GOV-8 consent copy) → watch a tick fire
   with nobody present → its Run completes.
2. `/workstreams` detail — the **Scoped memory** section shows the accrued
   content-free pointer beside the EVD-4 run history (nobody wrote it).
3. `/memories` — author a note on that scope; read the GOV-9 consent copy (sealed +
   operator-only; what Redact does NOT reach).
4. **Redact** it (the confirm restates the R2 boundary) → the row renders the
   **Redacted** tombstone, content gone.
5. Author two more — **pin** one, give the other a short expiry → watch the sweep
   tombstone the unpinned one (**Expired**) while the pinned one survives.
6. **Export** the scope → the markdown manifest downloads.
7. `/audit` — the `memory.*` events are present, attributed, and content-free.

Sign-off → the T-086 closure commit flips the notes + manifest to **released** and
applies the local annotated tag.

## Step 7 — tag (after sign-off)  ⚠ local only, no remote

```bash
git tag -a v0.8.0 -m "v0.8.0 Persistent Scoped Memory"
# DO NOT push — local annotated tag only (repo doctrine).
```
