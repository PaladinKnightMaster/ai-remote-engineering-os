---
name: T-030a Ticket
about: Governed Egress Foundation — re-point credential consumers onto connector (code half)
title: "T-030a: broker re-point onto connector lease + api decrypt removal (SEC-13 code half)"
labels: ["ticket", "security", "refactor"]
---

## Ticket ID and link

T-030a — first half of **T-030** (re-point the credential consumers onto
connector-service). No B-NNN counterpart. The **code half** of the SEC-13 closer:
it removes `pgp_sym_decrypt` + `AIREOS_RESOURCE_SECRET_KEY` from the two legacy
custodians (`services/api`, `services/session-broker`) and routes the broker's
credential fetch through connector-service's gated lease RPC (T-029b-ii). The
**live cross-boundary proof is T-030b** (the G1 gate) — this ticket is mocked on
the broker side (httptest + a fake leaser), explicitly NOT a verified e2e.

Builds on: T-029a (custody), T-029b-i (outbox+serve), T-029b-ii (lease RPC +
egress audit). Conforms ADR-0053 (connector-service) and ADR-0048 §7.

## Goal

Make connector-service the **sole** holder of `AIREOS_RESOURCE_SECRET_KEY` and
the only service that decrypts target credentials, in service runtime code:

> broker at WS-connect → `LoadTargetCoords` (labels only, no decrypt) +
> `connector.Client.Lease(resource,session,operator)` → merge the leased
> `{private_key,passphrase}` into the dial target. api never decrypts (the
> `resources` provisioning subcommand + its key reader are deleted; provisioning
> is now `connector-service credentials set-secret`).

## Priority

P1 — v0.2.0 "Governed Egress Foundation" spine. Closes SEC-13 at the code level;
SEC-13 **runtime** proof is T-030b.

## Files in scope (13; +1 over the ≤12 ceiling, CEO-approved)

- Create: `services/session-broker/internal/connector/connector.go` (lease client)
- Create: `services/session-broker/internal/connector/connector_test.go` (httptest wire contract)
- Modify: `services/session-broker/internal/server/server.go` (Config: drop `SecretKey`, add `Leaser`; handler: `LoadTargetCoords` + lease)
- Modify: `services/session-broker/internal/server/server_test.go` (fakeLeaser; retarget the 503→424 tests)
- Modify: `services/session-broker/internal/store/store.go` (`LoadTarget`→`LoadTargetCoords`; drop decrypt/`material`/`ErrNoSecret`)
- Modify: `services/session-broker/internal/store/store_integration_test.go` (seed without secret; coords-only assertion)
- Modify: `services/session-broker/main.go` (drop key read + key-separation guard; add `CONNECTOR_URL` + `connectorTokenSource` + client wiring)
- Modify: `services/api/main.go` (remove `resources` subcommand, genkey block, `resourceSecretKey`, `checkKeySeparation`, `secret` import, `flag` import)
- Delete: `services/api/internal/secret/secret.go`
- Delete: `services/api/internal/secret/secret_integration_test.go`
- Modify: `services/connector-service/main.go` (add `credentials genkey`; fix stale `api resources genkey` message)
- Modify: `Taskfile.yml` (drop `resources` task; fold genkey into `connector` desc)
- Create: `docs/tickets/T-030a-broker-repoint-api-decrypt-removal.md` (this file)

The 13th file is this ticket doc; the substantive change is 11 code files + the
Taskfile. ~300 code LoC net, well under the 500 ceiling.

## Decisions

- **genkey relocation → `connector-service credentials genkey`** (CEO sign-off).
  connector is the sole key holder, so the key generator lives with it; keeping
  the resource-key and auth-key provisioning flows on separate command surfaces
  self-documents the `AIREOS_RESOURCE_SECRET_KEY ≠ AIREOS_PASETO_KEY` authority
  separation (vs. pointing operators at `authctl genkey`, which invites reuse).
  Offline (no DB), handled before DSN resolution — same shape api used.
- **Broker token to connector** = `IssueService("session-broker","connector-service")`
  when `AIREOS_PASETO_KEY` set, else `BROKER_CONNECTOR_TOKEN` (dev), else none —
  mirrors the existing `auditTokenSource`.
- **Degraded-boot semantics shift 503→424.** The old "key unset → 503" branch is
  gone from the broker; the 503 now lives on **connector** (its key unset). A
  missing/unreachable connector or a missing credential is a **424 Failed
  Dependency** on the broker (the session cannot be brokered) — same family as
  the old `ErrNoSecret`/`ErrTargetIncomplete` 424.

## G1 — live test named (mocked here, proven in T-030b)

T-030a is **mocked on the broker side**: `connector_test.go` exercises the real
client wire-format (method/path/bearer/body/decode) against an `httptest.Server`;
`server_test.go` drives the handler branches with a `fakeLeaser`. **No live
broker→connector boundary is crossed.** The live proof — add connector-service
(serve) to `docker-compose.e2e.yml`, seed the credential via
`connector credentials set-secret`, assert the SSH session works with the secret
coming **from connector** and the `credential.leased` egress event lands in the
live chain — is **T-030b**.

## G2 — ADR divergence flagged (sequencing refinement, no new ADR)

ADR-0053 §5 specifies expand-migrate-**contract** ("…then drops
`resources.connection_secret`"). T-030a does **expand + migrate** (re-point the
reads off the column; remove the key from api + broker) but **defers the column
DROP to a v0.3.0 contract migration** (CEO sign-off). Rationale: the leftover
ciphertext is inert without the key, which only connector now holds; deferring
the drop keeps the T-029a `BackfillFromResources` upgrade-source intact. This is
a **sequencing refinement of an Accepted ADR, not a new architectural decision —
no new ADR required**, recorded here and in MEMORY.

## SEC-13 grep-clean — scope asserted

Clean over **service runtime code** (`services/api` + `services/session-broker`):
zero `pgp_sym_decrypt`, zero `AIREOS_RESOURCE_SECRET_KEY`. Deliberately **out of
scope** (still reference the key until their own tickets):

- `tests/e2e/cmd/seed` + `infra/docker-compose/docker-compose.e2e.yml` → re-pointed
  in **T-030b**.
- `infra/offline/install.sh` + systemd units → **T-033 / OI-6** (relocate the env
  from api to connector).
- `services/api/migrations/0003_resource_secret.up.sql` → the deferred column
  (v0.3.0 contract leg); it defines the column, runs no decrypt.

## Verification (Dockerized — golang:1.23; integration on compose mvp Postgres)

- `go build ./...`, `go vet ./...`, `golangci-lint run` across api/broker/connector — clean.
- Broker unit: `go test ./services/session-broker/internal/server/... ./services/session-broker/internal/connector/...` (fakeLeaser branches + httptest wire contract).
- Broker store integration (`-tags integration`, `BROKER_TEST_DATABASE_URL`): `LoadTargetCoords` reads labels, carries no secret, runs no decrypt; lifecycle/prune regressions green.
- connector store integration regression green (T-029a/b unchanged).
- SEC-13 grep over `services/api` + `services/session-broker`: no `pgp_sym_decrypt`, no `AIREOS_RESOURCE_SECRET_KEY`.

## Definition of done

Code re-pointed; api decrypt path deleted; genkey relocated; Taskfile updated;
all listed verification green; G1 (mocked-here / T-030b-live) + G2 (deferred
column drop) recorded. Commit on `dev`, fast-forward `main` when verified. No push.
