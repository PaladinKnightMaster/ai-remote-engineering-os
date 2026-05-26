---
name: T-012 JS-side lint suite closing ADR-0047 P0 verification gaps
about: Lint suite enforcing openapi-fetch wrapper-only fetch calls, no-localStorage-for-tokens, no-dangerouslySetInnerHTML, and meta-framework dep prohibition in apps/*
title: "T-012: JS-side lint suite (ADR-0047 verification gaps + v0.1.0 SEC-7)"
labels: ["ticket", "P1", "lint", "ci", "release-blocker"]
---

## Ticket ID and link

T-012 — see `docs/tickets/README.md`. Closes the P0 verification gaps in
ADR-0047 §Verification AND the **SEC-7 release-blocker** in
`docs/evaluation/v0.1.0-release-criteria.md`. Lead lens: **Frontend Lead**;
cross-review: **Permission Model Reviewer** (`fetch`-bypass is the CSRF
chokepoint; localStorage-token is the XSS escape hatch), **Tech Lead**
(CI surface coherence with T-011).

## Goal

Land four JS-side enforcement mechanisms, wired into `task lint` and the
CI `js` job, that fail loud on commits violating already-decided policy:

1. Direct `fetch()` / `new Request()` outside the `createApiClient` wrapper.
2. `localStorage` / `sessionStorage` usage in `apps/` and `packages/ui/`.
3. `dangerouslySetInnerHTML` anywhere in JS sources.
4. Any meta-framework dep (`next`, `@remix-run/*`, `@sveltejs/kit`,
   `astro`, `nuxt`, `solid-js`, `@tanstack/react-start`) in `apps/*/package.json`.

The first three are ESLint rules; the fourth is a small Node script
checking `apps/*/package.json` directly.

## Priority

**P1 → release-blocker.** Filed at P1 before
`docs/evaluation/v0.1.0-release-criteria.md` was Accepted. That document
makes rules 1-3 **SEC-7 BLOCKERS** for v0.1.0; rule 4 closes ADR-0047
§Verification §1's review-enforced gap. The original P1 label is kept
for ladder continuity; the effective binding is the release criteria.

## Scope decisions (settled in consult / during implementation)

- **`@aireos/config`, not `@org/config`.** The pre-existing ticket
  draft used a placeholder namespace; the workspace name is `@aireos/*`
  per T-007a.
- **`packages/config/eslint.config.js`, not `.ts`.** ESLint flat config
  TS loading requires the `jiti` transpiler hook. Plain JS with JSDoc
  types keeps the supply chain smaller (ADR-0042 "smallest viable
  tooling kit"). The shared config is ~150 lines; the JS-vs-TS overhead
  isn't material.
- **`packages/config/eslint-rules.test.ts`, not `eslint.config.test.ts`.**
  Vitest's default `exclude` pattern filters `**/eslint.config.*` — files
  literally named that get silently skipped. Renamed to avoid the trap.
- **Fixtures under `packages/config/__fixtures__/`** (rule-trigger
  fixtures) and `tools/forbidden-deps.fixtures/` (meta-framework dep
  fixtures). Both directories are in the shared config's universal
  `ignores` list so the *workspace* lint run does NOT lint these
  intentionally-broken files. The programmatic test in
  `eslint-rules.test.ts` strips those ignores from a copy of the config
  and runs ESLint against the fixtures.
- **Three rule families in `no-restricted-syntax` + `no-restricted-globals`
  + `react/no-danger`.** The CSRF/401 chokepoint contract enforces by:
  - `CallExpression[callee.name='fetch']` → forbidden globally,
    exception scoped to `packages/shared-types/src/`.
  - `NewExpression[callee.name='Request']` → same.
  - `localStorage` / `sessionStorage` as globals → forbidden in
    apps/* and packages/ui/* (exempted in Node-context files via a
    scoped override block).
  - `react/no-danger` + `react/no-danger-with-children` → globally
    on.
- **Scoped exceptions live in the shared config**, not in per-package
  overrides. Reason: review discipline. A contributor in `apps/web` who
  needs an exception adds an `eslint-disable` comment citing the
  doctrine they're loosening — visible in code review. If exceptions
  lived in per-package config files, drift would be invisible.
- **Test files get relaxed rules** (no-explicit-any, no-unused-vars,
  console) via a scoped override matching `**/*.test.{ts,tsx}`.
- **Node-context files (`vite.config.ts`, `vitest.config.ts`, `tools/`)**
  get `no-restricted-globals` turned off so they can read `process.env`,
  use `console`, etc. Browser-only rules don't apply.
- **`forbidden-deps.mjs` is a plain Node script**, no external deps —
  uses `node:fs` only. Glob via `readdirSync` + `existsSync`. Test via
  `node:test` (no extra devDep).
- **`@aireos/config` becomes a real workspace dep** of every linting
  package. Previously it was config-files-only; now packages that lint
  declare `@aireos/config: workspace:*` to establish the dependency
  graph that `pnpm install` resolves.

## Files in scope

- **Create:**
  - `packages/config/eslint.config.js` — shared flat config (the load-
    bearing file).
  - `packages/config/prettier.config.cjs` — shared Prettier config
    (deferred from T-007a; lands here per ADR-0047 §Verification §5).
  - `packages/config/__fixtures__/bad-fetch.tsx` — fixture triggering
    no-restricted-syntax on `fetch`/`Request`.
  - `packages/config/__fixtures__/bad-storage.tsx` — fixture triggering
    no-restricted-globals on `localStorage`/`sessionStorage`.
  - `packages/config/__fixtures__/bad-danger.tsx` — fixture triggering
    react/no-danger.
  - `packages/config/__fixtures__/good-app.tsx` — clean fixture (no
    errors).
  - `packages/config/eslint-rules.test.ts` — programmatic vitest
    asserting the rules fire / don't fire on the four fixtures above.
  - `tools/forbidden-deps.mjs` — meta-framework dep checker.
  - `tools/forbidden-deps.test.mjs` — node:test self-tests
    (clean / violating / missing-apps cases).
  - `tools/forbidden-deps.fixtures/clean/apps/good-app/package.json` —
    clean fixture.
  - `tools/forbidden-deps.fixtures/violating/apps/bad-app/package.json`
    — fixture with `next` + `@remix-run/react` + `astro`.
  - `tools/forbidden-deps.fixtures/empty/.gitkeep` — empty-base-dir
    fixture.
  - `apps/web/eslint.config.js` — re-exports the shared config.
  - `packages/ui/eslint.config.js` — re-exports.
  - `packages/shared-types/eslint.config.js` — re-exports.
  - `docs/tickets/T-012-js-lint-suite.md` — this ticket (replaces the
    pre-existing draft).
- **Modify:**
  - `Taskfile.yml` — `task lint` now runs `pnpm -r --if-present run lint`
    + `node tools/forbidden-deps.mjs`; the Go-side banner stays until
    T-011 lands.
  - `.github/workflows/ci.yml` — `js` job extended with lint step,
    forbidden-deps step, and forbidden-deps self-tests step.
  - `packages/config/package.json` — adds `@eslint/js`, `eslint`,
    `eslint-plugin-react`, `eslint-plugin-react-hooks`, `globals`,
    `typescript-eslint`; adds `test` script.
  - `packages/shared-types/package.json` — adds `@aireos/config` +
    `eslint` devDeps; adds `lint` script.
  - `packages/ui/package.json` — same.
  - `apps/web/package.json` — same.
  - `docs/tickets/README.md` — T-012 marked complete.

## Acceptance criteria

- [x] `task lint` fails when any file under `apps/web/src/`,
      `apps/admin/src/`, or any other apps/* consumer makes a direct
      `fetch(...)` call or constructs `new Request(...)` outside
      `packages/shared-types/src/`. **Verified by
      `eslint-rules.test.ts` against `bad-fetch.tsx`.**
- [x] `task lint` passes when the same call exists inside
      `packages/shared-types/src/client.ts` — verified by the existing
      wrapper not triggering the rule.
- [x] `task lint` fails when `localStorage` / `sessionStorage` is used
      in apps/* or packages/ui/* — verified by `bad-storage.tsx`.
- [x] `task lint` fails on `dangerouslySetInnerHTML` — verified by
      `bad-danger.tsx` triggering `react/no-danger`.
- [x] `task lint` fails when any of `next`, `@remix-run/*`,
      `@sveltejs/kit`, `astro`, `nuxt`, `solid-js`, or
      `@tanstack/react-start` appears in any `apps/*/package.json`
      `dependencies` / `devDependencies` / `peerDependencies` /
      `optionalDependencies` block — verified by
      `forbidden-deps.test.mjs` against
      `tools/forbidden-deps.fixtures/violating/`.
- [x] `task lint` passes on the clean fixture and on the real
      `apps/web/package.json`.
- [x] **Passing fixture:** the entire workspace lint runs clean on the
      committed code (T-007a/b/c shipped already-conforming code; T-012
      proves the architecture by linting it without modification).
- [x] **Violating fixtures:** 4 ESLint fixtures + 2 forbidden-deps
      fixtures, each producing the expected rule-id output.
- [x] OpenAPI / schemas — N/A (no contract changes).
- [x] Docs / ADR — N/A (ADR-0001..0047 untouched; criteria already
      Accepted at f2d82b9).
- [x] Unit tests cover valid + invalid + edge — see above.
- [x] Verification command runs clean (see below).

## Out of scope

- **Go-side lint suite (T-011)** — sibling ticket; SEC-8 release-blocker.
  Lands next.
- **Pre-commit hooks** (Husky / lefthook) — CI-only at P0.
- **License audit automation (`task licenses`)** — a §11a release-prep
  ticket per the criteria.
- **`task doc-check`** — same.
- **Bundle-size enforcement** — FE-2 SLO is review-enforced at v0.1.0;
  automation is a follow-up.
- **Custom ESLint rule for "every interactive control wrapped by
  CapabilityGate"** — FE-3 is review-enforced; no automation plan
  (acknowledged in criteria).

## Diff budget

17 authored files + lockfile regen. **Exceeds the default 12-file cap;
explicitly approved** in the A→B→C→T-011→C strategy sequence accepted
at f2d82b9. Most files are tiny (fixtures, re-export configs, the
single Taskfile/CI tweaks). Total authored LoC ≈ 600 including the
shared-config rules + test + script.

## ADRs required

None new. Closes ADR-0047 §Verification §1 review-enforced gaps and
v0.1.0 release-criteria SEC-7 (release-blocker).

## Verification

Verified in `node:22-alpine` on `--network docker-compose_default`:

```
# Frozen-lockfile install (post lockfile regen for new deps).
rm -rf node_modules apps/*/node_modules packages/*/node_modules
pnpm install --frozen-lockfile                                          # 18s

# Workspace gates.
pnpm -r run typecheck                                                   # clean
pnpm -r --if-present run test                                           # 16/16 pass
pnpm -r --if-present run lint                                           # clean across all packages
pnpm --filter @aireos/web run build                                     # 187 KB JS / 62 KB gz

# Lint-rule self-tests (the proof that SEC-7 is enforced).
pnpm --filter @aireos/config run test                                   # 4/4 pass

# Forbidden-deps gates.
node tools/forbidden-deps.mjs                                           # 1 apps/* checked; clean
node --test tools/forbidden-deps.test.mjs                               # 3/3 pass
```

All green.

## Owner / cross-review

- **Owner:** Frontend Lead.
- **Cross-review (required):** Permission Model Reviewer (SEC-7
  enforcement is the contract this ticket lands), Tech Lead (CI
  coherence with T-011).
- **Cross-review (optional):** Release Guard.

## Related

- ADR-0042 — Monorepo tooling. `task lint` is the canonical entry point
  this ticket extends; pnpm workspaces are the substrate.
- ADR-0043 — API framework. The `openapi-fetch` wrapper consumes the
  same `docs/api-contracts/openapi.v3.fragment.yaml` that produces Go
  server stubs; the `fetch`-bypass lint protects that contract.
- ADR-0046 — Auth adapter. The CSRF double-submit token is attached by
  the wrapper; bypassing the wrapper bypasses CSRF.
- ADR-0047 — Frontend framework. §Verification §Known gaps at P0
  named both this lint set and the meta-framework-dep check as
  review-enforced; this ticket promotes them to CI-enforced.
- `docs/evaluation/v0.1.0-release-criteria.md` — SEC-7 (this ticket)
  and SEC-8 (T-011, sibling).
- T-011 — Go-side lint suite (sibling release-blocker, SEC-8).
