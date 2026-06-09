---
name: T-031c Ticket
about: Governed Egress Foundation — <ApprovalPrompt> + apps/web pending-session round-trip
title: "T-031c: <ApprovalPrompt> + the apps/web approval round-trip (closes §FE-5)"
labels: ["ticket", "frontend", "policy"]
---

## Ticket ID and link

T-031c — the UI for the approval primitive: the `<ApprovalPrompt>` `packages/ui`
component + the `apps/web` pending-session round-trip. The **last piece of T-031**
(after the policy backend T-031a and the api seam T-031b). Closes the v0.1.0
**§FE-5** deferral (the last reserved `packages/ui` component besides
`<RemoteSessionView>`, which stays deferred with guacd). Conforms ADR-0047 §FE-5 +
ADR-0054 decision 2. **Frontend-only** — no backend/contract/codegen change.

## Goal

> SessionView GETs the session → `pending_approval` renders `<ApprovalPrompt>` →
> operator approves → `POST /v1/sessions/{id}/resolve {decision:"approve"}` →
> refetch (now `pending`) → `<TerminalView>` mounts; deny → refetch (`closed`) →
> "not approved". The approval is enforced server-side; this UI only drives the
> audited control plane.

## Priority

P1 — v0.2.0 spine; completes the approval primitive end to end (UI side).

## Files in scope (7)

- Create: `packages/ui/src/components/ApprovalPrompt.tsx` + `ApprovalPrompt.test.tsx`
- Modify: `packages/ui/src/index.ts` (export `ApprovalPrompt` + `ApprovalPromptProps`; drop the deferred note)
- Modify: `apps/web/src/routes/SessionView.tsx` (GET + status branch + resolve)
- Create: `apps/web/src/routes/SessionView.test.tsx`
- Create: `docs/tickets/T-031c-…md` (this) + `docs/tickets/README.md` status

## Design-system preflight (UI work rule)

- **Placement:** `<ApprovalPrompt>` is the ADR-0047 §FE-5-reserved `packages/ui`
  component (the barrel already names it) — the designated **reusable** approval
  primitive (v0.3.0 MCP tool-call / mobile approval consume it). Not the Audit.tsx
  one-consumer-app-route case.
- **Tokens:** semantic Tailwind only (`primary`/`destructive`/`muted-foreground`/
  `border`/`background`), matching `TerminalView.tsx` + `Sessions.tsx`. No hardcoded colors.
- **States:** idle / busy (actions disabled, `aria-busy`) / error (`role="alert"`).
- **A11y (§FE-7 baseline):** `role="region"` + label, heading, accessible Approve
  (primary) / Deny (destructive) buttons, alerted error.
- **Boundary:** presentational only — `apps/web` owns the API call + routing
  (same as `<TerminalView>`).

## Design decisions

- **SessionView branches on status** (GET on mount → ApprovalPrompt vs Terminal vs
  closed). Keeps the create→navigate path unchanged; the Sessions list needs no
  change (its link already routes here). Rejected inline-in-list (busier, more churn).
- **Refetch after resolve** reconciles both outcomes: approve → `pending` → terminal;
  deny / expired (409 closes the session) → `closed` → "not approved".

## G1 / G2

- **G1** — mocked (apiClient + `@aireos/ui` stubbed in the route test). The live
  login→create→approve→terminal proof is **T-033/WD-2**.
- **G2** — conforms ADR-0047 §FE-5 + ADR-0054; no divergence; no ADR edited.

## Verification (Dockerized node:22-alpine)

`corepack enable`; `pnpm install --frozen-lockfile` (no new deps); `pnpm -r typecheck`;
`pnpm --filter @aireos/ui test`; `pnpm --filter @aireos/web test`;
`pnpm --filter @aireos/web build`; `pnpm -r lint` — all green. No Go/Postgres/codegen;
the generated `openapi.ts` is untouched.

## Definition of done

`<ApprovalPrompt>` built + exported + tested; SessionView round-trip + tests; all
JS gates green; ≤12 files. Commit on `dev`, ff `main`. No push. **Completes T-031**;
next is T-032 (reconnect + reaper).
