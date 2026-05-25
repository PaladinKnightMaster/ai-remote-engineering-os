---
name: T-007c apps/web shell + capability UI + login consumer
about: Vite 6 + React 18 + react-router-dom v7 SPA, CapabilityProvider/Gate, WS/SSE hook skeletons, and the login route that closes the end-to-end loop with T-007b
title: "T-007c: apps/web shell + capability UI + login consumer (ADR-0047)"
labels: ["ticket", "P0"]
---

## Ticket ID and link

T-007c — see `docs/tickets/README.md`. Last of three T-007 slices. Closes
ADR-0047 §Verification §1–§4 end-to-end: the SPA shell exists, the
client wrapper from T-007a flows through the SPA, the cookie posture
from T-007b round-trips against the live `services/api`. Lead lens:
**Frontend Lead** (apps/web shape, capability-edge UI primitives);
**Permission Model Reviewer** (401 chokepoint + route guard wiring).

## Goal

Land `apps/web/` as a working Vite 6 + React 18 + react-router-dom v7
SPA that round-trips the operator login flow end-to-end with
`services/api`:

- Two routes: `/login` (public) and `/` (gated dashboard).
- `<CapabilityProvider>` + `<CapabilityGate>` in `packages/ui` (the
  capability-edge UI primitive ADR-0047 §6 commits to).
- `useWebSocketChannel` + `useEventSource` hooks in `packages/ui`
  (skeletons; real wiring lands with the first session-broker /
  approval-prompt feature ticket).
- 401 chokepoint reloads to `/login`, with a guard against infinite
  loops when the 401 lands on `/login` itself.
- Tailwind v4 CSS-first token map in `apps/web/src/styles.css` (the
  packages/config typed-token reference stays as documentation; v4's
  idiomatic config lives in CSS).
- Vite dev proxy `/v1/* → http://localhost:8080` so dev mirrors the
  ADR-0047 §10 same-origin prod posture (no CORS in dev or prod).

## Priority

P0. Last bootstrap ticket; unblocks the cockpit feature tickets that
ride on `packages/ui` and `apps/web/src/routes/`.

## Scope decisions (settled in consult / during implementation)

- **Tailwind v4 CSS-first** in `apps/web/src/styles.css` via `@theme`.
  The packages/config/tailwind.config.ts file from T-007a remains as a
  typed-token reference for tooling and as the migration target if
  apps/admin lands a divergent palette. Documented in
  packages/config/tailwind.config.ts (the four token categories that
  ride on v4 defaults at T-007a).
- **`pnpm.overrides: { vite: ~6.0.7 }`** at the workspace root. `vitest`
  in shared-types/ui declares vite as a peer; `@tailwindcss/vite` +
  `@vitejs/plugin-react` accept ranges that include v5; without the
  override pnpm pulled vite@5.4 in addition to apps/web's @~6.0.7,
  and TypeScript reported "Plugin from vite@5 is not assignable to
  vite@6's Plugin." Override forces a single vite version everywhere.
- **`baseUrl: window.location.origin`** in `apps/web/src/api.ts`, not
  `baseUrl: "/"`. openapi-fetch builds requests via `new Request(url)`
  which node-undici (used by jsdom and SSR) rejects when the URL isn't
  absolute. Real browsers tolerate bare paths but jsdom doesn't — using
  `window.location.origin` keeps test and runtime aligned and remains
  same-origin in prod (Caddy/Traefik fronts both SPA and API per
  ADR-0047 §10).
- **`vi.mock("../api")` for the Login route test**, not
  `vi.stubGlobal("fetch", …)`. openapi-fetch captures its fetch
  reference inside the client instance at construction time, so
  stubbing globalThis.fetch after the module imports doesn't intercept.
  Canonical RTL pattern.
- **`afterEach(() => cleanup())`** in every RTL test file. With
  `globals: false` (preferred — keeps the vitest API explicit),
  @testing-library/react doesn't auto-register cleanup, and tests leak
  DOM into each other. One-line fix, applied to both test files.
- **`@testing-library/react@~15.0.7`** (not 16.x). RTL 16 requires
  React 19; we're on React 18.
- **`environmentOptions.jsdom.url: "http://localhost:5173"`** in
  `apps/web/vitest.config.ts` so jsdom's window.location matches the
  Vite dev server origin. Tests rehearse against the same host pattern
  the SPA uses in dev.
- **401 chokepoint via `window.location.assign("/login")`**, not
  react-router navigation. Page reload re-bootstraps the
  CapabilityProvider cleanly. The guard against reloading when already
  on `/login` prevents loops when a CSRF cookie has gone stale and the
  next login submission also 401s.
- **`<ApprovalPrompt>`, `<RemoteSessionView>`, `<TerminalView>`
  deferred** to their feature tickets (consistent with the T-007 consult
  + `search-before-create` rule in design-system-governance §3). Each
  has dedicated UX in `docs/ui-ux/` that hasn't been pinned down yet;
  shipping speculative components now would freeze decisions
  prematurely. packages/ui/src/index.ts documents which feature ticket
  lands each one.
- **No `@radix-ui/*` deps yet.** They land with the first vendored
  shadcn primitive (also a feature ticket). The minimal Login form
  uses raw Tailwind-styled inputs — adequate for T-007c's verification
  anchor; replaced by shadcn primitives when those land.
- **`@types/node`** added to apps/web devDeps so vite.config.ts can
  read `process.env.AIREOS_API_PROXY` (the dev-only proxy target
  override). Without it tsc fails on `process` being undefined.

## Files in scope

- **Create (apps/web):**
  - `apps/web/package.json` — Vite 6, React 18, react-router-dom v7,
    workspace deps on @aireos/{shared-types,ui,config}.
  - `apps/web/tsconfig.json` — extends @aireos/config/tsconfig.base.json.
  - `apps/web/vite.config.ts` — React plugin + Tailwind v4 plugin + dev
    proxy `/v1/* → http://localhost:8080`.
  - `apps/web/vitest.config.ts` — jsdom env + URL config.
  - `apps/web/index.html` — minimal HTML root.
  - `apps/web/src/main.tsx` — React 18 createRoot bootstrap.
  - `apps/web/src/styles.css` — Tailwind v4 entry + `@theme` token map.
  - `apps/web/src/api.ts` — module-level `createApiClient` with 401
    chokepoint + `fetchCurrentCapability` adapter.
  - `apps/web/src/App.tsx` — CapabilityProvider + BrowserRouter +
    RequireAuth route guard + /login + / + catch-all.
  - `apps/web/src/routes/Login.tsx` — form posting to /v1/auth/login,
    reload capability on success, navigate to /.
  - `apps/web/src/routes/Dashboard.tsx` — minimal post-login surface;
    Cockpit feature routes land later.
  - `apps/web/src/routes/Login.test.tsx` — happy / 401 / 500 cases
    via vi.mock("../api").
- **Create (packages/ui):**
  - `packages/ui/src/capability/CapabilityProvider.tsx` — context +
    fetch-on-mount + reload + clear + injectable `fetchCapability`.
  - `packages/ui/src/capability/CapabilityGate.tsx` — edge + optional
    subjects allow-list + fallback.
  - `packages/ui/src/capability/capability.test.tsx` — 8 tests covering
    gate edge/subject logic, provider mount/reload, useCapability guard.
  - `packages/ui/src/hooks/useWebSocketChannel.ts` — auto-reconnect +
    exponential backoff + status surface.
  - `packages/ui/src/hooks/useEventSource.ts` — named-event subscription
    + status surface.
  - `packages/ui/vitest.config.ts` — jsdom env (shared-types stays node).
- **Modify:**
  - `package.json` — `pnpm.overrides: { vite: ~6.0.7 }`.
  - `packages/ui/package.json` — react, @testing-library/react, jsdom,
    vite (peer alignment), vitest.
  - `packages/ui/src/index.ts` — re-export the new surface.
  - `packages/shared-types/package.json` — vite (peer alignment).
  - `.github/workflows/ci.yml` — add `pnpm --filter @aireos/web run
    build` step in the js job (verifies the SPA builds).
  - `pnpm-lock.yaml` — regenerated.
  - `docs/tickets/README.md` — mark T-007c complete.
- **Create:** `docs/tickets/T-007c-web-shell-capability-ui.md` (this file).

## Acceptance criteria

- [x] `pnpm install --frozen-lockfile` clean.
- [x] `task codegen` clean; drift check passes.
- [x] `pnpm -r run typecheck` clean across all 4 packages (config,
      shared-types, ui, web).
- [x] **Passing fixtures:** 16/16 tests pass — 5 shared-types (CSRF/401
      contract), 8 ui (CapabilityProvider/Gate + useCapability),
      3 apps/web (Login happy / 401 / 500).
- [x] `pnpm --filter @aireos/web run build` clean; produces hashed
      static assets under `apps/web/dist/` (~187 KB JS, ~62 KB gzipped,
      ~9 KB CSS).
- [x] **Live e2e** (curl from an alpine container against a live
      `services/api` on the docker-compose network):
      login (200 + 2 cookies, body has only `expiresAt`, no `token`) →
      /v1/auth/me via session cookie only (200 with subject + edge) →
      bad password (401) →
      POST /v1/resources with cookies but no X-CSRF-Token (403) →
      POST with cookies + matching X-CSRF-Token (201) →
      POST with cookies + WRONG X-CSRF-Token (403) →
      POST /v1/auth/logout (204 + Set-Cookie clears on both).
- [x] No `next`, `remix`, `@remix-run/*`, `@sveltejs/kit`, `astro`,
      `nuxt`, `solid-js`, `@tanstack/react-start` in any package.json
      (ADR-0047 §Verification §1).
- [x] `apps/web` builds to static assets only; no Node runtime ships
      in the production artifact (ADR-0047 §Verification §2).
- [x] OpenAPI / schemas — N/A (T-007c does not change the contract).
- [x] Docs / ADR — N/A (ADR-0001..0047 untouched).
- [x] Unit tests cover valid + invalid + edge — see above.
- [x] Verification command runs clean (see below).

## Out of scope

- **`<ApprovalPrompt>`, `<RemoteSessionView>`, `<TerminalView>`** —
  ADR-0047 §Verification §4 names them on packages/ui; they land with
  the feature tickets that consume them (approval, session-broker,
  terminal). packages/ui/src/index.ts documents the mapping.
- **Vendored shadcn primitives** — first vendored component lands with
  its consumer (no speculative copy-paste).
- **`apps/admin`** — deferred to a P1 ticket; the same stack will
  consume packages/{ui,shared-types,config}.
- **Playwright e2e harness** — the live curl-driven e2e above is the
  T-007 verification anchor; full Playwright tests land with the first
  remote-session ticket (when the cockpit's interactive surface earns
  the browser-driver overhead).
- **Login CSRF** — known limit documented in T-007b ticket; defended
  by SameSite=Strict but not by a pre-auth CSRF token at P0.

## Diff budget

~18 authored files + 2 generated (pnpm-lock.yaml regenerated; openapi.ts
unchanged this slice) ≈ 800 authored LoC. **Exceeds the default 12-file
/ 500-LoC cap.** Explicitly approved in the T-007 consult: any of the
three T-007 slices alone is shaped by the apps/web Vite + router +
capability + Tailwind landing.

## ADRs required

None new. Implements ADR-0047 §Verification §1, §3 (capability/gate
surface), §4 (the parts of packages/ui that ship at T-007c), §7 (401
chokepoint), §10 (same-origin via Vite proxy in dev, Caddy/Traefik in
prod).

## Verification

```
# Frozen install + drift + typecheck + tests + SPA build
rm -rf node_modules apps/*/node_modules packages/*/node_modules
pnpm install --frozen-lockfile                                         # clean
pnpm --filter @aireos/shared-types run codegen
git diff --exit-code packages/shared-types/src/generated/openapi.ts    # CLEAN
pnpm -r run typecheck                                                  # clean
pnpm -r --if-present run test                                          # 16/16 pass
pnpm --filter @aireos/web run build                                    # dist/ produced

# Live login→me→csrf→logout against services/api on docker-compose_default.
# services/api boots with AIREOS_PASETO_KEY + AIREOS_BOOTSTRAP_OPERATOR_*
# env vars (see commands in commit message); curl from an alpine container
# threads cookies manually (curl refuses to write Secure cookies on http,
# but the cookies are correctly set on the response).
```

All gates passed.
