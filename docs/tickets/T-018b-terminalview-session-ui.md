---
name: T-018b Ticket
about: SSH end-to-end vertical slice (c) — <TerminalView> + session UI
title: "T-018b: <TerminalView> (xterm.js) + session list/start/end UI"
labels: ["ticket"]
---

## Ticket ID and link

T-018b — second half of the SSH end-to-end vertical slice (c) per ADR-0048
§Hand-off. No B-NNN counterpart. Depends on **T-018a** (cookie WS auth + the
typed `/v1/sessions` client). Completes the operator-usable cockpit: create a
session and drive a live, recorded SSH terminal from the browser.

## Goal

Land the `<TerminalView>` xterm.js component (the last of ADR-0047's three
deferred `packages/ui` components for this slice) wired to the session-broker
WebSocket, plus the `apps/web` session list/start/end routes that drive the
audited control-plane → data-plane path.

## Priority

P0 — v0.1.0 wedge (Remote Cockpit pillar, ADR-0047/0048). No new ADR.

## Files in scope

- Modify: `packages/ui/src/hooks/useWebSocketChannel.ts` (`binaryType` option)
- Create: `packages/ui/src/components/TerminalView.tsx`
- Create: `packages/ui/src/components/TerminalView.test.tsx`
- Modify: `packages/ui/src/index.ts` (export `TerminalView`)
- Modify: `packages/ui/package.json` (`@xterm/xterm`, `@xterm/addon-fit`)
- Create: `apps/web/src/routes/Sessions.tsx` (list + start + end)
- Create: `apps/web/src/routes/SessionView.tsx` (terminal + end)
- Create: `apps/web/src/routes/Sessions.test.tsx`
- Modify: `apps/web/src/App.tsx` (guarded `/sessions`, `/sessions/:id` routes)
- Modify: `apps/web/src/routes/Dashboard.tsx` (Sessions link)
- Modify: `apps/web/vite.config.ts` (broker WS dev proxy)
- Modify: `pnpm-lock.yaml` (xterm deps)  *(mechanical)*

12 files; 1 mechanical (lockfile). Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (design-system preflight + consult, 2026-05-30)

- **Preflight: reuse, don't reinvent.** Existing semantic tokens
  (`bg-background`/`text-foreground`/`border-border`/`primary`/`destructive`)
  are used as-is (no new tokens; Style-Binding rule, T-012 lint). a11y mirrors
  Login/Dashboard (`role`, `aria-label`, focusable terminal `role="group"`).
- **`<TerminalView>` lives in `packages/ui`** (ADR-0047 boundary; the barrel
  reserved it for "the first terminal UI ticket"). It wires xterm ↔
  `useWebSocketChannel` on the **T-017b wire protocol**: inbound binary → 
  `term.write`; `term.onData` → binary `send`; `FitAddon` → text control
  `{"type":"resize",cols,rows}`. The hook gained a `binaryType:"arraybuffer"`
  option so PTY bytes arrive synchronously and in order (a `blob` default forces
  an async `.arrayBuffer()` that can reorder frames).
- **xterm.js is ADR-0047's named pick**, so adding `@xterm/xterm` +
  `@xterm/addon-fit` implements an approved decision — **no new ADR**. New pnpm
  deps must enter the offline content store (§OI, devops).
- **Audited path only.** The UI starts a session via `POST /v1/sessions` (api)
  then connects the broker WS from `<SessionView>` — it never dials the broker
  without the api-minted session, so every session is in the audit chain
  (session.open/close, T-017b).
- **Same-origin WS.** `<SessionView>` builds `ws(s)://<origin>/v1/sessions/<id>/
  channel`; the Vite dev proxy adds a regex rule (`^/v1/sessions/[^/]+/channel$`
  → broker :8082, `ws:true`) **ahead of** the general `/v1` → api :8080 rule.
  Prod same-origin is the gateway's job (OQ-3).
- **Generated `SessionCreate` requires `mode`.** openapi-typescript emitted the
  single-value enum field as required, so the SPA sends `mode:"terminal"`
  explicitly (the only v0.1.0 value; the server also defaults it).
- **Test realm note.** xterm needs a canvas jsdom lacks, and the WS hook needs a
  WebSocket jsdom lacks, so `TerminalView.test` mocks both and asserts the wire
  protocol. jsdom's cross-realm `Uint8Array` makes `toBeInstanceOf` unreliable;
  the test uses `ArrayBuffer.isView` + byte contents instead.

## Acceptance criteria

- [x] `<TerminalView>` connects with `binaryType:"arraybuffer"`, writes inbound
      PTY bytes to xterm, sends keystrokes as binary frames, and sends a
      `{type:"resize",cols,rows}` text frame on open + viewport resize (tests).
- [x] `useWebSocketChannel` honors a `binaryType` option.
- [x] `Sessions` lists `GET /v1/sessions`, starts via `POST /v1/sessions`
      (→ navigates to the terminal), and ends via `POST /v1/sessions/{id}/end`
      (tests cover list/start/error).
- [x] `<SessionView>` mounts the terminal against the broker channel URL and
      offers End.
- [x] Routes are capability-guarded (`RequireAuth`); Dashboard links to Sessions.
- [x] Vite dev proxy routes the broker channel to :8082 (`ws:true`) before the
      general `/v1` rule.
- [x] `pnpm -r typecheck`, `pnpm -r test`, `pnpm -r lint` all clean.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Frame reorder — violating:** default `blob` binaryType forces async
  `.arrayBuffer()`; concurrent frames can render out of order. **Passing:**
  `arraybuffer` → synchronous, ordered `term.write`.
- **Wrong frame kind — violating:** sending keystrokes as text or resize as
  binary breaks the broker's `MessageText`/`MessageBinary` split. **Passing:**
  keystrokes = binary, resize = text JSON (tests assert both).
- **Proxy precedence — violating:** the general `/v1` rule catches the channel
  and proxies the WS to api (no WS handler → fails). **Passing:** the regex
  channel rule is listed first and wins.
- **Unaudited dial — violating:** UI opens the broker WS without an api session.
  **Passing:** create via REST first, then connect — every session is audited.

## Out of scope

- Cast-file persistence layout / retention / chain linkage → **T-019**.
- `<RemoteSessionView>` (RDP/VNC Guacamole) + `<ApprovalPrompt>` → their tickets.
- A resource picker (the start form takes a resource id directly at v0.1.0).
- Prod reverse-proxy config (Caddy/Traefik same-origin) → release/devops (OQ-3).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 12 files (1 mechanical), within budget.

## ADRs required

- None new. Implements ADR-0047 (`<TerminalView>`/xterm.js, WS hook) + ADR-0048
  data-plane wire protocol.

## Verification

```sh
# pnpm install (adds xterm), typecheck, tests, lint (node:22-alpine, pnpm 10.32.1)
docker run --rm -e CI=true -v "$PWD:/work" -v aireos-pnpm:/pnpm-store -w /work node:22-alpine sh -c '
  corepack enable && corepack prepare pnpm@10.32.1 --activate &&
  pnpm install --no-frozen-lockfile --store-dir /pnpm-store &&
  pnpm -r run typecheck && pnpm -r run test && pnpm -r run lint'
```

Expected: install adds `@xterm/xterm` + `@xterm/addon-fit`; typecheck clean;
tests pass (`packages/ui` incl. TerminalView 4, `apps/web` incl. Sessions 3);
eslint clean. (Confirmed 2026-05-30 on Docker / node:22-alpine / pnpm 10.32.1.)
