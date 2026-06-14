---
name: T-034 Ticket
about: v0.3.0 verification infra — Playwright browser smoke suite + DoD addendum
title: "T-034: browser smoke suite (headless §12 walkthrough) + browser-evidence DoD addendum"
labels: ["ticket", "verification", "web", "ci"]
---

## Ticket ID and link

T-034 — the **Playwright browser smoke suite** + a one-line **DoD addendum**. The
first v0.3.0 ticket and the bridge from v0.2.0: it converts the §12 human WD-2
gate into a machine gate, and would have caught the latent xterm.css defect the
human run found at release (`<TerminalView>` never importing `@xterm/xterm/css/xterm.css`).
Verification infrastructure — **no new ADR** (same class as `task release-check` /
the e2e aggregator).

## Goal

A real-browser smoke that drives the rendered cockpit through the governed-egress
walkthrough — login → start a `requires_approval`-flagged session → `<ApprovalPrompt>`
→ **deny** (blocked) → **approve** → **assert the RENDERED xterm terminal** (type a
command, read its output back from the painted rows) → end → assert the audit
timeline renders the governed outcomes. Plus a rulebook line requiring
browser-rendered evidence for `packages/ui` / `apps/web` rendering changes.

## Priority

P1 — v0.3.0 opener; the machine gate that hardens every future cockpit/UI change
before the MCP Gateway work begins.

## Files in scope (10)

- Create: `tests/smoke/package.json` — standalone npm island (OUTSIDE the pnpm
  workspace), pins `@playwright/test@1.49.1`.
- Create: `tests/smoke/playwright.config.ts` — one chromium project, `ignoreHTTPSErrors`.
- Create: `tests/smoke/governed-egress.smoke.spec.ts` — the walkthrough.
- Create: `tests/smoke/.gitignore` — node_modules / reports / results.
- Create: `infra/docker-compose/smoke/Caddyfile` — same-origin HTTPS gateway.
- Create: `infra/docker-compose/docker-compose.smoke.yml` — `include`s the e2e
  backend + adds webbuild (build SPA) + web (Caddy) + smoke (Playwright runner).
- Modify: `Taskfile.yml` — new `task smoke`; appended as a `release-check` leg.
- Modify: `CLAUDE.md` — the DoD addendum line.
- Create: `docs/tickets/T-034-browser-smoke-dod-addendum.md` (this) + README status.

## Design (from the consult, CEO-approved)

- **Target = a new browser-smoke compose profile (option B).** Reuse the live e2e
  backend verbatim via `include`; layer on a one-shot SPA build → Caddy serving the
  **built** bundle same-origin over HTTPS → a Playwright runner. Compiles from
  source every run, so it sidesteps the stale-`dist/` trap (the T-033 lesson). The
  installed/systemd posture stays where it belongs — the human WD-2 + `task wd1`.
- **HTTPS is mandatory**, not cosmetic: api sets its session + CSRF cookies `Secure`
  (hardcoded, ADR-0047 §10), so plain HTTP would drop them. Caddy uses `tls internal`;
  Playwright trusts the local CA via `ignoreHTTPSErrors`.
- **The xterm.css regression guard** is the load-bearing assertion: a DOM-text check
  alone would PASS even with the bug (the rows still carry text, they just paint as
  unreadable overlapping layers). So the spec also asserts a stylesheet whose rules
  target `.xterm` is present in the rendered document — that fails iff the css import
  is dropped. A screenshot is attached as browser-rendered evidence (the DoD line).
- **Division of labour:** the Go e2e (`tests/e2e/approval_test.go`) remains the
  authoritative LIVE AUDIT CHAIN proof (cast-SHA anchored). This suite owns only
  what a real browser proves — that the shipped bundle RENDERS. The audit-timeline
  assertion is a UI smoke (the table renders the governed outcomes), not a re-verify.

## Dependency diligence — Playwright (heavyweight devDep)

- **Never ships.** `tests/smoke` is a standalone npm island OUTSIDE the pnpm
  workspace, so `@playwright/test` cannot enter apps/web's build graph, the shipped
  service closure, or the offline bundle (which is only apps/web's build output +
  service binaries). Verified by grepping the built bundle (see Verification).
- **License:** Apache-2.0 — already in the allowlist; but it's a devDep, so the
  `licenses` gate (production deps only) never sees it. No license-policy change.
- **Pin:** `@playwright/test@1.49.1` MUST match the runner image tag
  `mcr.microsoft.com/playwright:v1.49.1-noble` (browser revisions are version-locked).
  Bump both together.
- **Online-only:** the gate pulls the Playwright image, so like the e2e +
  release-check Docker legs it runs on CI / a full dev env, NOT the air-gapped box.

## release-check

`task smoke` joins `task release-check` as its own leg appended **after** `e2e`,
carrying the "Docker + browser image, CI/full-env only" caveat — the offline dev
box still runs every other leg per-leg.

## G1 / G2

- **G1 (live cross-boundary test):** the smoke spec itself — a real browser →
  Caddy → api → policy → connector → broker → sshd → audit, asserting the
  **rendered** terminal output + the UI control flow. No mocked boundary. The
  machine version of the §12 human gate.
- **G2:** no ADR divergence. Verification infrastructure; touches no Accepted ADR
  and no criteria doc. ADR-0056 stays reserved for the v0.3.0 gateway scoping consult.

## Verification (Dockerized)

- `task smoke` green: deny leg blocked, approve leg renders, `SMOKE-42-OK` read
  from the painted `.xterm-rows`, the xterm.css stylesheet guard passes, audit
  timeline shows `approval.granted` / `approval.denied:operator` / `credential.leased`
  / `closed:operator`.
- Regression: `apps/web` + `packages/ui` vitest still green (`pnpm --filter
  @aireos/{ui,web} test`); `pnpm --filter @aireos/web build` clean.
- Isolation proof: grep the built `dist/` (and the apps/web pnpm closure) for
  `playwright` → zero hits (it never ships).

## Definition of done

`task smoke` green and wired into `release-check`; the DoD addendum line in
CLAUDE.md (CEO-signed); ticket + README updated. Commit on `dev`, ff `main`. No
push. After this: the **v0.3.0 scoping consult** (`docs/evaluation/v0.3.0-release-criteria.md`
+ the MCP Gateway ADRs, ADR-0056+).
