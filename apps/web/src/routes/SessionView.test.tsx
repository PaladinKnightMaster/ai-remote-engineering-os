// Tests for the SessionView route — the approval round-trip (T-031c). Mocks the
// module-level apiClient (same pattern as Sessions.test.tsx) and stubs @aireos/ui
// so the test asserts SessionView's branching + API wiring, not xterm/WS or the
// real <ApprovalPrompt> (covered by its own test).
//
// T-117: SessionView now also imports <Button> from @aireos/ui (its Back/End
// session/"start a new one" actions). The mock below previously replaced the
// WHOLE module, which silently dropped Button along with everything else this
// route doesn't use — importOriginal + spread keeps every real export (Button
// included) and only overrides TerminalView/ApprovalPrompt.
//
// VER-14 (T-132): the vi.mock block below replaces TerminalView/ApprovalPrompt
// wholesale, so nothing in THIS suite ever renders the real components or
// touches xterm.js/WS. Two risks follow from that, and the "@aireos/ui mock ↔
// real module contract" describe block at the bottom of this file covers each
// differently — see its own comment for what it does and does not catch.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import type { ApprovalPromptProps, TerminalViewProps } from "@aireos/ui";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

vi.mock("@aireos/ui", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@aireos/ui")>();
  return {
    ...actual,
    TerminalView: () => <div data-testid="terminal" />,
    ApprovalPrompt: (props: { onApprove: () => void; onDeny: () => void }) => (
      <div data-testid="approval-prompt">
        <button onClick={props.onApprove}>Approve</button>
        <button onClick={props.onDeny}>Deny</button>
      </div>
    ),
  };
});

import { SessionView } from "./SessionView";
import { apiClient } from "../api";

function sess(status: string, extra: Record<string, unknown> = {}) {
  return {
    id: "s1",
    resourceId: "r1",
    operatorId: "o",
    mode: "terminal",
    status,
    startedAt: "2026-06-01T00:00:00Z",
    ...extra,
  };
}

function renderView() {
  return render(
    <MemoryRouter initialEntries={["/sessions/s1"]}>
      <Routes>
        <Route path="/sessions/:id" element={<SessionView />} />
        <Route path="/sessions" element={<div data-testid="sessions-list" />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("SessionView route", () => {
  it("renders the approval prompt for a pending_approval session", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({ data: sess("pending_approval"), error: undefined } as never);
    renderView();
    await waitFor(() => expect(screen.getByTestId("approval-prompt")).toBeTruthy());
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/sessions/{id}", { params: { path: { id: "s1" } } });
  });

  it("approve → POST resolve{approve}, then the refetched 'pending' session mounts the terminal", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({ data: sess("pending_approval"), error: undefined } as never)
      .mockResolvedValue({ data: sess("pending"), error: undefined } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: {}, error: undefined } as never);

    renderView();
    await waitFor(() => expect(screen.getByTestId("approval-prompt")).toBeTruthy());
    fireEvent.click(screen.getByText("Approve"));

    await waitFor(() => expect(screen.getByTestId("terminal")).toBeTruthy());
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/sessions/{id}/resolve", {
      params: { path: { id: "s1" } },
      body: { decision: "approve" },
    });
  });

  it("deny → POST resolve{deny}, then the refetched 'closed' session shows the denied state", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({ data: sess("pending_approval"), error: undefined } as never)
      .mockResolvedValue({ data: sess("closed", { closedReason: "approval_denied" }), error: undefined } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: {}, error: undefined } as never);

    renderView();
    await waitFor(() => expect(screen.getByTestId("approval-prompt")).toBeTruthy());
    fireEvent.click(screen.getByText("Deny"));

    await waitFor(() => expect(screen.getByText(/not approved/i)).toBeTruthy());
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/sessions/{id}/resolve", {
      params: { path: { id: "s1" } },
      body: { decision: "deny" },
    });
  });

  it("renders the terminal for an active session", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({ data: sess("active"), error: undefined } as never);
    renderView();
    await waitFor(() => expect(screen.getByTestId("terminal")).toBeTruthy());
  });
});

// === VER-14: does the @aireos/ui mock above still match the real module? ===
//
// 1. Export existence — a runtime check, via vi.importActual to bypass the
//    vi.mock above just for this one lookup. Catches a removed/renamed
//    export; runs as part of the normal `pnpm test` suite.
//
// 2. Props-contract drift — "the part that actually bites" per the ticket:
//    the stubs above are hand-typed to accept exactly the props THIS file
//    assumes ({ url } for TerminalView — the stub ignores it, but it must
//    still exist on the real required-props contract; { onApprove, onDeny }
//    for ApprovalPrompt). The two assignments below are compile-time-only:
//    each is an object literal, independently hand-written here (NOT
//    re-derived from the imported TerminalViewProps/ApprovalPromptProps
//    types — re-deriving would make the check track the target automatically
//    and catch nothing), assigned to a variable typed as the REAL component's
//    Props. If the real component gains a new REQUIRED prop this hand-written
//    literal doesn't supply, the assignment fails to compile.
//
//    What this DOES catch: a new required prop added to TerminalView or
//    ApprovalPrompt's real interface that these hand-written literals don't
//    already satisfy — exactly the "mock stays green while the integration
//    breaks" hazard VER-14 names.
//    What this does NOT catch: a type change to an EXISTING prop that stays
//    optional, or any runtime behavior drift the type system can't see (e.g.
//    the real component calling onApprove with an argument the mock's own
//    stub ignores). It is also enforced at `pnpm --filter @aireos/web run
//    typecheck` time, not by `vitest run` itself — this file's transform is
//    esbuild (type-stripping, no checking), so a bad assignment here would
//    NOT turn `pnpm test` red on its own. Stated plainly so nobody assumes
//    `pnpm test` alone re-proves it; wrapped in an `it()` and read back via
//    `expect` purely so the values are exercised, not just declared unused.
describe("@aireos/ui mock ↔ real module contract (VER-14)", () => {
  it("the real module still exports TerminalView and ApprovalPrompt as functions", async () => {
    const real = await vi.importActual<typeof import("@aireos/ui")>("@aireos/ui");
    expect(typeof real.TerminalView).toBe("function");
    expect(typeof real.ApprovalPrompt).toBe("function");
  });

  it("the mocked stubs' assumed prop shape still satisfies the real components' required props", () => {
    const terminalViewContractCheck: TerminalViewProps = { url: "ws://example.test/channel" };
    const approvalPromptContractCheck: ApprovalPromptProps = {
      onApprove: () => {},
      onDeny: () => {},
    };
    expect(terminalViewContractCheck.url).toBeTruthy();
    expect(typeof approvalPromptContractCheck.onApprove).toBe("function");
  });
});
