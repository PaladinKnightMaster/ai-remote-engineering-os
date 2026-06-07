---
name: T-030b Ticket
about: Governed Egress Foundation — live broker→connector→audit proof (SEC-13 runtime, G1)
title: "T-030b: live connector credential-lease e2e proof (SEC-13 runtime / G1)"
labels: ["ticket", "test", "security"]
---

## Ticket ID and link

T-030b — second half of **T-030**. The **live cross-boundary proof** that T-030a's
re-point works on real wires: the broker leases the SSH credential from
connector-service (holding no key itself) and the egress is audited. This is the
**G1 gate** for the connector spine (`ai/war-room/activation-matrix.overlay.md`)
and the **runtime** proof of **SEC-13**. Pairs with T-030a (the code half, mocked
on the broker side). Conforms ADR-0053 (lease topology) + ADR-0045 (egress audit).

## Goal

Extend the existing live SSH harness (`task e2e`, `docker-compose.e2e.yml`,
`tests/e2e`) so the credential comes from connector over the wire, with the
broker holding **no** resource key, and assert the egress in the live chain:

> login → create session → broker WS → type a command → read PTY output → end →
> assert the live chain has session.open + terminal.input.line + session.close
> (cast SHA) **and `credential.leased`** (target `resource:<id>`, actor
> `operator:admin`, evidence `session:<id>`), with the broker booted with an
> EMPTY `AIREOS_RESOURCE_SECRET_KEY`. No mocked boundary.

## Priority

P1 — v0.2.0 spine. The load-bearing verification that de-risks T-031/T-032 (do not
stack policy/approval on an unproven spine).

## Files in scope (5)

- Modify: `infra/docker-compose/docker-compose.e2e.yml` — add `connector` service (serve + `AIREOS_AUDIT_URL` so its egress relay delivers); seed runs `connector-service migrate up` + `connector credentials set-secret … --key-file /seed-keys/client_ed25519`; broker gets `CONNECTOR_URL` + **`AIREOS_RESOURCE_SECRET_KEY: ""`**; api gets `AIREOS_RESOURCE_SECRET_KEY: ""`; `connector` added to `tester.depends_on`.
- Modify: `tests/e2e/cmd/seed/main.go` — resource INSERT is labels-only (no `connection_secret`); drop the now-unused `AIREOS_RESOURCE_SECRET_KEY` read (the connector CLI provisions the credential).
- Modify: `tests/e2e/harness_test.go` — `pollLeased` helper.
- Modify: `tests/e2e/e2e_test.go` — step 10: assert `credential.leased`.
- Create: `docs/tickets/T-030b-live-connector-egress-proof.md` (this file) + `docs/tickets/README.md` status flip.

## What makes it a real proof (G1)

- **Real PASETO across the cluster** (one `AIREOS_PASETO_KEY`), not `*_DEV_INSECURE_AUTH`. The broker mints `IssueService("session-broker","connector-service")`; connector verifies audience `connector-service`.
- **Broker holds NO resource key** — `AIREOS_RESOURCE_SECRET_KEY: ""` on the broker (and api). Only connector + the seed-time CLI hold it. The SSH session succeeding is the proof the secret came from connector.
- **Provisioning via the production CLI** — `connector credentials set-secret` (the path T-030a re-pointed), not a SQL insert into `resources.connection_secret` (which is left NULL).
- **Egress audited through the real relay** — `credential.leased` is enqueued atomically with the decrypt in `connector_outbox` and delivered to audit-service by connector's in-process relay; the test reads it back via `GET /v1/events`.

## G2

No ADR divergence. Conforms ADR-0053 + ADR-0045. The deferred column drop (T-030a
G2) is unchanged; the `resources.connection_secret` column still exists but is left
NULL by the seed — inert, as designed.

## Known-broken-until-T-033 (carried from T-030a)

`api resources set-secret` is gone, so the offline WD-1 harness
(`infra/offline/wd1/run-wd1.sh`) is hard-guarded to abort until **T-033/OI-6**
re-points offline provisioning to `connector credentials set-secret` (and removes
the guard). T-030b is the **headless compose e2e** — it compiles service binaries
from source on every run and uses no offline bundle / `apps/web/dist`, so it is
unaffected. WD-2 (the v0.2.0 release capstone, T-033) is where the offline path is
re-proven.

## Verification

`task e2e` (up → seed → test → down) green with the new `credential.leased`
assertion AND the broker booted with an empty resource key — on the Windows host
under `MSYS_NO_PATHCONV=1`. Plus `go vet -tags e2e ./tests/e2e/...`.

## Definition of done

Live chain carries `credential.leased` alongside the lifecycle events; broker
proven to hold no key; `task e2e` green; ticket + README updated. Commit on `dev`,
fast-forward `main` when verified. No push.
