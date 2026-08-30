# Agent Egress Policy

## Goals
Agents must not receive raw persistent secrets. They request operations through a gateway.

## Controls
- just-in-time credentials
- credential lease expiration
- domain/API allowlists
- human approval for sensitive access
- per-ticket egress scopes
- full audit trail

## Implemented at v0.2.0 (implementation references; DOC-9)

`services/connector-service` is the egress + credential gateway (ADR-0053) and
`services/policy-service` the human-approval decision point (ADR-0054):

- **Just-in-time credentials** — the broker never reads a stored secret; it
  leases the decrypted credential per session-open via the internal
  `POST /v1/credentials/lease` (`services/connector-service/internal/server/server.go`),
  gated `service-service` (SEC-15). connector-service is the **sole**
  `AIREOS_RESOURCE_SECRET_KEY` holder and only `pgp_sym_decrypt` path (SEC-13;
  decrypt in `services/connector-service/internal/store/store.go`), enforced by
  the `task sec13-gate` grep gate + the e2e runtime proof (api/broker boot with
  empty keys).
- **Human approval for sensitive access** — every session-start is parked and
  consults policy-service before proceeding (`services/api/internal/server/server.go`
  `createGoverned`, ADR-0074 §D2); a `requires_approval=true` resource (or a
  matching policy rule) leaves it parked until the operator resolves it through
  policy-service (`services/policy-service/internal/server/server.go`); unresolved
  approvals fail closed on the sweeper timeout (`approval.denied:timeout`).
- **Full audit trail** — every lease emits `credential.leased` and every
  decision/resolution emits `policy.decision:*` / `approval.*` through the
  per-service transactional outboxes into the chained-hash audit-service
  (SEC-14/AUD-7).

Deferred (release-criteria §10): domain/API allowlists + per-ticket egress
scopes arrive with the Typed MCP Gateway (the first agent surface); v0.2.0's
egress scope is the SSH cockpit session. Lease expiry rides the session
lifetime + the api-side stuck-session reaper at v0.2.0.
