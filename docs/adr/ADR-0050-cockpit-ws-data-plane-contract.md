# ADR-0050: Cockpit WebSocket data-plane contract — channel path and operator-audience cookie auth

## Status

Accepted — 2026-05-31. **Refines ADR-0048 §1 (terminal transport / WS path) and
§4 (WebSocket-upgrade authentication), and ADR-0046 (token audience model) as it
applies to the cockpit WebSocket.** Does not modify those ADRs; per CLAUDE.md a
deviation from an Accepted ADR is recorded in a new ADR. Both points below were
surfaced by the T-020b live end-to-end harness — the first run on real wires of
the operator → api → session-broker → sshd → audit-chain path.

## Context

ADR-0048 specified the cockpit session lifecycle. Two of its details did not
match what the SSH slice (T-016…T-020) actually shipped, and the gaps were
invisible until T-020b ran the path with real PASETO auth (every prior
session-broker test used `BROKER_DEV_INSECURE_AUTH=1`, an allow-all verifier
that ignores both the route detail and the token audience):

1. **WS path.** ADR-0048 §1 wrote the data-plane route as
   `/v1/sessions/{id}/stream`. The slice shipped `/v1/sessions/{id}/channel`
   end to end — the session-broker chi route (T-017b), the `apps/web` Vite dev
   proxy rule `^/v1/sessions/[^/]+/channel$` (T-018b), and `<TerminalView>`'s
   `useWebSocketChannel` consumer all use `channel`. The ADR text was never
   reconciled.

2. **WS upgrade authentication audience.** ADR-0048 §4 said the browser's
   existing `aireos_session` HttpOnly cookie authenticates the upgrade —
   "no new auth path; the cookie-based posture from ADR-0047 flows through."
   That cookie carries the operator PASETO token, which `services/api` mints
   (the only operator-token issuance in the system) with **audience `api`**
   (`IssueOperator(subject, "api")`). The session-broker, however, built its
   capability verifier with **audience `session-broker`**, mirroring the
   per-service convention from the other services. No code anywhere mints a
   `session-broker`-audience operator token, so the broker's WS gate was
   **unsatisfiable by any operator** under real PASETO: every upgrade returned
   `401 invalid capability token`. The cockpit WebSocket was unusable in any
   non-dev deployment — a latent release-blocker masked entirely by
   dev-insecure auth in unit/integration tests.

## Decision

1. **The cockpit WS data-plane route is `GET /v1/sessions/{id}/channel`.** This
   refines ADR-0048 §1's `…/stream`. `channel` is the shipped contract across
   the broker, the SPA proxy, and the TS client; the ADR conforms to it rather
   than forcing a cosmetic rename across three shipped surfaces pre-v0.1.0.
   (WebSocket routes are not modeled in the OpenAPI fragment, so there is no
   generated-client drift to reconcile.)

2. **The session-broker authenticates the WS upgrade against audience `api`.**
   The broker's capability verifier uses audience `api` — the audience operators
   actually carry — not `session-broker`. The cockpit is one operator-facing
   trust domain on one origin; the operator's `aireos_session` cookie is THE
   operator credential and flows through unchanged, exactly as ADR-0048 §4
   intended. This refines how ADR-0046's audience-separation model applies to
   the cockpit WebSocket: audience separation still isolates *service* tokens
   and cross-gateway replay, but the operator-facing data plane shares the `api`
   operator audience because it shares the operator session cookie.

## Alternatives considered

- **Rename the route to `/stream` to match ADR-0048 §1.** Rejected: churns the
  broker route, the Vite proxy regex, and `<TerminalView>` for zero functional
  gain and real regression risk immediately before release. The cheaper, honest
  fix is to ratify the shipped name.
- **Mint a separate `session-broker`-audience token/cookie for the WS.**
  Rejected: a "new auth path" that ADR-0048 §4 explicitly rules out. It would add
  a second cookie, a second issuance path in `services/api`, and SPA changes, to
  re-establish a trust relationship the existing operator cookie already
  expresses on the same origin.
- **Broker verifier with empty audience (accept any).** Rejected: discards
  audience checking entirely; accepting the specific `api` operator audience is
  the minimal correct relaxation.

## Consequences

### Positive
- The cockpit WebSocket is reachable by operators under real PASETO — the SSH
  pillar is actually operable, not just operable-with-dev-auth.
- The ADR ladder is honest: §1's route and §4's audience now match shipped code.
- No new cookie, no new issuance path, no SPA change.

### Negative
- The session-broker no longer enforces a broker-specific audience; it trusts
  `api`-audience operator tokens. Acceptable because broker and api are the same
  operator-facing surface on one origin, and the broker still verifies the full
  PASETO (signature, expiry, edge `agent-remote-session`/`human-agent`). Service
  tokens (e.g. the broker→audit relay) keep their own recipient audiences.

## Verification

- T-020b (`tests/e2e`): the live harness drives the real
  operator → api → broker → sshd → audit path with real PASETO; the WS upgrade
  succeeds with the operator `aireos_session` cookie at `/v1/sessions/{id}/channel`,
  and the audit chain receives `session.open` + `terminal.input.line` +
  `session.close`. Before the §2 fix the same test failed with
  `401 invalid capability token` at the upgrade — the regression guard.
- `go test ./services/session-broker/...` stays green after the audience change.

## Related ADRs

- ADR-0046 — PASETO capability tokens + audience model (refined here for the
  cockpit WS).
- ADR-0047 — `aireos_session` HttpOnly cookie posture (the credential that flows
  through to the broker).
- ADR-0048 — cockpit session lifecycle (§1 WS path and §4 upgrade auth, refined
  here).
- ADR-0049 — cockpit session audit content model (the other SSH-slice
  refinements of ADR-0048).
