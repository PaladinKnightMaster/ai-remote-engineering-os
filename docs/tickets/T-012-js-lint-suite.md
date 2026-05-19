---
name: T-012 JS-side lint suite closing ADR-0047 P0 verification gaps
about: P1 lint ticket enforcing openapi-fetch wrapper-only fetch calls and meta-framework dep prohibition in apps/*
title: "T-012: JS-side lint suite (ADR-0047 verification gaps)"
labels: ["ticket", "P1", "lint", "ci"]
---

## Ticket ID and link

T-012 — see `docs/tickets/README.md`. Closes P0 verification gaps documented in ADR-0047 §Verification.

## Goal

Land two JS-side enforcement mechanisms, wired into `task lint`, that fail CI on commits violating already-decided ADR-0047 policy: (a) direct `fetch` calls outside the `openapi-fetch` wrapper, (b) meta-framework dependencies in `apps/*/package.json`. The policies themselves are settled; this ticket closes their P1 enforcement deferral.

## Priority

**P1.** ADR-0047 explicitly defers both lints to P1 and names the gaps as review-enforced at P0. No new ADR required.

## Files in scope

- **Create:**
  - `tools/forbidden-deps.mjs` — small Node script reading every `apps/*/package.json` and failing if any of the ADR-0047-forbidden deps appear in `dependencies`, `devDependencies`, `peerDependencies`, or `optionalDependencies`: `next`, `@remix-run/*` (glob), `@sveltejs/kit`, `astro`, `nuxt`, `solid-js`, `@tanstack/react-start`. Exit code 0 on clean, 1 with named-offender output on violation.
  - `tools/forbidden-deps.test.mjs` — exercises the script against an in-repo `tools/forbidden-deps.fixtures/` directory containing a violating `apps/X/package.json` (must fail) and a clean one (must pass).
  - `packages/config/eslint.config.ts` — shared ESLint config (or extend if T-002/T-007 lands a stub) adding a `no-restricted-syntax` rule banning `CallExpression[callee.name='fetch']` and `NewExpression[callee.name='Request']`, with an `overrides` exception scoping the allowed location to `packages/shared-types/src/**`. Also retains the ADR-0047-mandated `react/no-danger` rule (or equivalent) covering `dangerouslySetInnerHTML`.
  - `packages/config/eslint.config.test.ts` — verifies the rule fires on a fixture file outside `packages/shared-types/src/` and stays silent on a fixture inside it.
- **Modify:**
  - `Taskfile.yml` — extend `task lint` to invoke `pnpm -r eslint .` (already implied by ADR-0042's `task lint` contract) and `node tools/forbidden-deps.mjs`. Ordering: pnpm install/check first, then ESLint, then the deps check.
  - `apps/web/eslint.config.ts` and (when present) `apps/admin/eslint.config.ts` — re-export from `@org/config/eslint` (or equivalent workspace import) so the shared rule applies. If T-007 has not yet introduced these files, that wiring is part of T-007's diff and this ticket only extends the shared `packages/config/eslint.config.ts`.
- **Test:** as listed under *Create*.

## Acceptance criteria

- [ ] `task lint` fails when any file under `apps/web/src/`, `apps/admin/src/`, or any other `apps/*` consumer makes a direct `fetch(...)` call or constructs `new Request(...)`. Verified by a fixture file outside `packages/shared-types/src/`.
- [ ] `task lint` passes when the same call exists inside `packages/shared-types/src/client.ts` (or anywhere under `packages/shared-types/src/`).
- [ ] `task lint` fails when any of `next`, `@remix-run/*`, `@sveltejs/kit`, `astro`, `nuxt`, `solid-js`, or `@tanstack/react-start` appears in any `apps/*/package.json` `dependencies`/`devDependencies`/`peerDependencies`/`optionalDependencies` block. Verified against `tools/forbidden-deps.fixtures/violating/`.
- [ ] `task lint` passes on the clean fixture and on the real `apps/web/package.json` once T-007 lands it.
- [ ] The forbidden-deps script also runs against `apps/admin/package.json` when that file exists (no special-casing; the script globs `apps/*/package.json`).
- [ ] `task lint` passes on `main` after this ticket lands (all targets clean or non-existent).
- [ ] OpenAPI / schemas updated if applicable — **N/A** (no contract changes).
- [ ] Docs / ADR updated if applicable — see note under T-011 *Acceptance criteria* re: ADR back-pointers; this ticket is the reverse pointer to ADR-0047 §Verification gaps.
- [ ] Unit tests cover valid + invalid + edge cases — see `*.test.mjs` and `eslint.config.test.ts`.
- [ ] Verification command runs clean (see *Verification* below).

## Out of scope

- The Go-side lints for ADR-0044/0045/0046 — covered by T-011.
- Pre-commit hook integration (pnpm `husky`-style); CI-only at P0.
- Migrating to a different ESLint flat-config setup beyond what ADR-0047 implies — flat config is assumed per ADR-0047 §Verification §5.
- Bundle-size or Vite-minor-bump security-review automation (ADR-0047 mentions this as standard doctrine; not a lint).
- `<ApprovalPrompt>` UX validation lint (ADR-0047 commits architecture only; UX is a separate UI-spec ticket).

## Diff budget

≤12 files / ≤500 LoC. One Node script + one test + one ESLint config extension + one ESLint config test + small Taskfile change is well within budget.

## ADRs required

None new. This ticket implements verification-gap closure for **ADR-0047** only.

## Verification

```sh
# From repo root, with the ticket's changes applied:
task lint                                  # passes on main

# Negative tests — each fixture must fail:
node tools/forbidden-deps.test.mjs
pnpm --filter @org/config test
```

Expected output: both test scripts assert violating fixtures produce non-zero exit and clean fixtures produce zero exit.

## Activation prerequisites

This ticket sits in the backlog until **both** of the following land:

1. T-002 (skeleton) — provides `Taskfile.yml` with a `task lint` target stub, `pnpm-workspace.yaml`, and the `packages/config/` workspace member.
2. T-007 (web dashboard shell) — first ticket that creates `apps/web/package.json`, `packages/shared-types/src/client.ts`, and consumes the shared ESLint config.

The lint MUST land in the same milestone as T-007 at the latest — not as a follow-up — so the rule is born already enforced against real code.

## Owner / cross-review

- **Owner:** Frontend Lead.
- **Cross-review (required):** Security Reviewer (`fetch`-bypass is an XSS-adjacent surface; CSRF-token-attaching wrapper is the chokepoint being defended), Tech Lead (CI surface coherence with T-011).
- **Cross-review (optional):** Release Guard.

## Related

- ADR-0047 — Frontend framework. *Verification* §Known gaps at P0:
  - "No CI lint enforces 'no direct `fetch` calls bypass the `openapi-fetch` wrapper.'" — closed here.
  - "No CI lint enforces 'no meta-framework deps in `apps/*/package.json`.'" — closed here.
- ADR-0042 — Monorepo tooling. `task lint` is the canonical orchestration entry point this ticket extends; pnpm workspaces are the substrate.
- ADR-0043 — API framework. The `openapi-fetch` wrapper consumes the same `docs/api-contracts/openapi.v3.fragment.yaml` that produces Go server stubs; the `fetch`-bypass lint protects that contract on the JS side.
- ADR-0046 — Auth adapter. The CSRF double-submit token is attached by the wrapper; bypassing the wrapper bypasses the CSRF defense, which is the operational reason the lint exists.
- T-011 — Go-side lint suite (ADR-0044/0045/0046). Sibling ticket on the Go stack.
