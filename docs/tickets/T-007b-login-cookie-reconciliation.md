---
name: T-007b services/api login cookie reconciliation (HttpOnly session + CSRF double-submit + sliding refresh)
about: Move the operator PASETO out of the LoginResponse body into an HttpOnly session cookie, add the non-HttpOnly aireos_csrf cookie + double-submit middleware, and land sliding-refresh cookie rotation
title: "T-007b: services/api login cookie reconciliation (ADR-0047)"
labels: ["ticket", "P0"]
---

## Ticket ID and link

T-007b — see `docs/tickets/README.md`. Second of three T-007 slices.
Reconciles `services/api` to the ADR-0047 §Decision posture: PASETO opaque
to JS, double-submit CSRF, server-side sliding refresh. T-007a landed the
JS client wrapper that consumes this contract; T-007c lands the SPA shell
that round-trips through it. Lead lens: **Permission Model Reviewer**
(cookie + CSRF + 401 mechanics); **Tech Lead** (capability middleware
delta + codegen ripple).

## Goal

Make `services/api`'s operator-login surface match ADR-0047 §Decision:

- `POST /v1/auth/login` no longer returns the PASETO in the JSON body.
  Instead it sets an `HttpOnly; Secure; SameSite=Strict` session cookie
  (`aireos_session`) + a non-`HttpOnly` CSRF cookie (`aireos_csrf`); the
  body carries only `expiresAt` so the SPA can render time-to-expiry
  without reading cookies.
- All gated mutating routes verify `X-CSRF-Token` (header) equals
  `aireos_csrf` (cookie) when the request came in over the cookie path.
  Service-to-service (Authorization header, no cookie) is exempt.
- A new `POST /v1/auth/logout` (public, security: []) clears both cookies.
- Operator tokens within the second half of their TTL are rotated inline
  with the response (8h `OperatorTTL` ⇒ refresh window `< 4h`).

## Priority

P0. Without this, the T-007a CSRF/401 client wrapper has no server to
talk to; without it, T-007c can't land authenticated routes.

## Scope decisions (settled in consult / during implementation)

- **Cookie/header contract names** (settled in the T-007a consult,
  committed in `packages/shared-types/src/client.ts`):
    - `aireos_session` — HttpOnly session cookie.
    - `aireos_csrf` — non-HttpOnly random value; SPA reads to echo.
    - `X-CSRF-Token` — request header carrying the echoed value.
  T-007a's wrapper and T-007b's middleware reference these as constants;
  the contract is enforced by the unit tests on both sides.
- **`mw.Capability` extended** with `ExpiresAt time.Time` + `Audience string`.
  Adapters that can populate them (`packages/go/auth/middleware.go`,
  PASETO Verifier) do; adapters that can't (`services/api/main.go`'s
  `devAllowAll`) leave them zero-valued. Sliding refresh short-circuits
  on zero `ExpiresAt`. Backward-compatible for `audit-service` and
  `session-broker`, which only read `Subject`/`Edge`.
- **`packages/go/middleware/` stays cookie-agnostic.** A thin
  `cookieToAuthHeader` adapter in `services/api/internal/server/` copies
  `aireos_session` into the `Authorization` header before the existing
  `RequireCapability` runs. Browser callers use cookies; service-service
  callers use the header directly. Tech Lead lens favors this over
  teaching middleware about cookies (no other service has a browser to
  defend at P0).
- **CSRF gates only browser-path mutations.** Service-to-service traffic
  (Authorization header, no `aireos_session` cookie) is exempt by
  construction — the cookie presence is the discriminator. This avoids a
  CSRF false-positive on the outbox relay's `audit-service` calls (the
  ADR-0045 path) and on any other internal RPC.
- **Sliding refresh runs after the gate, not before.** It reads the
  verified Capability from the request context; if the Edge is
  `human-agent` and remaining TTL `< OperatorTTL/2`, it mints a new
  operator token (same Subject + Audience) and re-sets both cookies on
  the response. No client involvement (ADR-0047 §Decision: "no
  client-side refresh logic").
- **Middleware order pitfall surfaced and pinned in code.** The
  generated `ServerInterfaceWrapper` composes middlewares with
  `handler = m(handler)` per iteration — the LAST entry becomes the
  OUTERMOST wrapper at runtime. The `Middlewares` slice in
  `services/api/internal/server/server.go` is written innermost-first
  with an in-code comment so a future refactor doesn't silently invert
  the chain.
- **CSRF compared with constant-time.** A `subtleEqual` helper guards
  against length-leak timing oracles on the CSRF value. Inline (one-line
  XOR fold) instead of importing `crypto/subtle` for a single call.
- **CSRF token entropy.** 32 random bytes from `crypto/rand` → 64 hex
  chars; refreshed on each login and on every sliding-refresh rotation.
- **Cookie attributes**:
    - Both: `Secure`, `SameSite=Strict`, `Path=/`, `Max-Age = OperatorTTL`.
    - Session-only: `HttpOnly`.
  `Secure` is set unconditionally; modern browsers (Chromium ≥ 89,
  Firefox ≥ 75) treat localhost as a Secure context so dev works
  without a TLS terminator.

## Files in scope

- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — `LoginResponse` drops
    `token` (keeps `expiresAt`); add `/v1/auth/logout` (public, 204).
  - `services/api/internal/server/gen.go` — regenerated; gains `Logout`
    operation + the updated `LoginResponse` model.
  - `services/api/internal/server/server.go` — `Login` sets cookies (no
    body token); new `Logout` handler; new `cookieToAuthHeader`,
    `gateByDeclaredSecurity` extended with the CSRF check,
    `slidingRefresh` middlewares; cookie + header name constants.
  - `services/api/internal/server/server_test.go` — adds
    `TestLoginSetsCookies` (cookie attributes verified),
    `TestSessionCookieAuthenticates` (cookie unlocks `/v1/auth/me`),
    `TestAuthorizationHeaderStillWorks` (S2S path not regressed),
    `TestCSRFRequiredOnMutationsWithCookie` (no header / mismatched /
    matching), `TestLogoutClearsCookies` (Max-Age<0 on both),
    `TestSlidingRefresh` (short-TTL token rotates; long-TTL doesn't).
    Existing `TestOperatorLoginAndMe` updated to assert via cookies.
  - `packages/go/middleware/verifier.go` — `Capability` gains
    `ExpiresAt time.Time` + `Audience string`.
  - `packages/go/auth/middleware.go` — adapter populates the new fields.
  - `packages/shared-types/src/generated/openapi.ts` — regenerated by
    `task codegen` (LoginResponse + Logout op).
  - `docs/tickets/README.md` — T-007 split index updated.
- **Create:**
  - `docs/tickets/T-007b-login-cookie-reconciliation.md` — this ticket.

## Acceptance criteria

- [x] `task codegen` regenerates both consumers (Go + TS) cleanly; drift
      checks pass.
- [x] **Passing fixtures:** valid login → 200 with `Set-Cookie:
      aireos_session=…; HttpOnly; Secure; SameSite=Strict` and
      `Set-Cookie: aireos_csrf=…; Secure; SameSite=Strict`; the body
      carries `{expiresAt}` only — no `token` field. The same session
      cookie unlocks `GET /v1/auth/me` → 200. `POST /v1/resources` with
      both cookies + matching `X-CSRF-Token` → 201. A short-TTL token
      triggers cookie rotation on the next gated request.
      **Violating fixtures:** login body contains `"token"` → contract
      test fails (`TestLoginSetsCookies`); POST with cookie but no/wrong
      `X-CSRF-Token` → 403; POST with neither cookie nor header → 401;
      `Authorization: Bearer` (service-to-service) still works without
      CSRF (`TestAuthorizationHeaderStillWorks`); long-TTL token does
      not trigger rotation.
- [x] `POST /v1/auth/logout` returns 204 and sets both cookies with
      `Max-Age=-1` (delete).
- [x] OpenAPI / schemas updated; `validate_contracts.py` passes.
- [x] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [x] Unit tests cover valid + invalid + edge — see the seven server
      tests above.
- [x] Verification command runs clean (see below).

## Out of scope

- **`apps/web` shell + capability UI consumer** — T-007c.
- **Per-token revocation / signing-key rotation tooling** — ADR-0046
  defers `task auth rotate-key` to a devops ticket; here we only
  consume the existing `auth.Signer.IssueOperator` surface.
- **CSRF rule for service-to-service** — service-service traffic is
  Authorization-only and CSRF-exempt by design (no browser).
- **Refresh-via-explicit-endpoint** — ADR-0047 commits to inline
  cookie rotation; no `/v1/auth/refresh` route at P0.

## Known limit — Login CSRF

The double-submit CSRF defense gates *gated mutations on the cookie path*
(see `needsCSRF` in `server.go`: method-is-mutating AND session-cookie-
present). `POST /v1/auth/login` is public (`security: []`) and runs before
any session exists — so it is **not** behind a CSRF check.

That means an attacker with same-site-initiator access (e.g. an XSS on a
sibling subdomain or a same-origin form they can plant) can cross-cause a
victim's browser to `POST /v1/auth/login` with attacker-controlled
credentials, logging the victim INTO THE ATTACKER'S account. The victim
then sees attacker-controlled data and any data they enter goes to the
attacker. SameSite=Strict on the session cookie blocks *cross-site*
initiators but not *same-site* ones.

This is the classic "Login CSRF" class. It is not a confidentiality
break (the attacker doesn't learn anything about the victim's real
account) but it is an integrity break (the attacker can phish-by-proxy).

**Status at P0:** documented and accepted. Mitigations require either
(a) a pre-auth CSRF token issued on `GET /v1/auth/login-challenge` and
echoed in the login POST body, or (b) signed nonces baked into the SPA's
initial HTML page. Both add a roundtrip / a page-render dependency.
Defer to a hardening ticket (or to the Zitadel adoption window per
ADR-0046 — Zitadel's flows handle login-CSRF natively).

## Diff budget

9 files (7 modified + 1 created + 1 regenerated TS) / ≈ 350 authored LoC.
Within the default 12-file / 500-LoC cap.

## ADRs required

None new. Implements ADR-0047 §Decision items "Auth-token storage",
"CSRF defense: double-submit token", "Refresh: server-side cookie
rotation", and §Verification §6 (logout).

## Verification

Verified in `golang:1.23` and `node:22-alpine` containers on
`docker-compose_default`:

```
# Go: build + vet + unit + integration
go build / go vet                                                 # clean
go test ./packages/go/auth/...  ./services/api/...  ./services/audit-service/... ./services/session-broker/...
                                                                  # all packages ok
go test -tags integration ./services/audit-service/internal/store/... ./services/api/internal/store/...
                                                                  # both ok against live Postgres

# JS: install + codegen drift + typecheck + unit
pnpm install --frozen-lockfile                                    # clean
pnpm --filter @aireos/shared-types run codegen                    # regenerates cleanly
git diff --exit-code packages/shared-types/src/generated/openapi.ts # CLEAN after commit
pnpm -r run typecheck                                             # clean
pnpm -r --if-present run test                                     # 5/5

# Contract
python infra/scripts/validate_contracts.py                        # 10 fixtures + fragment all pass
```
