---
name: T-032 Ticket
about: Governed Egress Foundation — api-owned stuck-session reaper
title: "T-032: stuck-session reaper (close stranded pending / pending_approval sessions)"
labels: ["ticket", "api", "durability"]
---

## Ticket ID and link

T-032 — the **api-owned stuck-session reaper**, the reaper/sweep ADR-0049
anticipated ("a reaper/sweep is future work"). Closes the **T-031b carry-forward**:
a `pending_approval` session whose operator never acts is auto-denied by
policy-service at ~120s, but policy cannot write the api session row, so the
session sat stuck. api-only; no contract/migration/codegen/broker change.

## Goal

A serve-time goroutine periodically closes sessions stranded in a PRE-OPEN state:
a `pending` session no broker attached to (→ `ttl`) or a `pending_approval` session
whose approval was never resolved (→ `approval_timeout`). Status-only — these
never opened, so no `session.close` is emitted.

## Priority

P1 — v0.2.0 spine hygiene; the last code ticket before T-033 (WD-2).

## Files in scope (5)

- Modify: `services/api/internal/store/sessions.go` (`ReapStale`)
- Modify: `services/api/internal/store/store_integration_test.go` (`TestReapStale`)
- Modify: `services/api/main.go` (serve-time reaper goroutine + TTL config)
- Create: `docs/tickets/T-032-session-reaper.md` (this) + `docs/tickets/README.md` status

## Design (from the consult, CEO-approved)

- **api-owned** (not broker): these are api-created, pre-open rows the broker never
  saw; api owns the sessions table + already has the close machinery. The broker
  (active sessions, casts, `session.close`) stays untouched.
- **`ReapStale(pendingCutoff, approvalCutoff)`** — one UPDATE; `pending`→`ttl`,
  `pending_approval`→`approval_timeout` (**existing** closed_reason values, no
  contract change). **Idempotent** — a reaped row is `closed`, excluded next sweep.
- **Status-only, no `session.close`** — never-opened sessions have no `session.open`
  to pair with; the denial is already audit-chained by policy-service; api has no
  session outbox.
- **ACTIVE sessions are never reaped** — a live one is indistinguishable from a
  broker-crashed one without a heartbeat. The broker's idle-30m timer handles live
  sessions.
- **TTLs:** pending 30m, pending_approval 5m (> the 120s policy timeout), tick 2m;
  env overrides `API_REAP_PENDING_MINUTES` / `API_REAP_APPROVAL_MINUTES`. Best-effort.

## Deferred to v0.3.0 (residuals — recorded here per the consult)

- **(A) active-orphan audit-close** — a session that *opened* (`session.open`
  emitted) but whose broker **crashed** before `Close` leaves a dangling open with
  no `session.close` (the ADR-0049 orphan-close gap). Closing it safely needs a
  **broker heartbeat** to tell a live active session from a crashed one — a real
  feature, not a rider. This reaper deliberately leaves `active` sessions alone.
- **WS-drop reconnect** — the only ADR-0049-conforming form (fresh-shell = a new
  session) would re-trigger the approval prompt on every transient blip for
  `requires_approval` resources; true resume contradicts ADR-0049 §3 + the
  single-cast-SHA anchor (would need a new ADR). Deferred to a dedicated design pass.

## G1 / G2

- **G1** — `ReapStale` is store-integration-verified against real Postgres (not a
  cross-service boundary; no live-proof deferral needed).
- **G2** — conforms ADR-0049 (this IS its anticipated reaper); no ADR edited; the
  (A) audit-close stays a documented residual.

## Verification (Dockerized golang:1.23 + compose-mvp Postgres)

build/vet `./services/api/...`; golangci-lint; `go test ./services/api/internal/server/...`
(regression); `migrate up` stays **version 5** (no new migration); store integration
incl. `TestReapStale` (stale pending+pending_approval closed with ttl/approval_timeout;
fresh + active untouched; 2nd sweep returns 0).

## Definition of done

`ReapStale` + the goroutine wired + verified; residuals recorded. Commit on `dev`,
ff `main`. No push. After this, **only T-033 (WD-2) remains for v0.2.0.**
