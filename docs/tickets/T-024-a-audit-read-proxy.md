---
name: T-024-a Ticket
about: operator audit-timeline read-proxy through services/api (Option B)
title: "T-024-a: services/api audit read-proxy — GET /v1/audit/events (v0.1.0)"
labels: ["ticket", "feature", "security"]
---

## Ticket ID and link

T-024-a — backend half of **T-024** (operator audit-timeline view), surfaced by
T-021d-b/WD-1. No B-NNN counterpart. WD-1 step 8 ("operator visits the audit
timeline") is not reachable in the cockpit: Caddy has no route to
`audit-service`, and the operator cookie's `api` audience would not pass
`audit-service`'s verifier (same class as the ADR-0050 broker fix). Events are
already produced + chained + CLI-verifiable (`audit-service verify`); only the
in-UI read path is missing. Binding specs: ADR-0043, ADR-0046, ADR-0047.

## Decision: Option B — api read-proxy (CEO-approved, split a/b)

Rather than expose `audit-service` through Caddy and re-issue the operator cap
for the `audit-service` audience (Option A), `services/api` — already the
operator-facing gateway with the operator's `api` capability — **proxies** the
read to `audit-service` using a **service token** it mints itself (workload
`api`, audience `audit-service`, reusing `auditTokenSource`). One audience
boundary, one origin, no new Caddy route, no cross-audience cookie handling.
This (a) is the backend proxy + contract + tests; **T-024-b** is the SPA
timeline page + the WD-1 harness assertion + release-artifact flip.

## Goal

Operator-gated, read-only `GET /v1/audit/events` on `services/api` that forwards
`limit`/`cursor` to `audit-service`'s `GET /v1/events` with a minted service
token and streams the page envelope back verbatim.

## Priority

P0 — release-gating (closes the backend half of the last open v0.1.0 gap).

## Files in scope

- Modify: `docs/api-contracts/openapi.v3.fragment.yaml` (+ `audit-read` tag, `GET /v1/audit/events` → `{items:[AuditEvent], page:PageMeta}`, reuses `LimitParam`/`CursorParam`)
- Modify: `oapi-codegen.api.yaml` (include-tag `audit-read`)
- Regenerate: `services/api/internal/server/gen.go` (`ListAuditEvents`, gated via `BearerAuthScopes`)
- Regenerate: `packages/shared-types/src/generated/openapi.ts` (one schema, two consumers — ADR-0047)
- Modify: `services/api/internal/server/server.go` (`ListAuditEvents` handler; `auditURL` field; `New` 5-arg)
- Modify: `services/api/internal/server/server_test.go` (5-arg `New`; proxy + 503 tests)
- Modify: `services/api/main.go` (pass `os.Getenv("AIREOS_AUDIT_URL")` to `server.New`)
- Create/modify: `docs/tickets/T-024-a-*.md` (this) + `docs/tickets/README.md`

**7 files** — within the ≤12 / ≤500 LoC budget.

## Behaviour notes

- **Operator-gated, same edge as the registry.** The generated wrapper sets
  `BearerAuthScopes` (gated exactly like `ListResources`); the handler also
  requires an operator subject (`operatorSubject`) before proxying — no
  anonymous reads.
- **Service token reuse.** When a `*auth.Signer` is present the handler mints
  `IssueService("api", "audit-service")` per request and sets it as the upstream
  bearer — the same token authority the api outbox relay already uses (T-022).
  No new key, no new audience.
- **Fail loud, not silent.** No `AIREOS_AUDIT_URL` → **503** (`unavailable`),
  never an empty page passed off as "no events". Upstream unreachable → **502**
  (`bad_gateway`). Status + body from `audit-service` otherwise pass through.
- **Read-only.** Only `GET`; no mutation surface on the proxy (the chain is
  append-only at the authority — ADR-0045).

## Acceptance criteria

- [x] `GET /v1/audit/events` exists on api, operator-gated, forwards
      `limit`/`cursor` to `audit-service` `/v1/events` with a minted service token.
- [x] Page envelope passes through verbatim (status + body).
- [x] No `AIREOS_AUDIT_URL` → 503; the proxy never fabricates an empty page.
- [x] Go build/vet/golangci-lint/tests clean on `services/api`; no codegen drift
      (Go `gen.go` + TS `openapi.ts` regenerate byte-stable); shared-types typechecks.

### Violating vs. passing fixtures

- **Expose `audit-service` through Caddy + re-mint the operator cap for the
  `audit-service` audience — violating** (Option A: two audiences, cross-origin
  cookie handling, ADR-0050-class boundary risk). **Passing:** api proxies with
  its own service token (one audience boundary).
- **Missing `AIREOS_AUDIT_URL` → return `{items:[]}` — violating** (silent empty
  timeline masks a misconfigured deploy). **Passing:** 503 `unavailable`.

## Out of scope (→ T-024-b)

- SPA audit-timeline page (`packages/ui` component + `apps/web` `/audit` route,
  capability-gated) consuming this endpoint.
- WD-1 harness assertion that the operator reads the timeline through
  Caddy → api → audit-service.
- Flipping the T-027 release artifacts' remaining-gate to `[]`.

## ADRs required

- None new. Conforms to ADR-0043 (api is the operator-facing chi-server gateway),
  ADR-0046 (service-service PASETO), ADR-0047 (one schema, two consumers). The
  read-proxy is a faithful composition of those — no Accepted ADR is contradicted
  (G2 clear).

## Verification

```sh
# Go: build/vet/lint/test the api module
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c '
  gofmt -l services/api/...
  go build ./services/api/... && go vet ./services/api/... && go test ./services/api/...
  (cd services/api && go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./...)'
# codegen drift (VER-2): regenerate Go + TS, assert no diff
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c \
  'go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 --config oapi-codegen.api.yaml docs/api-contracts/openapi.v3.fragment.yaml'
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && corepack prepare pnpm@10.32.1 --activate && pnpm --filter @aireos/shared-types run codegen && pnpm --filter @aireos/shared-types run typecheck'
git diff --quiet services/api/internal/server/gen.go packages/shared-types/src/generated/openapi.ts && echo "no drift"
```

Confirmed 2026-06-01: gofmt clean; build/vet/golangci-lint(exit 0)/tests pass
(incl. `TestListAuditEventsProxies` + `TestListAuditEventsUnavailableWithoutAuditURL`);
Go + TS codegen byte-stable; shared-types typechecks clean.
