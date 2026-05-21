---
name: T-008 Auth placeholder adapter (packages/go/auth — PASETO v4 capability tokens)
about: In-process capability-token issuer/verifier that satisfies the middleware Verifier port and replaces audit-service's fail-loud dev verifier
title: "T-008: Auth placeholder adapter (packages/go/auth/, PASETO v4)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-008 — see `docs/tickets/README.md`. The auth placeholder, framed by **ADR-0046**:
lands `packages/go/auth/` (the fifth `packages/go/<infra>/` module) and wires it
into the chain-authority service so `POST /v1/events` is gated by a real
capability token instead of the T-004b fail-loud dev verifier. Lead lens:
**Permission Model Reviewer** (capability-edge enforcement); **Audit/Evidence
Reviewer** (the ingest edge is service-service).

## Goal

Issue and verify PASETO v4 local (symmetric) capability tokens with the typed
`CapabilityToken` claim set, expose a `middleware.Verifier` adapter, and have
`services/audit-service` use it when a cluster key is configured.

## Priority

P0. Closes ADR-0046 §Verification items 1–4 for the in-process token model and
the audit-service S2S edge; unblocks secure service-to-service calls for every
later service.

## Scope decisions (settled in consult)

- **Placeholder = `packages/go/auth/` + audit-service wiring.** Operator login
  (`POST /v1/auth/login`, argon2id bootstrap account), signing-key custody,
  `task auth rotate-key`, and `install.sh` provisioning are **deferred to the
  `services/api` ticket** (ADR-0046 §Hand-off explicitly puts them there).
- **PASETO v4 local** via `aidanwoods.dev/go-paseto` (note: the ADR wrote the
  repo URL `github.com/aidantwoods/go-paseto`; the actual Go module path is the
  vanity `aidanwoods.dev/go-paseto` — **T-011's depguard rule must target the
  module path, not the repo URL**).
- **Dependency direction inverted vs ADR-0046's literal wording.** The ADR says
  "`RequireCapability` imports `packages/go/auth/`"; instead T-004a's
  `middleware.Verifier` **port** is kept and `auth` provides
  `AsMiddlewareVerifier` (so `auth` imports `middleware`). `middleware` stays
  dependency-free (it shipped first; every service imports it; this keeps the
  go-paseto dependency from leaking transitively into the middleware import
  graph). Net behavior is identical — `RequireCapability` verifies via the PASETO
  Verifier and extracts edge + scope.
- **audit-service verifier resolution (fail-loud preserved):**
  `AIREOS_PASETO_KEY` (hex cluster key) → real verifier requiring audience
  `audit-service`; else `AUDIT_DEV_INSECURE_AUTH=1` → dev allow-all; else fatal.
- **Revocation = key rotation + short TTL** at P0 (no revocation list); forbidden
  features (password reset / signup / OAuth / JWKS rotation) documented in the
  package README, review-enforced.

## Files in scope

- **Create:**
  - `packages/go/auth/go.mod`
  - `packages/go/auth/auth.go` — `Key` (+ `GenerateKey` / `NewKeyFromHex`),
    `Scope`, `CapabilityToken`, `Signer` (`Issue` + `IssueService` / `IssueOperator`,
    tiered TTLs), `Verifier` (`Verify` with expiry/issuer/audience rules).
  - `packages/go/auth/middleware.go` — `AsMiddlewareVerifier` adapter to the
    `middleware.Verifier` port.
  - `packages/go/auth/auth_test.go` — round-trip, scope preservation, expired /
    wrong-audience / wrong-key / tampered rejection, adapter.
  - `packages/go/auth/README.md` — usage + tiered TTLs + the forbidden-features
    list + the deferred `services/api` items.
  - `docs/tickets/T-008-auth-placeholder.md` — this ticket.
- **Modify:**
  - `go.work` — `use ./packages/go/auth`.
  - `services/audit-service/go.mod` — require + replace `packages/go/auth`.
  - `services/audit-service/main.go` — `capabilityVerifier()` uses the PASETO
    verifier when `AIREOS_PASETO_KEY` is set.
  - `services/audit-service/internal/server/server_test.go` — end-to-end test
    with a real signed service token (ADR-0046 §Verification item 4).
  - `docs/tickets/README.md` — mark T-008 complete.

## Acceptance criteria

- [ ] `packages/go/auth/` exports `Signer`, `Verifier`, and the typed
      `CapabilityToken` (`sub`/`edge`/`scope`/`iat`/`exp`/`jti`/`aud`/`iss`).
- [ ] `AsMiddlewareVerifier` makes a `*Verifier` satisfy `middleware.Verifier`;
      `RequireCapability` verifies via PASETO and extracts edge + scope.
- [ ] **Passing fixture:** a service-service token minted for audience
      `audit-service` passes `POST /v1/events` (`TestRealPasetoServiceToken`).
      **Violating fixtures:** wrong-audience / wrong-key / expired / tampered
      tokens are rejected (auth_test.go + the e2e 401 case).
- [ ] audit-service uses the real verifier when `AIREOS_PASETO_KEY` is set and
      still fails loud with no key and no dev opt-in.
- [ ] go-paseto is imported only inside `packages/go/auth/`.
- [ ] OpenAPI / schemas — **N/A**. Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — done.
- [ ] Verification command runs clean (see below).

## Out of scope

- Operator login / argon2id bootstrap account / `POST /v1/auth/login` — `services/api`.
- `task auth rotate-key`, signing-key custody, `install.sh` provisioning — `services/api` (ADR-0046 §Hand-off).
- Egress lease issuance wiring in `connector-service`; `jti` replay table in `policy-service`.
- The outbox relay attaching a service token — first producer ticket (ADR-0045).
- Zitadel / federation — P1 reserve (ADR-0046).

## Diff budget

≤12 files / ≤500 LoC. Actual: 6 created + 5 modified = 11 files, ≈450 LoC.

## ADRs required

None new. Implements ADR-0046 (in-process PASETO capability tokens) and consumes
ADR-0043 (the `middleware` port). All Accepted.

## Verification

Verified in a `golang:1.23` container against the docker-compose Postgres:

```
go build ./packages/go/auth/... ./packages/go/middleware/... ./packages/go/migrate/... ./services/audit-service/...
go vet  <same>
go test ./packages/go/auth/... ./services/audit-service/...        # unit (incl. PASETO e2e)
go run  ./services/audit-service migrate up
go test -tags integration ./services/audit-service/internal/store/...
```

All **pass**: auth crypto suite (round-trip, scope, expired/wrong-aud/wrong-key/
tamper, adapter), the audit-service real-PASETO e2e (valid token → 200,
wrong-audience → 401), chain + integration unchanged.
