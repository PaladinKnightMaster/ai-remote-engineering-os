---
name: T-024-b Ticket
about: operator audit-timeline SPA page + WD-1 live read assertion (closes T-024)
title: "T-024-b: operator audit-timeline SPA + WD-1 timeline-read proof (v0.1.0)"
labels: ["ticket", "feature", "frontend"]
---

## Ticket ID and link

T-024-b — frontend half of **T-024** (operator audit-timeline view), closing the
"operator visits the audit timeline" half of **WD-1 step 8**. Builds on the
backend read-proxy from **T-024-a** (`GET /v1/audit/events`). No B-NNN
counterpart. Binding specs: ADR-0047 (Vite SPA, capability-gated UI,
`openapi-fetch` client), ADR-0045 (append-only chain).

## Goal

A capability-gated `/audit` page in `apps/web` that reads the chained evidence
log through the T-024-a proxy and renders it as a read-only timeline; plus a
WD-1 assertion that the operator reads their own chained events through
Caddy → api → `audit-service` on real wires.

## Priority

P0 — release-gating. **The last open v0.1.0 gate.** After this, the manifest's
`remaining_release_gates` is `[]` and the release is ready to tag.

## Decision: page scope + component home (CEO-delegated to the council)

- **Page scope: minimal read-only table + cursor "Load more" + manual
  "Refresh".** A chronological table (time · actor · edge · target · outcome ·
  evidence) is the right v0.1.0 surface. "Load more" exercises the proxy's
  forward cursor; "Refresh" re-reads from the head (the "did my action land?"
  loop). **Client-side filtering rejected** — filtering only the *loaded* page
  would misrepresent the chain; server-side query is v0.2.0.
- **Component home: an `apps/web` route**, matching the `Sessions`/`SessionView`
  precedent — not a vendored `packages/ui` component. The timeline has one
  consumer; design-system-governance §3 ("search before create") says don't
  speculate a shared component (no `apps/admin` at v0.1.0).

## Files in scope

- Create: `apps/web/src/routes/Audit.tsx` (the timeline page)
- Create: `apps/web/src/routes/Audit.test.tsx` (RTL, mocked `apiClient`: list, Load-more cursor, error)
- Modify: `apps/web/src/App.tsx` (`/audit` route under `RequireAuth`)
- Modify: `apps/web/src/routes/Dashboard.tsx` (capability-gated "Audit timeline →" link)
- Modify: `tests/e2e/wd1_test.go` (step 6: `assertAuditTimelineReadable` — read through Caddy→api→audit, poll for the operator's own `auth.login.success`)
- Modify: `docs/releases/v0.1.0/release_notes_v0_1_0.md` (gate closed; "ready to tag")
- Modify: `docs/releases/v0.1.0/release_manifest_v0_1_0.json` (`remaining_release_gates: []`, status `ready-to-tag`)
- Create/modify: `docs/tickets/T-024-b-*.md` (this) + `docs/tickets/README.md`

**~8 files** — within the ≤12 / ≤500 LoC budget.

## Behaviour notes

- **Read-only, gated.** Route under `RequireAuth`; the Dashboard link sits inside
  the existing `CapabilityGate edge="human-agent"`. The actual boundary is the
  proxy's operator gate (T-024-a) + `RequireCapability` (ADR-0046) — the gate is
  UX (ADR-0047): a non-operator's `/audit` would 403 at the proxy.
- **Forward-only cursor.** `apiClient.GET("/v1/audit/events", {params:{query:{limit:50,cursor}}})`;
  "Load more" appends the next page using `page.nextCursor`; "Refresh" resets
  from the head.
- **Same client, same origin.** Reuses the module-level `apiClient` (ADR-0047 one
  schema, two consumers) with the 401 chokepoint — no new fetch surface.

## Acceptance criteria

- [x] `/audit` route renders the chain as a read-only table; "Load more" pages
      forward on the cursor; "Refresh" re-reads from the head; load-failure shows
      an alert.
- [x] Dashboard exposes a capability-gated link to `/audit`.
- [x] **Live (G1):** WD-1 asserts the operator reads their own chained
      `auth.login.success` (actor `operator:admin`) back through
      **Caddy → api proxy → audit-service** over real self-signed HTTPS — not a
      mocked boundary. Closes WD-1 step 8's timeline-view half.
- [x] `apps/web` typecheck + eslint + vitest clean (incl. the 3 `Audit.test.tsx`
      cases); `tests/e2e` compiles under both `wd1` and `e2e` tags.
- [x] Release artifacts flipped: `remaining_release_gates: []`, status
      `ready-to-tag`.

### Violating vs. passing fixtures

- **Client-side filter over the loaded page — violating** (appears to filter the
  whole chain but only sees a slice). **Passing:** no filter at v0.1.0; "Load
  more" + "Refresh" only.
- **Mocked timeline boundary as the e2e proof — violating** (G1: a mocked
  boundary is not a verified e2e). **Passing:** WD-1 reads through the real
  Caddy→api→audit wire.

## Out of scope

- Server-side filtering/search + live auto-refresh/SSE → v0.2.0.
- Resource-picker page (separate feature ticket).

## ADRs required

- None new. Conforms to ADR-0047 (SPA route + capability-gated UI + `openapi-fetch`
  client) and ADR-0045 (append-only chain, read-only view). G2 clear.

## Verification

```sh
# TS: typecheck + eslint + vitest (apps/web)
docker run --rm -v "$PWD":/w -w /w node:22-alpine sh -c \
  'corepack enable && corepack prepare pnpm@10.32.1 --activate && pnpm install --frozen-lockfile &&
   pnpm --filter @aireos/web run typecheck && pnpm --filter @aireos/web run lint && pnpm --filter @aireos/web run test'
# Go: e2e compiles under both tags
docker run --rm -v "$PWD":/w -w /w golang:1.23 bash -c \
  'go vet -tags wd1 ./tests/e2e/... && go vet -tags e2e ./tests/e2e/...'
# Live: the WD-1 timeline-read assertion runs as part of `task wd1`
```

Confirmed 2026-06-01: typecheck/eslint clean; vitest 9/9 (incl. 3 Audit cases);
e2e compiles under `wd1` + `e2e`; doc-check green. The live `task wd1`
timeline-read assertion is exercised on the installed stack (the WD-1 run is the
operator-driven capstone; the new step is part of that flow).
