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

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

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
