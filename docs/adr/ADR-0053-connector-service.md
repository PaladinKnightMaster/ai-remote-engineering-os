# ADR-0053: connector-service — credential custody + egress gateway (promotes the ADR-0048 §7 reserve and closes the SEC-12 pgcrypto debt)

## Status

**Accepted** — 2026-06-01 (ratified in the T-028 war-room consult, which settled
OQ-A/OQ-B/OQ-E below). Recorded as part of the v0.2.0 "Governed Egress
Foundation" release (`docs/evaluation/v0.2.0-release-criteria.md`). Binding spec
for the v0.2.0 connector-service implementation tickets (T-029/T-030).

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

## Decision

1. **connector-service is the sole custody + decrypt authority for target
   credentials.** The `pgp_sym_decrypt` path is **removed from `services/api`**
   (`internal/secret`) **and the broker's mirror** (`internal/store.LoadTarget`):
   after v0.2.0 no service other than connector-service holds
   `AIREOS_RESOURCE_SECRET_KEY` or can read a target secret. This closes SEC-12
   and satisfies SEC-13.

2. **Storage (OQ-A → resolved): connector owns its own credential table + its own
   transactional outbox, in the shared cockpit DB.** connector-service is a
   stateful outbox producer (conforms ADR-0045) with its own migrations under
   ADR-0044; it reuses the T-022 parameterized-outbox-table precedent (e.g.
   `connector_outbox`). The encrypted `{private_key, passphrase}` blob **moves off
   `resources.connection_secret` into the connector-owned table**; non-secret
   connection coordinates (host, port, login user, `ssh_host_key`) **remain on
   `resources.labels`** (the broker still reads them at connect). Result: api and
   broker can no longer even *see* the ciphertext column, not merely lack the key.

3. **Lease consumer + transport (OQ-B → resolved): the session-broker leases the
   credential directly from connector-service.** At SSH connect the broker (the
   SSH dialer) requests the plaintext credential via **sync RPC**
   (`Authorization: Bearer <service-PASETO>`, `edge: service-service`, ADR-0046);
   it never reads an encrypted column.
   **G2 — refines ADR-0045.** ADR-0045's topology table illustratively names
   `api → connector-service (credential lease)`; that row predates the SSH slice
   making the **broker** the SSH client. This ADR records the corrected consumer
   (`broker → connector-service`). ADR-0045's *intent* is preserved — the lease is
   a **sync command, not an event**; only the consumer identity is corrected.
   **Honest scope of SEC-13:** the broker still *receives plaintext* to dial SSH
   (unavoidable while it is the SSH client). The win is custody **centralization**
   (one key holder) + **audited egress decisions** — not plaintext elimination.
   connector-as-SSH-dialer (the README's adapter future) is deferred so ADR-0048's
   broker-owns-the-PTY-data-plane is untouched.

4. **Egress decisions fan in to `audit-service` as chained-hash events** (ADR-0045
   `connector-service → audit-service`), via connector's transactional outbox
   (`packages/go/outbox`). Each credential lease emits an audited egress decision.

5. **Existing-credential migration (OQ-E → resolved): data-migrate, not greenfield
   re-provision.** The key *value* is unchanged, so connector's migration
   **relocates the existing ciphertext** from `resources.connection_secret` into
   the connector-owned table (no re-encryption), then drops
   `resources.connection_secret`. Existing targets survive the v0.1.0 → v0.2.0
   offline upgrade. The one manual upgrade step: `AIREOS_RESOURCE_SECRET_KEY`
   moves from `services/api`'s systemd env to connector-service's (handled by
   `install.sh`). The `api resources set-secret` provisioning path re-points to
   connector-service.

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

## Open questions — resolved in the T-028 consult

- **OQ-A → resolved (Decision 2):** connector owns its own credential table + its
  own outbox in the shared cockpit DB (stateful producer).
- **OQ-B → resolved (Decision 3):** the broker leases directly from
  connector-service over sync RPC; recorded as the ADR-0045 consumer refinement.
- **OQ-E → resolved (Decision 5):** data-migrate the existing ciphertext into
  connector custody; drop `resources.connection_secret`; key env relocates to
  connector.

Out of scope for v0.2.0 (future ADR/ticket when triggered): connector-service as
the SSH/GUI connection *adapter* (owning the dial), and connector running its own
isolated database rather than the shared cockpit DB.

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
