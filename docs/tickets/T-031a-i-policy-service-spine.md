---
name: T-031a-i Ticket
about: Governed Egress Foundation — policy-service spine (schema + outbox + serve)
title: "T-031a-i: policy-service spine (schema + outbox + serve/healthz)"
labels: ["ticket", "policy", "scaffolding"]
---

## Ticket ID and link

T-031a-i — first slice of **T-031** (policy/approval decision point, ADR-0054).
The policy-service backend is connector-sized, so T-031 runs as three
deliverable-cohesive rungs (backend / api-seam / UI) and the backend self-split
**a-i / a-ii** at the build consult — drawn along the proven connector
**T-029b-i / T-029b-ii** line so the audited store methods ship *with* their RPC
consumers (never written twice). This slice is the **spine**. Conforms ADR-0054
(§6 own-tables, same pattern as connector-service) + ADR-0043/0044/0045.

## Goal

Stand policy-service up: it migrates its schema, serves an ungated `/healthz`, and
runs an in-process audit relay. **No decision logic, no store methods, no gated
RPC** — those are T-031a-ii.

## Priority

P1 — v0.2.0 "Governed Egress Foundation" spine (the **approval** primitive, the
last gate before bucket-B work per CLAUDE.md doctrine).

## Files in scope (12)

- Create: `services/policy-service/go.mod`, `go.sum` (new module)
- Modify: `go.work`, `go.work.sum` (add `./services/policy-service`)
- Create: `services/policy-service/migrations/0001_policy.{up,down}.sql` (both tables in one initial-schema migration)
- Create: `services/policy-service/internal/server/server.go` + `server_test.go` (ungated `/healthz`)
- Create: `services/policy-service/main.go` (`serve` / `migrate` / `outbox relay`)
- Modify: `services/policy-service/README.md` (overwrite the scaffold)
- Create: `docs/tickets/T-031a-i-policy-service-spine.md` (this file) + `docs/tickets/README.md` status

## Schema (`0001_policy`) — the shapes confirmed at consult

- **`pending_approvals`**: `id` uuidv7 PK · `session_id` text UNIQUE (soft ref to the api session, no cross-service FK) · `subject` (requesting operator) · `resolved_by` NULL (distinct from subject — seats segregation-of-duties in v0.3.0 with no migration) · `edge` (gated action's edge) · `target` (`resource:<id>`) · `action` · `status` CHECK∈(`pending,granted,denied`) · `reason` default `''` · `created_at` · `expires_at` NOT NULL · `resolved_at` NULL. Partial index `WHERE status='pending'` on `expires_at` for the T-031a-ii sweeper. A timeout = `status='denied', reason='timeout'` (fail-closed), not a separate status.
- **`policy_outbox`** + `policy_outbox_new` NOTIFY channel + `pg_notify` trigger — own table/channel so it won't collide with `outbox` / `api_outbox` / `connector_outbox` in the shared cockpit DB.

## Design decisions (carried from the consult)

- **Both tables in one `0001_policy` migration** (vs. two) — a file-count trim that's clean for a greenfield service's *initial* schema (CEO-approved).
- **Own tables in the shared cockpit DB**, not a separate physical DB — ADR-0054 §6 says "the same parameterized-table pattern as connector-service", which is exactly this. G2-clean.
- **No store / RPC / verifier wiring yet** — `server.New()` is signature-minimal (healthz only); T-031a-ii extends it with the pool + capability verifier when the gated RPCs need them. Keeps a-i free of unused scaffolding.

## G1 / G2

- **G1** — single-service spine (healthz + migration); no cross-service boundary. The live decision→approval→audit proof is **T-033 / WD-2**. Same honest single-service cadence as connector T-029b-i.
- **G2** — no ADR divergence; ADR-0054 is the spec. Own-tables-in-shared-DB conforms §6.

## Verification (Dockerized golang:1.23 + compose-mvp Postgres)

- `go build` / `go vet` / `golangci-lint run` on `services/policy-service` — clean.
- `/healthz` unit test (200 + body `ok`).
- `policy-service migrate up` applies `0001_policy` on the compose-mvp Postgres; `pending_approvals` + `policy_outbox` + the NOTIFY trigger present.
- Relay declines gracefully when `AIREOS_AUDIT_URL` unset (serve still boots).

## Definition of done

Service builds/lints; healthz green; `0001_policy` migrates clean; module wired
into `go.work`. Commit on `dev`, fast-forward `main` when verified. No push.
T-031a-ii (decide/resolve RPC + audited store + sweeper) follows.
