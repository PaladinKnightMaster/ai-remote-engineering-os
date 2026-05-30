# Tickets — T-NNN ladder

This directory is the canonical home for committed T-NNN ticket files. Each file follows the format in `.github/ISSUE_TEMPLATE/ticket.md`.

Two ladders coexist in this repo and should not be confused:

- **B-NNN** — backlog rows in `../requirements/backlog_v3.csv` (frozen v3.0.1 baseline). Requirements/feature scope.
- **T-NNN** — tickets here. Implementation units. Some T-NNN tickets implement a B-NNN row; others are bootstrap or hygiene tickets (skeleton, lint enforcement) with no B-NNN counterpart.

## Index

### Bootstrap and skeleton (T-001..T-010)

These are enumerated in `../../.github/ISSUE_TEMPLATE/ticket.md` and tracked there until each is filed as a ticket file in this directory.

- **T-001** Repo skeleton + root documentation — *complete* (the existing repo is the artifact; see commit history).
- [T-002 — Repo skeleton commit (go.work, pnpm-workspace, Taskfile, MVP compose)](T-002-repo-skeleton-commit.md) — *complete*. (Backlog row title "Postgres + Redis/NATS + API + web" predates ADR-0045 and ADR-0047; the actual P0 compose is Postgres only.)
- [T-003 — Shared domain schemas (Organization, User, Resource, Session, AuditEvent)](T-003-shared-domain-schemas.md) — *complete*.
- **T-004** audit-service minimal append-only API — *split* (a whole service plus the shared Go packages exceeds the diff budget; split into a scaffolding half and a service half at consult sign-off):
  - [T-004a — Shared Go scaffolding (middleware + migrate + codegen wiring)](T-004a-shared-go-scaffolding.md) — *complete*. Lands `packages/go/middleware/` + `packages/go/migrate/`, `task codegen` / `task migrate`, and the `aireos.local` module-path prefix (ADR-0043 / ADR-0044 §Hand-off).
  - [T-004b — audit-service minimal append-only API](T-004b-audit-service.md) — *complete*. The chain-authority service: `POST /v1/events` ingest (dedup + chained-hash), read surface, forward-only migration, tests (ADR-0045 / ADR-0044).
- **T-005** resource registry CRUD API — *split* (owning service: `services/api`; split at consult sign-off):
  - [T-005a — services/api skeleton + resource-registry store + migration](T-005a-api-skeleton-resource-store.md) — *complete*. Stands up `services/api` with the `resources` table, store CRUD, `0001` up/down migration, and a per-service migrations-table fix to `packages/go/migrate`.
  - [T-005b — resource registry HTTP surface (`/v1/resources` CRUD)](T-005b-resource-http-surface.md) — *complete*. chi-server resource CRUD on `services/api`, generated per-service from the contract (include-tags), gated by `RequireCapability` + PASETO; CI extended to cover `auth` + `api`.
- [T-006 — session-broker skeleton (HTTP/2 + WebSocket)](T-006-session-broker-skeleton.md) — *complete*. `services/session-broker`: a capability-gated WebSocket channel (`github.com/coder/websocket`) + an HTTP/2 (`h2c`) readiness endpoint over the shared middleware chain. Closes ADR-0043 §Verification item 5 and settles its WebSocket-library choice. Session-lifecycle REST + protocol deferred to the session feature ticket.
- **T-007** web dashboard shell — *split* (pnpm workspace materialization, the login-cookie reconciliation against ADR-0047, and the apps/web shell + capability UI together exceed the diff budget; split at consult sign-off into three slices, ADR-0047):
  - [T-007a — Frontend toolchain + packages skeleton + TS codegen wiring](T-007a-frontend-toolchain-bootstrap.md) — *pending*. Lands the root pnpm workspace + `pnpm-lock.yaml`, `packages/{config,shared-types,ui}` skeletons, the `openapi-typescript` + `openapi-fetch` client wrapper (`createApiClient` with CSRF auto-attach + 401 chokepoint), and extends `task codegen` so the OpenAPI fragment drives a third consumer.
  - [T-007b — `services/api` login cookie reconciliation](T-007b-login-cookie-reconciliation.md) — *complete*. Moves the operator PASETO from the `LoginResponse` JSON body into an `HttpOnly; Secure; SameSite=Strict` session cookie set by `services/api`, adds the non-HttpOnly `aireos_csrf` cookie + double-submit middleware, sliding-refresh cookie rotation (operator TTL second half), and `POST /v1/auth/logout`. Extends `mw.Capability` with `ExpiresAt`+`Audience` (backward-compat). Settles the T-015b-vs-ADR-0047 mismatch flagged in the T-007 consult.
  - [T-007c — `apps/web` shell + capability UI + login consumer](T-007c-web-shell-capability-ui.md) — *complete*. Vite 6 + React 18 SPA with `react-router-dom` v7, `<CapabilityProvider>` / `<CapabilityGate>` + WS/SSE hook skeletons in `packages/ui`, the login route consuming the typed client from T-007a + cookies from T-007b, route guards via `GET /v1/auth/me`, 401 chokepoint, Tailwind v4 CSS-first token map, Vite dev proxy to `services/api`. Verified end-to-end against a live `services/api` (login → cookie-only `/v1/auth/me` → CSRF gate → logout). Closes T-007.
- [T-008 — Auth placeholder adapter (packages/go/auth/, PASETO v4)](T-008-auth-placeholder.md) — *complete*. In-process PASETO v4 capability tokens (`Signer`/`Verifier`/`CapabilityToken`), `AsMiddlewareVerifier` adapter to the T-004a port, and audit-service wired to verify real tokens (`AIREOS_PASETO_KEY`). Operator login + key rotation + `install.sh` deferred to the `services/api` ticket (ADR-0046 §Hand-off).
- [T-009 — OpenAPI contract scaffold (shared components + security + envelopes)](T-009-openapi-contract.md) — *complete*. (Backlog title "+ generated client types" predates ADR-0043/0047, which assign codegen to the consumer tickets — T-004 Go stubs, T-007 TS types; T-009 lands the contract they generate from.)
- [T-010 — CI checks (build, test, schema validation, codegen drift, lint hook)](T-010-ci-checks.md) — *complete*. GitHub Actions `ci.yml`: Go build/vet/test + Postgres-backed integration, oapi-codegen drift gate, OpenAPI + JSON-Schema validation (`infra/scripts/validate_contracts.py`), and the `task lint` stub hook. Dormant until a remote is configured; validation Python-based (npm precedent deferred to T-007).

### Operator login (T-015) — closes the auth bits ADR-0046 deferred from T-008

- [T-015a — Operator auth primitives + provisioning (argon2id + authctl)](T-015a-auth-primitives-provisioning.md) — *complete*. argon2id password hashing in `packages/go/auth`, the env-provisioned bootstrap operator account in `services/api`, and the `authctl` (`task auth`) provisioning CLI.
- [T-015b — operator login HTTP surface (`/v1/auth/login` + `/v1/auth/me`)](T-015b-operator-login-surface.md) — *complete*. Public `POST /v1/auth/login` issues an audience-scoped operator PASETO token; gated `GET /v1/auth/me` returns the verified capability. Mixed public/gated routing via the generated `BearerAuthScopes` marker; closes the operator-login bits ADR-0046 deferred from T-008.

### Verification-gap closures (T-011..)

- [T-011 — Go-side lint suite (ADR-0044/0045/0046 + v0.1.0 SEC-8)](T-011-go-lint-suite.md) — *complete* (release-blocker). golangci-lint via `go run` enforces PASETO isolation (depguard), `http.DefaultServeMux`/`http.Handle`/`http.HandleFunc` bans (forbidigo), and `panic()` ban in production code; two custom Go tools (`tools/migrate-lint`, `tools/forbidden-features-check`) enforce ADR-0044 forward-only migrations and ADR-0046 forbidden-features absence. All 8 Go workspace modules lint clean on `main`; 6 self-tests + 3-case real-repo verification pass. Closes v0.1.0 release-criteria SEC-8.
- [T-012 — JS-side lint suite (ADR-0047 + v0.1.0 SEC-7)](T-012-js-lint-suite.md) — *complete* (release-blocker). Shared ESLint flat config in `@aireos/config` enforcing the v0.1.0 release-criteria SEC-7 rules (no direct `fetch()` outside `createApiClient`, no `localStorage`/`sessionStorage` for tokens, no `dangerouslySetInnerHTML`), plus the `tools/forbidden-deps.mjs` script blocking meta-framework deps in `apps/*/package.json` (closes ADR-0047 §Verification §1). Programmatic self-tests in `packages/config/eslint-rules.test.ts` (4/4) + Node-test self-tests for `forbidden-deps.mjs` (3/3) prove the rules fire on intentionally-broken fixtures. The workspace lint runs clean on the existing T-007a/b/c code without modification — proves the chokepoint architecture was correctly built.
- [T-013 — Issue-template ladder drift](T-013-issue-template-drift.md) — P2, *complete*. Replaced the stale inline first-10 list in `.github/ISSUE_TEMPLATE/ticket.md` with a pointer to this file. Surfaced during T-002.
- [T-014 — Per-service OpenAPI contract slicing for oapi-codegen](T-014-per-service-openapi-codegen-slice.md) — P1, *complete*. `task codegen` now emits only the owning service's chi-server surface (operation tags + `include-tags`); audit-service is re-seated onto the generated `ServerInterface`. Closes ADR-0043 §Verification item 2; surfaced during T-004b. Owner: Tech Lead.

### SSH end-to-end vertical slice (T-016+) — per ADR-0048 §Hand-off

The cockpit SSH slice splits into four sub-slices (a–d), each its own
consult, analogous to T-007. Slice (a) is itself split (persistence/secrets
vs. REST surface) along the proven T-005a/5b boundary.

- [T-016a — sessions + resource-secret migrations + pgcrypto provisioning](T-016a-sessions-schema-resource-secret.md) — *complete*. The `sessions` table (ADR-0048 §3; `resource_id` UUID FK fix), the pgcrypto-encrypted `resources.connection_secret` BYTEA column (§7 / §SEC-12), the `api resources genkey|set-secret` provisioning subcommand (`task resources`), and the `AIREOS_RESOURCE_SECRET_KEY` vs `AIREOS_PASETO_KEY` separation guard. Audit-silent (no outbox yet). No HTTP surface.
- [T-016b — `/v1/sessions` REST control plane](T-016b-sessions-rest-control-plane.md) — *complete*. `createSession`/`listSessions`/`getSession`/`endSession` on `services/api`, generated per-service via oapi-codegen (`sessions` added to `include-tags`), gated on the existing `human-agent`+`service-service` edges. Operator-scoped (caller's capability subject is `operator_id`; no cross-operator scope, ADR-0048 §5); `Session` schema narrowed to the DB CHECK reality (`mode=terminal`, `status ∈ {pending,active,closed,terminated}`); unknown `resourceId` → 404 via FK; `end` idempotent + non-downgrading. Audit-silent (broker owns `session.*`).
- **T-017** — session-broker SSH gateway + per-input-line audit emission (builds the first `packages/go/outbox/`). *Pending.*
- **T-018** — `apps/web` `<TerminalView>` + session lifecycle UI. *Pending.*
- **T-019** — cast-file persistence layout + audit-chain linkage + cleanup policy. *Pending.*

## Conventions

- Filename: `T-NNN-short-kebab-slug.md`.
- Format: `.github/ISSUE_TEMPLATE/ticket.md`.
- T-NNN numbers are assigned at ticket creation and never reused. T-001..T-010 are reserved for the bootstrap ladder above; subsequent tickets start at T-011.
- A ticket's dependencies should be expressed by the *target it creates* ("whichever ticket first creates `packages/go/outbox/`") in preference to a T-NNN reference, so the dependency survives renumbering.
- Acceptance criteria must include a concrete violating fixture and a concrete passing fixture for any rule the ticket lands.

## Out of scope for this directory

- Backlog/feature rows — those live in `../requirements/backlog_v3.csv` (frozen) and any future post-v3.0.1 backlog artifact.
- ADRs — those live in `../adr/`.
- Spec docs — those live under the relevant `../<concern>/` directory.
