---
name: T-007a Frontend toolchain + packages skeleton + TS codegen wiring
about: Materialize the pnpm workspace, stand up packages/{ui,shared-types,config} skeletons, extend task codegen with openapi-typescript, and land the first pnpm-lock.yaml
title: "T-007a: Frontend toolchain + packages skeleton + TS codegen wiring (ADR-0047)"
labels: ["ticket", "P0"]
---

## Ticket ID and link

T-007a — see `docs/tickets/README.md`. First of three slices for T-007 (web
dashboard shell). Pivots the stack from Go backend to TypeScript/React. This
slice lands the workspace materialization + codegen wiring + lockfile; the
companion slices are T-007b (login cookie reconciliation on `services/api`)
and T-007c (apps/web shell + capability UI). Lead lens: **Frontend Lead**
(ADR-0047 package boundaries); **Tech Lead** (codegen extension); **Offline-
Install Engineer** (pnpm content store, supply chain).

## Goal

Materialize the pnpm workspace declared by ADR-0042 / ADR-0047 — root
`package.json`, `pnpm-lock.yaml`, and `packages/{config,shared-types,ui}` as
workspace members — and extend `task codegen` so the OpenAPI fragment now
drives a third consumer (`openapi-typescript` + the `openapi-fetch` client
wrapper). No apps/web app code yet; that lands in T-007c.

## Priority

P0. ADR-0047 §Hand-off names this as the first frontend-implementation
ticket; T-010 explicitly deferred the npm precedent here.

## Scope decisions (settled in consult / during implementation)

- **Three-way T-007 split approved.** T-007a (this), T-007b (login cookie
  reconciliation on `services/api`), T-007c (apps/web shell + capability UI).
  Single-ticket T-007 would exceed the diff budget; three slices each fit.
- **pnpm 10.32.1 pinned** via `packageManager` in root `package.json`. Matches
  the host's installed version exactly to avoid lockfile churn. `.npmrc`
  already has `engine-strict=true` + `frozen-lockfile=true` (T-002).
- **`task codegen` extends in-place.** One target generates Go chi-server
  stubs (per ADR-0043) AND the TS client (per ADR-0047). Mirrors the T-014
  per-service slicing precedent: same fragment, multiple consumers, one
  invocation.
- **TS client: `openapi-typescript` (devDep) + `openapi-fetch` (runtime).**
  Per ADR-0047 §Decision item "TS client codegen." Generated artifact lives
  at `packages/shared-types/src/generated/openapi.ts` and is committed.
- **Wrapper centralizes CSRF + 401 chokepoint.** `createApiClient` in
  `packages/shared-types/src/client.ts` auto-attaches `X-CSRF-Token` from the
  `aireos_csrf` cookie on every POST/PUT/PATCH/DELETE, and routes 401s to a
  caller-supplied `onUnauthorized` hook (the SPA's 401 chokepoint per
  ADR-0047 §Decision). Cookie/header names match the T-007b contract.
- **packages/ui is a skeleton** with an empty barrel. Components/hooks
  enumerated in ADR-0047 §Verification §4 land in T-007c — vendoring shadcn
  primitives speculatively now would violate "search-before-create" in
  `design-system-governance.md`.
- **packages/config ships static config files only** at T-007a: shared
  `tsconfig.base.json` + `tailwind.config.ts` (the Token Map per
  design-system-governance §2). `eslint.config.ts` + `prettier.config.cjs`
  defer to T-012, which is the explicit JS-side lint follow-up.
- **No `@radix-ui/*` deps yet.** ADR-0047 §Verification §4 names them on
  `packages/ui`; they land with the first vendored shadcn component in
  T-007c. Speculative-deps avoided.
- **Verification loop runs in Docker.** `node:22-alpine` container with a
  named `pnpmstore` volume on the existing `docker-compose_default` network.
  Mirrors the backend pattern (`golang:1.23` + `gomodcache` volume).

## Files in scope

- **Create:**
  - `package.json` — root; `packageManager: pnpm@10.32.1`; `engines.node: >=22`; cross-workspace scripts.
  - `pnpm-lock.yaml` — first lockfile (the npm precedent T-010 deferred).
  - `packages/config/package.json` — workspace member; static config exports.
  - `packages/config/tsconfig.base.json` — shared strict TS config.
  - `packages/config/tailwind.config.ts` — Tailwind v4 Token Map (semantic colors + radius).
  - `packages/shared-types/package.json` — declares `openapi-fetch` (dep) + `openapi-typescript` (devDep); `codegen` + `typecheck` scripts.
  - `packages/shared-types/tsconfig.json` — extends config base.
  - `packages/shared-types/src/index.ts` — public barrel (re-exports `createApiClient` + generated types).
  - `packages/shared-types/src/client.ts` — `createApiClient` factory: CSRF auto-attach + 401 chokepoint hook + injectable cookie reader.
  - `packages/shared-types/src/generated/openapi.ts` — committed `openapi-typescript` output.
  - `packages/ui/package.json` — react/react-dom; `typecheck` script.
  - `packages/ui/tsconfig.json` — extends config base.
  - `packages/ui/src/index.ts` — empty barrel; components land in T-007c.
  - `docs/tickets/T-007a-frontend-toolchain-bootstrap.md` — this ticket.
- **Modify:**
  - `Taskfile.yml` — extend `codegen` task with `pnpm --filter @aireos/shared-types run codegen`.
  - `.github/workflows/ci.yml` — add `js` job (pnpm install, codegen, drift check, `pnpm -r run typecheck`).
  - `docs/tickets/README.md` — T-007 split index entries.

## Acceptance criteria

- [ ] `pnpm install --frozen-lockfile` succeeds against the committed lockfile.
- [ ] `task codegen` regenerates both Go stubs AND `packages/shared-types/src/generated/openapi.ts` cleanly; `git diff --exit-code` clean after.
- [ ] `pnpm -r run typecheck` clean across all three packages.
- [ ] **Violating fixture:** delete `packages/shared-types/src/generated/openapi.ts` → `task codegen` regenerates it; CI `js` job's drift check fails when the generated file is stale. **Passing fixture:** after `task codegen` the SHA of the generated file matches what's committed; the wrapper exposes the typed `paths` from the fragment (audit + resources + auth surfaces) at the type level.
- [ ] Root `package.json` pins `packageManager: pnpm@10.32.1` and declares `engines.node: >=22`.
- [ ] `.npmrc` posture (`frozen-lockfile=true`, `engine-strict=true`, `node-linker=isolated`) verified by the install step.
- [ ] OpenAPI / schemas updated — **N/A** (T-007a does not touch the fragment; T-007b does).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [x] Unit tests cover valid + invalid + edge — 5 black-box tests in `packages/shared-types/src/client.test.ts` (vitest) assert the CSRF cookie/header contract on POST + cookie present, POST + cookie absent, GET (header never attached); 401 chokepoint fires `onUnauthorized` exactly once; non-401 responses (200/403/500) do not fire it. Tests added by the post-consult tightening pass; see the second commit on this ticket.
- [ ] Verification command runs clean (see below).

## Out of scope

- `apps/web/` Vite shell, routes, `<CapabilityProvider>` / `<CapabilityGate>` — T-007c.
- `services/api` httpOnly cookie + CSRF middleware + sliding refresh — T-007b.
- ESLint + Prettier configs (the "no `dangerouslySetInnerHTML`" rule and the
  "no direct `fetch` outside `createApiClient`" rule) — T-012.
- `@radix-ui/*` primitives + vendored shadcn components — T-007c (with the
  first component that needs them).
- `apps/admin/` — deferred to a P1 ticket per ADR-0047 §Decision (same stack;
  no second framework re-decision).

## Diff budget

15 authored files + 2 generated (`pnpm-lock.yaml`, `src/generated/openapi.ts`) =
17 total. **Exceeds the default ≤12 files cap.** Explicitly approved in the
T-007 split consult: the alternative (single T-007) would have ~25 files /
~700 LoC and fail the 500-LoC limit too. Total authored LoC ≈ 290.

## ADRs required

None new. Implements ADR-0042 (pnpm workspace materialization + `task codegen`)
and ADR-0047 (TS client codegen + `packages/{ui,shared-types,config}`
boundaries + `createApiClient` 401 chokepoint).

## Verification

Verified in a `node:22-alpine` container on `--network docker-compose_default`
with a named `pnpmstore` volume (mirrors the backend `golang:1.23` +
`gomodcache` posture):

```
docker run --rm --network docker-compose_default \
  -v "$PWD":/work -w /work \
  -v pnpmstore:/root/.local/share/pnpm/store \
  node:22-alpine sh -lc '
    corepack enable
    corepack prepare pnpm@10.32.1 --activate
    pnpm install --frozen-lockfile
    pnpm --filter @aireos/shared-types run codegen
    git diff --exit-code packages/shared-types/src/generated/openapi.ts
    pnpm -r run typecheck
  '
```

All expected to **pass**: install hydrates the workspace from the committed
lockfile; codegen is a no-op against the committed generated artifact;
typecheck clean across `@aireos/config`, `@aireos/shared-types`, `@aireos/ui`.

### Verification — run and passed

Two commits land this ticket: the base (`6f2a77f`) and a post-consult
tightening pass (`4a3965c`). Combined verification in `node:22-alpine` on
`docker-compose_default`:

- `rm -rf node_modules && pnpm install --frozen-lockfile` — 5.3s, lockfile
  up to date.
- `pnpm --filter @aireos/shared-types run codegen` — regenerates cleanly;
  `git diff --exit-code packages/shared-types/src/generated/openapi.ts` —
  CLEAN (codegen is idempotent against the committed artifact).
- `pnpm -r run typecheck` — clean across `@aireos/{config,shared-types,ui}`.
- `pnpm -r --if-present run test` — **5/5 pass** in 2.7s (the CSRF/401
  contract under `@aireos/shared-types`).

Adjacent surfaces re-run during the post-tightening review pass to confirm
no regressions:

- `python infra/scripts/validate_contracts.py` — JSON-Schema fixtures
  (5 valid + 5 invalid) and the OpenAPI fragment pass.
- `go build` + `go test` across `packages/go/...` and `services/...`
  (auth + api + audit-service + session-broker) — clean.

Post-consult tightening pass (commit `4a3965c`) addresses four items
surfaced in the war-room review of the base commit:

1. Added vitest + 5 unit tests on the CSRF / 401 contract (the line above).
2. Tightened runtime dep ranges from `^` to `~` (`openapi-fetch`, `react`,
   `react-dom`) per ADR-0042 supply-chain doctrine. Dev deps stay on `^`.
3. Removed the speculative `__INTERNAL__` export from `client.ts`; tests
   are black-box on wire-level behavior and don't need it.
4. Documented in `packages/config/tailwind.config.ts` that four of the
   seven design-system-governance §2 token categories (typography,
   spacing, elevation, motion) ride on Tailwind v4 defaults at T-007a;
   audit during T-007c when concrete consumers exist.

Surfaced but deliberately not taken (would scope-creep):

- `esbuild@0.21.5` build-script warning on every `pnpm install` (vitest
  transitive). The deny is correct for offline-install posture; the
  warning is the visibility signal. If it becomes noise, add
  `pnpm.onlyBuiltDependencies: []` to root `package.json`.
