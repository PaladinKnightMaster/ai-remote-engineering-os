---
name: T-017a Ticket
about: SSH end-to-end vertical slice (b) — outbox foundation + broker persistence spine
title: "T-017a: packages/go/outbox + session-broker persistence spine"
labels: ["ticket"]
---

## Ticket ID and link

T-017a — first half of the SSH end-to-end vertical slice (b) per ADR-0048
§Hand-off, and the **first transactional-outbox producer ticket** ADR-0045
§Hand-off always pointed at. No B-NNN counterpart. Sibling: **T-017b** (the SSH
data plane + lifecycle/line audit + cast writer) consumes the outbox + relay
this ticket stands up.

## Goal

Stand up the durable-evidence foundation for the cockpit: the shared
`packages/go/outbox/` module (Writer + Relay + audit HTTP client), and wire
`services/session-broker` as the first producer — its own `outbox` table, the
embedded migration, the `migrate`/`outbox relay` subcommands, and the
in-process relay goroutine. No SSH, no session emission yet (T-017b).

## Priority

P0 — v0.1.0 wedge (ADR-0045 event-bus strategy; ADR-0048 audit plane).
Both ADRs Accepted; no new ADR required.

## Files in scope

- Create: `packages/go/outbox/go.mod`  *(mechanical)*
- Create: `packages/go/outbox/outbox.go` (Writer.Enqueue + Event/wireEvent)
- Create: `packages/go/outbox/relay.go` (Relay.Run, Client, HTTPClient)
- Create: `packages/go/outbox/outbox_test.go` (fake-tx payload-contract unit tests)
- Create: `packages/go/outbox/relay_integration_test.go` (pg relay-drain, tagged)
- Create: `packages/go/outbox/README.md` (pattern + canonical migration template)
- Modify: `go.work` (`use ./packages/go/outbox`)  *(mechanical)*
- Modify: `services/session-broker/go.mod` (pgx, migrate, outbox deps)  *(mechanical)*
- Create: `services/session-broker/migrations/0001_outbox.up.sql`
- Create: `services/session-broker/migrations/0001_outbox.down.sql`
- Create: `services/session-broker/internal/store/store.go` (producer Enqueue seam)
- Modify: `services/session-broker/main.go` (serve|migrate|outbox relay + relay goroutine)
- Create: `docs/tickets/T-017a-outbox-foundation-broker-spine.md` (this file)
- Modify: `docs/tickets/README.md` (T-017a entry)

14 files; 4 mechanical (`go.work`, two `go.mod`, generated `go.sum`). ~10
substantive. Within the ≤12-file / ≤500-LoC budget intent.

## Scoping decisions (war-room consult, 2026-05-30)

- **Split T-017 into a/b (approved).** As one ticket T-017 is ~2× the budget
  (outbox module + broker persistence spine + SSH handler + cast writer + tests).
  The seam: the outbox + relay + broker spine is independently verifiable
  (ADR-0045 §Verification 1–6) with **no SSH**. SSH + audit emission + cast
  writing is T-017b. Same T-005a/b · T-016a/b precedent.
- **`audit-service` ingest already exists** (`POST /v1/events`, chained-hash +
  idempotency dedup, prior tickets). T-017a is only a *producer/client* — no
  audit-service change.
- **Audit client is a small typed `HTTPClient`, not oapi-codegen client-mode.**
  The audit `Ingest` DTO is already hand-authored in `audit-service`
  (`internal/audit/event.go`); a typed wrapper is consistent with how that
  contract is treated and saves a codegen config + generated file under the
  budget. The Writer stores the *verbatim* audit-ingest JSON, so the relay POSTs
  row bytes with no re-marshal.
- **`idempotency_key` is a UUID v7 minted by the Writer**; the sink dedupes on
  it (at-least-once + idempotent sink, ADR-0045). Delivery uses `FOR UPDATE SKIP
  LOCKED`, so the operator-run `outbox relay` subcommand can run alongside the
  in-process goroutine on disjoint rows.
- **`LISTEN/NOTIFY` is a wakeup hint only.** A per-insert `pg_notify('outbox_new')`
  trigger wakes the relay; a periodic sweep (default 10s) is the fallback so a
  missed notification never strands rows. The transport is always HTTP.
- **Relay is best-effort at boot.** `serve` starts it as a goroutine; if
  `AIREOS_AUDIT_URL` is unset the relay is skipped and the broker still serves —
  a degraded chain authority surfaces as growing outbox depth, not a failed boot
  (ADR-0045 "no boot-time draining gate").
- **Broker bearer = service-service PASETO** (`auth.Signer.IssueService(
  "session-broker", "audit-service")`) when `AIREOS_PASETO_KEY` is set; else a
  static `BROKER_AUDIT_TOKEN` (dev); else none (dev audit-service insecure auth).
- **`store.Enqueue` ships one ticket ahead of its consumer.** It is the producer
  seam T-017b enqueues through (then inside the session-state tx). Exported, so
  not lint-dead; its live broker→audit exercise is T-017b.

## Acceptance criteria

- [x] `packages/go/outbox/` is a `go.work` module exporting `Writer.Enqueue(ctx,
      tx, Event)` and `Relay.Run(ctx)` (ADR-0045 §Verification 1).
- [x] The stored outbox payload is exactly audit-service's `POST /v1/events`
      body (unit test unmarshals it into the ingest shape; optional fields
      omitted).
- [x] `session-broker` imports the outbox module, owns an `outbox` table via its
      own migration, and exposes the producer Enqueue seam (ADR-0045
      §Verification 2, producer-write half deferred to T-017b).
- [x] `session-broker migrate up` creates the table + partial index + pg_notify
      trigger; `migrate version` reports 1 (ADR-0044).
- [x] `session-broker outbox relay` runs the relay foreground; `serve` starts it
      as an in-process goroutine (ADR-0045 §Verification 4).
- [x] Relay drains a multi-batch backlog and stamps `delivered_at`; a failing
      client rolls the batch back leaving rows pending (at-least-once)
      (integration test).
- [x] Single static binary per service; no event-bus daemon (ADR-0045
      §Verification 5).
- [x] `golangci-lint` (T-011 config) clean on the new code.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Dual-write — violating:** POST to audit-service directly after commit (no
  outbox) loses evidence on a crash between commit and POST. **Passing:** the
  evidence row is enqueued inside the producer tx; the relay delivers
  at-least-once.
- **NOTIFY-as-transport — violating:** relying on `LISTEN/NOTIFY` to *carry*
  events drops them for any disconnected consumer. **Passing:** NOTIFY is a
  wakeup hint; HTTP is the transport; the sweep covers a missed notification.
- **Lost optional-field contract — violating:** emitting `"outcome":""` /
  `"evidencePacketId":""` would diverge from the ingest decoder's expectations.
  **Passing:** empty optionals are omitted (`omitempty`), asserted by test.

## Out of scope

- The SSH-over-WS data plane (x/crypto/ssh, PTY, resize, idle timeout,
  host-key pinning, large-output streaming) → **T-017b**.
- `session.open`/`session.close` lifecycle emission + the per-line/cast audit
  model (lifecycle-chain + cast-anchored, per consult) → **T-017b**.
- Byte-safe asciinema cast writer (`safeJSONStringFromBytes`) → **T-017b**.
- Cast-file persistence layout, retention/cleanup, chain linkage → **T-019**.
- Outbox-depth metric + runbook alert thresholds → follow-up (ADR-0045 known gap).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 14 files (4 mechanical), within intent.

## ADRs required

- None new. Lands `packages/go/outbox/` per ADR-0045 §Hand-off; advances
  ADR-0048's audit plane.

## Verification

```sh
# tidy + workspace + build + vet + gofmt + outbox unit tests (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 bash -c '
  (cd packages/go/outbox && go mod tidy) && (cd services/session-broker && go mod tidy) && go work sync &&
  go build ./packages/go/outbox/... && (cd services/session-broker && go build ./...) &&
  go vet ./packages/go/outbox/... ./services/session-broker/... &&
  test -z "$(gofmt -l packages/go/outbox services/session-broker)" &&
  go test ./packages/go/outbox/...'

# broker migrate smoke + outbox relay integration (MVP Postgres on docker-compose_default)
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e BROKER_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  -e OUTBOX_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 bash -c '
    go run ./services/session-broker migrate up &&
    go run ./services/session-broker migrate version &&
    go test -tags integration ./packages/go/outbox/...'

# lint (T-011 config)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./packages/go/outbox/... ./services/session-broker/...
```

Expected: build/vet/gofmt clean; outbox unit + integration tests pass (relay
drains all rows, stamps delivered_at, rolls back on delivery failure); broker
`migrate version` reports 1; lint clean. (All confirmed 2026-05-30 on Docker /
golang:1.23.)
