---
name: T-015b Operator login HTTP surface (POST /v1/auth/login public, GET /v1/auth/me gated)
about: The HTTP half of operator login — issue an operator token from credentials and expose the verified identity
title: "T-015b: Operator login HTTP surface (/v1/auth/login + /v1/auth/me)"
labels: ["ticket", "P0"]
---

## Ticket ID and link

T-015b — see `docs/tickets/README.md`. The HTTP half of operator login; builds on
T-015a (argon2id + bootstrap account + `authctl`). Closes the operator-login bits
ADR-0046 deferred from T-008. Makes the system usable end-to-end: something can
finally **issue** an operator token. Lead lens: **Permission Model Reviewer**.

## Goal

Add `POST /v1/auth/login` (public — exchanges credentials for an operator PASETO
token) and `GET /v1/auth/me` (gated — returns the verified capability) to
`services/api`, generated per-service from the contract, wiring the T-015a
account + `packages/go/auth` Signer.

## Priority

P0. Completes T-015.

## Scope decisions (settled in consult / during implementation)

- **Mixed public/gated security via the generated marker.** `login` declares
  `security: []` (public); the rest inherit the global bearer security. The
  oapi-codegen wrapper sets `BearerAuthScopes` in the request context only for
  secured operations, so a single `gateByDeclaredSecurity` middleware enforces
  `RequireCapability` exactly when the marker is present — login stays public,
  `me` + resources stay gated. (Verified: login wrapper sets the marker 0 times.)
- **Operator tokens are audience-scoped.** `IssueOperator(subject, audience)` now
  sets `aud` (the gateway issues `aud:"api"`); the api verifier requires audience
  `"api"`, so an operator token is rejected by other services. (This was a real
  bug the login→me test caught: tokens with no audience were rejected by the
  audience rule.)
- **Login depends on key + operator presence.** With `AIREOS_PASETO_KEY` set,
  `main` builds a Signer + (if the bootstrap operator env is present) the
  account; login returns **503** when login is not configured (dev mode / no
  operator), never a silent bypass.
- **`me` reads the verified capability** from context (`CapabilityFromContext`)
  and returns `{subject, edge}` (the ADR-0047 capability-edge UI input).

## Files in scope

- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — `auth` tag; `/v1/auth/login`
    (`security: []`) + `/v1/auth/me`; `LoginRequest` / `LoginResponse` / `Identity`.
  - `oapi-codegen.api.yaml` — `include-tags: [resources, auth]`.
  - `services/api/internal/server/gen.go` — regenerated (adds Login/GetMe).
  - `services/api/internal/server/server.go` — `New` takes an `Authenticator` +
    `*auth.Signer`; `gateByDeclaredSecurity`; `Login` + `GetMe` handlers.
  - `services/api/internal/server/server_test.go` — login→me e2e, bad password,
    me-without-token; existing tests updated to the new `New` signature.
  - `services/api/main.go` — `buildAuth` returns verifier + signer + account.
  - `packages/go/auth/auth.go` — `IssueOperator(subject, audience)`.
  - `docs/tickets/README.md` — mark T-015b complete.
- **Create:** `docs/tickets/T-015b-operator-login-surface.md` — this ticket.

## Acceptance criteria

- [ ] `task codegen` regenerates `gen.go` with Login + GetMe + the resource ops;
      drift check is clean once committed.
- [ ] **Passing fixture**: valid credentials → 200 with a token; that token is
      accepted by `GET /v1/auth/me` → 200 `{subject:"operator:admin", edge:"human-agent"}`.
      **Violating fixtures**: wrong password → 401; `me` without a token → 401.
      (`TestOperatorLoginAndMe`.)
- [ ] `login` is reachable without a bearer (public); `me` + resources require one.
- [ ] Login returns 503 when no Signer/operator is configured (dev mode).
- [ ] Operator tokens carry `aud:"api"`; a no-audience token is rejected.
- [ ] OpenAPI updated; `$ref`s resolve (validator passes).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — done.
- [ ] Verification command runs clean (see below).

## Out of scope

- `task auth rotate-key` (operator key rotation) + a full `install.sh` — devops ticket.
- Operator session refresh / sliding TTL, `GET /v1/auth/me` scope details beyond
  subject+edge — follow-ups.
- Multi-operator user store, signup, password reset, OAuth — forbidden at P0 (ADR-0046).

## Diff budget

Split half of T-015. Actual: 1 created + 8 modified = 9 files, ≈300 LoC
(gen.go is generated).

## ADRs required

None new. Implements ADR-0046 (operator login, capability tokens) + ADR-0043
(oapi-codegen per-service slice, mixed security via the generated marker).

## Verification

Verified in a `golang:1.23` container:

```
task codegen                                            # regenerates api gen.go (login public, me/resources gated)
go build/vet ./packages/go/... ./services/api/... ./services/audit-service/...   # clean
go test ./packages/go/auth/... ./services/api/...        # incl. TestOperatorLoginAndMe
python infra/scripts/validate_contracts.py               # contract + schema validation passes
```

All **pass**: login→me round-trip (real key/signer/account/verifier), bad
password 401, me-without-token 401, resource CRUD unchanged, contract valid.
