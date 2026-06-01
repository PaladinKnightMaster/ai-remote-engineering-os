---
name: T-022 Ticket
about: api outbox + auth/control-plane audit events (SEC-9)
title: "T-022: services/api outbox + auth audit events (login/refresh/logout) (v0.1.0)"
labels: ["ticket", "bug", "security"]
---

## Ticket ID and link

T-022 — release-gating gap surfaced by the pre-T-021c review. No B-NNN counterpart.
`services/api` was audit-silent (no outbox), but release-criteria **SEC-9**
(must-pass) + **WD-1 step 8** require operator auth events chained via the
ADR-0045 outbox. Completes ADR-0045's `api → audit-service` producer flow. **Must
precede T-021d (WD-1).** Binding specs: ADR-0045, ADR-0046, ADR-0048 §2.

## Goal

Give `services/api` its own transactional outbox + relay and emit the operator
auth-lifecycle events SEC-9 requires:

- `auth.login.success` on every `POST /v1/auth/login` 200,
- `auth.refresh` on every sliding-refresh cookie rotation,
- `auth.logout` on every `POST /v1/auth/logout` 204.

(`auth.service-token.mint` — SEC-9's 4th — deferred to **T-023**: it's the
audit-delivery relay's own credential, auditing every mint is circular/noisy and
not required by WD-1. Decision recorded with the CEO.)

## Priority

P0 — release-gating (SEC-9 must-pass; WD-1 step 8 depends on it).

## The shared-DB collision (and the fix)

api and the broker **share the cockpit database** (ADR-0048: the broker reads
api's `sessions`/`resources`). `packages/go/outbox` hardcoded the table name
`outbox`, which the broker already owns — so api couldn't add a second `outbox`.
**Fix (B1, CEO-approved):** parameterize the outbox table name. `Writer`/`Relay`
gain a `Table` field (default `"outbox"`, so the broker + the package's tests are
unchanged); api owns **`api_outbox`** with its own notify channel `api_outbox_new`.
Each producer owns its own outbox table + channel in the shared DB — the faithful
reading of ADR-0045's "a local outbox table per producer" under ADR-0048's shared
Postgres. No new ADR (it completes ADR-0045's intent).

## Files in scope

- Modify: `packages/go/outbox/{outbox.go,relay.go}` (parameterize `Table`; sanitized identifier)
- Modify: `packages/go/outbox/outbox_test.go` (expect the quoted default identifier)
- Create: `services/api/migrations/0004_outbox.{up,down}.sql` (`api_outbox` + trigger)
- Modify: `services/api/internal/store/store.go` (`outbox.Writer{Table:"api_outbox"}` + `RecordEvent` + table/channel consts)
- Modify: `services/api/internal/server/server.go` (emit on Login/refresh/Logout; `recordAuth` helper; verifier for logout attribution)
- Modify: `services/api/internal/server/server_test.go` (fake `RecordEvent`)
- Modify: `services/api/main.go` (`outbox relay` subcommand + in-process relay + `newRelay`/`auditTokenSource`)
- Modify: `services/api/go.{mod,sum}` (+ `packages/go/outbox`)
- Modify: `infra/docker-compose/docker-compose.e2e.yml` (+ `AIREOS_AUDIT_URL` for api) + `tests/e2e/e2e_test.go` (assert the live auth event)
- Create/modify: `docs/tickets/T-022-*.md` (this) + `docs/tickets/README.md`

**~15 files** — over the ≤12 budget, the cost of the CEO-approved B1 (parameterize
the shared package) + the live-harness verification. ~370 LoC (small per-file edits).

## Behaviour notes

- **Best-effort, never blocks auth.** `recordAuth` logs + continues on enqueue
  failure — a degraded audit path surfaces as growing outbox depth (ADR-0045),
  not a failed login.
- **Logout actor attribution:** `mw.CookieToAuthHeader` copies the session cookie
  into `Authorization` on every route; Logout verifies it (best-effort) to name
  the operator, else `operator:unknown`.
- **Forward-compatible install:** `install.sh` + the `aireos-api` systemd unit
  already pass `AIREOS_AUDIT_URL` to api (T-021b), so the offline install needs no
  change — api's relay simply starts delivering.

## Acceptance criteria

- [x] api owns `api_outbox` (migration `0004`) + relay (`api outbox relay`
      subcommand + in-process goroutine in `serve`); broker's `outbox` unchanged.
- [x] `Login`/`slidingRefresh`/`Logout` emit `auth.login.success`/`auth.refresh`/
      `auth.logout` via the outbox.
- [x] **Live:** the T-020b harness asserts `auth.login.success` by `operator:admin`
      reaches the chain through api's own relay (verified cold, `--network none` DB).
- [x] `packages/go/outbox` default behaviour unchanged (its tests + the broker
      pass); build/vet/golangci-lint clean on `packages/go/outbox` + `services/api`.

### Violating vs. passing fixtures

- **Hardcoded `outbox` table — violating:** api's `0004` migration collides with
  the broker's `outbox` in the shared DB. **Passing:** parameterized `Table`;
  api uses `api_outbox`.
- **Audit failure blocks login — violating:** returning 500 when the enqueue
  fails. **Passing:** `recordAuth` logs + continues (best-effort, ADR-0045).

## Out of scope

- `auth.service-token.mint` (SEC-9's 4th event) → **T-023** (token caching to make
  it bounded + non-circular).
- WD-1 wedge demo / full timeline against the installed stack → **T-021d**.

## ADRs required

- None new — completes ADR-0045's `api → audit-service` producer flow + SEC-9.

## Verification

```sh
# build/vet/lint/test the changed modules
docker run --rm -v "$PWD":/work -w /work -v aireos-gomod:/go/pkg/mod golang:1.23 bash -c '
  (cd services/api && GOWORK=off go mod tidy) &&
  go build ./packages/go/outbox/... ./services/api/... &&
  go vet ./packages/go/outbox/... ./services/api/... &&
  go test ./packages/go/outbox/... ./services/api/...'
# live: the e2e harness asserts auth.login.success in the chain (see T-020b verify)
cd infra/docker-compose && docker compose -f docker-compose.e2e.yml run --rm tester
```

Confirmed 2026-05-31: build/vet/golangci-lint/tests clean; cold e2e PASS incl.
`auth.login.success` by `operator:admin` delivered through api's relay.
