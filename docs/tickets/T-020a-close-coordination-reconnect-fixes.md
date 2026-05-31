---
name: T-020a Ticket
about: SSH e2e hardening (1/2) — close-coordination + reconnect fixes
title: "T-020a: fix session close-coordination audit gap + scope reconnect (v0.1.0)"
labels: ["ticket", "bug"]
---

## Ticket ID and link

T-020a — first half of T-020 (SSH e2e hardening). No B-NNN counterpart. Fixes two
defects found in a post-slice review of the end-to-end workflow (the slice's
tests all mocked the cross-service boundaries, so these integration bugs slipped
through). Pairs with **T-020b** (docker-compose live e2e) and **ADR-0049**
(records the decisions below).

## Goal

Fix the two real bugs the e2e review surfaced:
1. **Close-coordination audit gap** — ending a session via the UI/REST produced
   no `session.close` event and never recorded the cast SHA.
2. **Reconnect** — a transient WS drop permanently 409'd the session; scope this
   for v0.1.0 (drop ends the session; reconnect deferred to v0.2.0).

## Priority

P0 — release-blocking correctness (audit integrity). No code-affecting ADR
change; ADR-0049 ratifies the decisions.

## Files in scope

- Modify: `services/session-broker/internal/store/store.go` (finalize-once guard)
- Modify: `services/session-broker/internal/store/store_integration_test.go`
- Modify: `packages/ui/src/hooks/useWebSocketChannel.ts` (`reconnect` option)
- Modify: `packages/ui/src/components/TerminalView.tsx` (`reconnect: false`)
- Modify: `packages/ui/src/components/TerminalView.test.tsx`
- Modify: `apps/web/src/routes/SessionView.tsx` (ended-state notice)
- Create: `docs/tickets/T-020a-close-coordination-reconnect-fixes.md` (this file)
- Modify: `docs/tickets/README.md`

8 files. Within the ≤12-file / ≤500-LoC budget.

## The bugs (root cause)

**Bug 1 — UI-End emits no `session.close` + loses the cast SHA.** `apps/web` End →
`api.EndSession` sets `status='closed'`, but `services/api` has no outbox so it
emits **no** audit event. The subsequent broker WS teardown called
`store.Close` guarded by `WHERE status IN ('pending','active')` → the row was
already `closed` → 0 rows → `ErrNotActivatable` (swallowed). So `cast_sha256` was
never written and **no `session.close` event was emitted** on the primary end
path. (The tab-close path, where the WS dies before any REST end, happened to
work — inconsistent.)

**Bug 2 — reconnect permanently 409s.** On any WS drop the broker closed the
session (`status='closed'`); the SPA hook's auto-reconnect then loaded a closed
session → 409, and the cast writer's `O_EXCL` blocked re-attach anyway.

## Fixes (consult decisions, 2026-05-30)

- **Bug 1 — finalize-once guard.** `store.Close` now keys on
  `WHERE cast_sha256 IS NULL` (not status), with `ended_at`/`closed_reason`
  COALESCEd. The broker that held the live connection finalizes + audits exactly
  once, **regardless of whether `api.end` pre-set the status**. Every broker
  `Close` carries a non-empty cast SHA (the cast is opened before the SSH dial),
  so `cast_sha256 IS NULL` is a sound once-marker. Residual (documented): an
  *orphaned* session closed purely by `api.end` (no broker attached) still emits
  no `session.close` — api has no outbox; a reaper is future work.
- **Bug 2 — reconnect deferred to v0.2.0 (option B).** `useWebSocketChannel`
  gains a `reconnect` option (default true for SSE/other consumers);
  `<TerminalView>` passes `reconnect: false`. A broker drop ends the session
  (now properly audited via Bug 1's fix); `<SessionView>` surfaces an "ended"
  notice. ADR-0048 §item-8 always deferred reconnect/crash-recovery "to the SSH
  slice ticket" — we defer it explicitly to v0.2.0 (ADR-0049).

## Acceptance criteria

- [x] After an api-style pre-close (`status='closed'`, no audit, no cast SHA),
      the broker `Close` still records the cast SHA **and** emits exactly one
      `session.close` event anchoring it (integration test).
- [x] A second `Close` after finalize is a no-op (`ErrNotActivatable`).
- [x] The existing open→close lifecycle + cast-prune + input-line tests still
      pass with the new guard.
- [x] `<TerminalView>` passes `reconnect: false`; the hook honors it (no
      reconnect scheduled on close).
- [x] `<SessionView>` shows an "ended" notice when the channel closes.
- [x] Go: `gofmt`/`vet`/`golangci-lint` clean + store integration green.
      JS: `pnpm -r typecheck`/`test`/`lint` clean.

### Violating vs. passing fixtures

- **Status-guarded close — violating:** `WHERE status IN ('pending','active')`
  no-ops after `api.end` → lost close event + cast SHA. **Passing:**
  `WHERE cast_sha256 IS NULL` finalizes once regardless of status.
- **Auto-reconnect to a closed session — violating:** hook reopens → 409 loop.
  **Passing:** `reconnect:false` on the terminal; ended notice in the UI.

## Out of scope

- The docker-compose live e2e walkthrough → **T-020b**.
- ADR-0049 (audit content model + reconnect-deferral + `session.terminated`) +
  module-map refresh → follow-up task.
- Orphaned-session reaper (api-only close with no broker) → future.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 8 files, within budget.

## ADRs required

- None blocking; **ADR-0049** ratifies the reconnect-deferral + the audit content
  model (written immediately after this ticket).

## Verification

```sh
# Go: build/vet/gofmt + broker store integration + lint
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e BROKER_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 bash -c '
    test -z "$(gofmt -l services/session-broker)" &&
    (cd services/session-broker && go build ./... && go vet ./...) &&
    go test -tags integration ./services/session-broker/internal/store/... &&
    go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./services/session-broker/...'

# JS: typecheck + tests + lint
docker run --rm -e CI=true -v "$PWD:/work" -v aireos-pnpm:/pnpm-store -w /work node:22-alpine sh -c '
  corepack enable && corepack prepare pnpm@10.32.1 --activate &&
  pnpm install --frozen-lockfile --store-dir /pnpm-store &&
  pnpm -r run typecheck && pnpm -r run test && pnpm -r run lint'
```

Expected: `TestCloseFinalizesAfterApiPreClose` passes (cast SHA recorded + one
`session.close`); existing lifecycle/prune/input-line tests still green;
TerminalView asserts `reconnect:false`; all JS checks clean. (Confirmed
2026-05-30 on Docker / golang:1.23 / node:22-alpine.)
