---
name: T-018a Ticket
about: SSH end-to-end vertical slice (c) — browser WS-auth enablement + TS client regen
title: "T-018a: shared CookieToAuthHeader (broker WS auth) + TS client regen"
labels: ["ticket"]
---

## Ticket ID and link

T-018a — first half of the SSH end-to-end vertical slice (c) per ADR-0048
§Hand-off. No B-NNN counterpart. Unblocks **T-018b** (`<TerminalView>` + session
UI): without it a browser cannot authenticate the session-broker WebSocket, and
the SPA has no typed `/v1/sessions` client.

## Goal

Make the browser cockpit path reachable: give the session-broker WebSocket
upgrade cookie-based auth (browsers cannot set `Authorization` on a WebSocket),
and regenerate the SPA's typed OpenAPI client so it knows the `/v1/sessions`
surface T-016b added.

## Priority

P0 — v0.1.0 wedge (ADR-0047 same-origin cookie posture; ADR-0048 §item-4 "Cookie
auth on WS upgrade", the spike's unexercised item). No new ADR.

## Files in scope

- Modify: `packages/go/middleware/middleware.go` (`SessionCookieName` const +
  `CookieToAuthHeader` shared middleware)
- Create: `packages/go/middleware/cookieauth_test.go`
- Modify: `services/api/internal/server/server.go` (use shared middleware; drop
  the local `cookieToAuthHeader`; `sessionCookieName = mw.SessionCookieName`)
- Modify: `services/session-broker/internal/server/server.go` (wire
  `CookieToAuthHeader` before `RequireCapability` on the channel group)
- Modify: `services/session-broker/internal/server/server_test.go` (cookie-only
  upgrade authenticates)
- Modify: `packages/shared-types/src/generated/openapi.ts` (regenerated)  *(mechanical)*
- Create: `docs/tickets/T-018a-ws-cookie-auth-ts-client-regen.md` (this file)
- Modify: `docs/tickets/README.md` (T-018a entry)

8 files; 1 mechanical (generated TS). Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (design-system preflight + war-room consult, 2026-05-30)

- **Keystone gap (preflight):** `RequireCapability` reads only `Authorization`;
  the broker had no cookie→header adapter, and the WebSocket API cannot set
  request headers. So a browser could never authenticate the broker channel — it
  carries only the HttpOnly `aireos_session` cookie.
- **Promote, don't duplicate (approved).** api's local `cookieToAuthHeader` is
  promoted to `packages/go/middleware.CookieToAuthHeader(cookieName)` (shared-infra
  pattern, ADR-0043), and `SessionCookieName` becomes the one cross-service cookie
  name. api re-points to it; the broker wires it on the channel group. The PASETO
  token stays opaque to JS — the adapter runs server-side only.
- **WS upgrade is a GET → no CSRF.** Double-submit CSRF applies to mutations
  (create/end session REST); the upgrade is idempotent and exempt, same as api's
  `needsCSRF`.
- **Split a/b (approved).** This enablement is backend/infra and independently
  verifiable (broker authenticates a cookie-only upgrade; TS types include
  sessions). `<TerminalView>` + session UI is T-018b. Same T-016/T-017 precedent.
- **TS client was stale.** T-016b ran only the Go codegen leg; the generated
  `openapi.ts` had zero `/v1/sessions`. Regenerated with the pinned
  `openapi-typescript@7.4.4` (identical to `task codegen`'s TS leg) — now exposes
  `GET/POST /v1/sessions`, `/v1/sessions/{id}`, `/v1/sessions/{id}/end`, and the
  `Session`/`SessionCreate` types.

## Acceptance criteria

- [x] `middleware.CookieToAuthHeader` translates the named cookie to a bearer
      when `Authorization` is absent; an existing `Authorization` wins; empty/no
      cookie is a no-op (unit test).
- [x] api uses the shared middleware (local copy removed) and its existing
      cookie/CSRF/refresh tests still pass.
- [x] The broker channel authenticates a **cookie-only** upgrade (no
      `Authorization`) — reaching the target-unavailable branch (424) proves the
      capability gate passed (server test).
- [x] The regenerated TS client contains the `/v1/sessions` paths +
      `Session`/`SessionCreate` types.
- [x] `golangci-lint` (T-011) clean; `go vet`/`gofmt` clean.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Unreachable WS — violating:** broker gated on `Authorization` only → browser
  upgrade is always 401 (no way to send the header). **Passing:**
  `CookieToAuthHeader` translates the cookie → upgrade authenticates.
- **Contract drift — violating:** two hard-coded `"aireos_session"` literals
  (api + broker) drift apart. **Passing:** one `mw.SessionCookieName`.
- **Stale client — violating:** SPA cannot type a `/v1/sessions` call (paths
  absent from `openapi.ts`). **Passing:** regenerated client exposes them.

## Out of scope

- `<TerminalView>` (xterm.js), session list/start/end UI, dev WS proxy → **T-018b**.
- Adding the xterm dependency → **T-018b**.
- Prod reverse-proxy (Caddy/Traefik) same-origin routing of the broker path →
  release/devops (OQ-3).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 8 files (1 mechanical), within budget.

## ADRs required

- None new. Implements ADR-0047 cookie posture on the WS upgrade + ADR-0048
  §item-4; advances the shared-middleware pattern (ADR-0043).

## Verification

```sh
# regenerate the TS client (pinned openapi-typescript, == task codegen TS leg)
docker run --rm -v "$PWD:/work" -w /work node:22-alpine \
  npx -y openapi-typescript@7.4.4 docs/api-contracts/openapi.v3.fragment.yaml \
    -o packages/shared-types/src/generated/openapi.ts

# Go build + vet + gofmt + tests + lint (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 bash -c '
  test -z "$(gofmt -l packages/go/middleware services/api services/session-broker)" &&
  (cd packages/go/middleware && go build ./...) &&
  (cd services/api && go build ./...) && (cd services/session-broker && go build ./...) &&
  go vet ./packages/go/middleware/... ./services/api/... ./services/session-broker/... &&
  go test ./packages/go/middleware/... ./services/api/internal/server/... ./services/session-broker/internal/server/... &&
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run \
    ./packages/go/middleware/... ./services/api/... ./services/session-broker/...'
```

Expected: gofmt/build/vet clean; middleware cookie-auth unit tests pass; api
server suite (cookie/CSRF/refresh) still green; broker cookie-only upgrade → 424;
regenerated client has `/v1/sessions`; lint clean. (Confirmed 2026-05-30 on
Docker / golang:1.23 / node:22-alpine.)
