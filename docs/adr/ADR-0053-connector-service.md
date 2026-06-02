# ADR-0053: connector-service — credential custody + egress gateway (promotes the ADR-0048 §7 reserve and closes the SEC-12 pgcrypto debt)

## Status

**Proposed** — 2026-06-01. Recorded as part of the v0.2.0 "Governed Egress
Foundation" scoping consult (`docs/evaluation/v0.2.0-release-criteria.md`). This
ADR is **not yet Accepted**: it is ratified in its implementation consult (the
T-028 ADR-authoring ticket), where the open questions in §Open questions are
settled. It is published Proposed now so the v0.2.0 criteria have a stable
decision anchor and the ADR number is reserved.

**Builds on / conforms to:** ADR-0048 §7 (held `services/connector-service` in
reserve as the architecturally-correct home for target credentials),
ADR-0045 (already names the `api → connector-service` credential-lease as a sync
RPC command and the `connector-service → audit-service` egress-decision as a
fan-in chained-hash event), ADR-0041/0043/0044 (connector-service was always a
P0-planned Go capability-edge service). **No ADR divergence (G2-clean):** this
activates scaffolding the prior ADRs planned; it does not contradict any
Accepted ADR.

## Context

At v0.1.0, target connection credentials live in `services/api`'s Postgres as a
`pgcrypto`-encrypted `resources.connection_secret` column unwrapped with
`AIREOS_RESOURCE_SECRET_KEY` (SEC-12). The v0.1.0 criteria call this a **known
security debt** and name `services/connector-service` as the correct home; the
broker even **mirrors** a `pgp_sym_decrypt` to read the credential (a documented
duplication waiting on "a 3rd consumer" to extract a `packages/go/secret`).

Two facts make connector-service the v0.2.0 keystone rather than a later
nice-to-have:

1. **Doctrine gate.** CLAUDE.md: "No AI autonomy before audit + policy + approval
   primitives exist." The egress/credential chokepoint is the *egress* primitive.
   Every P1 module (MCP Gateway, Workstreams) is an egress-touching agent surface
   and cannot ship until this boundary exists.
2. **The service is scaffolded, not greenfield.** `services/connector-service/`
   exists (`.gitkeep` + README) and ADR-0045 already fixes its wire topology.
   v0.2.0 builds it; it does not invent it.

## Decision (direction — ratified in the T-028 consult)

1. **connector-service is the sole custody + decrypt authority for target
   credentials.** The `pgp_sym_decrypt` path is **removed from `services/api`**
   (and the broker's mirror): after v0.2.0, no service other than
   connector-service holds `AIREOS_RESOURCE_SECRET_KEY` or can read a target
   secret. This closes SEC-12.
2. **api/broker become credential-lease clients.** Per ADR-0045's named topology,
   the credential lease is a **sync RPC command** (`Authorization: Bearer
   <service-PASETO>`, `edge: service-service`, ADR-0046). The broker obtains the
   target credential for an SSH session by leasing it from connector-service at
   connect time — it never reads the encrypted column directly.
3. **Egress decisions fan in to `audit-service` as chained-hash events** (ADR-0045
   `connector-service → audit-service`), via the `packages/go/outbox` transactional
   outbox (connector-service becomes its own stateful producer, like the broker).
4. **Credential custody storage** — connector-service owns the encrypted
   credential material (migrated out of `services/api`). Whether it owns a fresh
   table in the shared cockpit DB or its own store, and the existing-row migration
   path, are settled in the T-028/T-029 consult (§Open questions).

## Alternatives considered (sketch — expanded at ratification)

- **Leave credentials in `services/api`, add only a façade.** Rejected: leaves
  the decrypt authority diffuse (api + broker mirror), so SEC-12 stays open and
  the egress chokepoint the agent layer needs never materializes.
- **Extract a `packages/go/secret` library shared by api + broker (no service).**
  Rejected as the *destination*: a shared lib spreads the key to every importer —
  the opposite of a chokepoint. (It may still appear as connector-service's
  internal implementation.)
- **Build the full connection-adapter surface (Guacamole/MeshCentral) now.**
  Deferred: v0.2.0 scope is credential custody + egress decision on the existing
  SSH wedge; GUI adapters track the guacd deferral (v0.1.0 §10).

## Open questions (settle in the T-028 ratification consult)

- **OQ-A.** connector-service store: own DB + migrations (stateful, like the
  broker) or a table in the shared cockpit DB? (ADR-0045 frames it as a stateful
  outbox producer.)
- **OQ-B.** Lease transport: in-process vs sync HTTP RPC; does the broker lease
  directly from connector-service or via `services/api`?
- **OQ-E.** Existing-credential migration: data-migrate the current
  `resources.connection_secret` rows into connector custody, or greenfield
  re-provision via an updated `resources set-secret` path?

## Verification (blocks the v0.2.0 connector-service tickets, not this ADR)

- **SEC-13:** `services/api` has **no** credential-decrypt path — grep-proven (no
  `pgp_sym_decrypt`, no `AIREOS_RESOURCE_SECRET_KEY` read outside
  connector-service) **and** runtime-proven (api cannot fetch a target secret).
- **WD-2 (live, G1):** the SSH wedge obtains its credential **via
  connector-service** on real wires through the `tests/e2e` + `task wd1` harness
  (no mocked boundary); the egress decision appears in the audit chain.

## Related ADRs

- ADR-0045 — outbox topology (`api → connector-service` lease, `connector-service
  → audit-service` egress events) this ADR implements.
- ADR-0046 — PASETO service tokens (`edge: service-service`) for the lease RPC.
- ADR-0048 §7 / SEC-12 — the reserve + debt this ADR promotes/closes.
- ADR-0054 — the approval/policy decision point that consumes connector-service's
  egress decisions.
