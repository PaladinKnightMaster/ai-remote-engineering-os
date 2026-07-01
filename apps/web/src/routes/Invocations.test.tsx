// Tests for the Invocations route (jsdom). Mocks the module-level apiClient (same
// pattern as Sessions.test.tsx). jsdom does NOT satisfy the DoD addendum for the
// rendering change — the browser-rendered evidence is the T-040c smoke; these cover
// the list + resolve logic.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Invocations } from "./Invocations";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderInvocations() {
  return render(
    <MemoryRouter initialEntries={["/invocations"]}>
      <Invocations />
    </MemoryRouter>,
  );
}

const onePending = {
  data: {
    invocations: [
      { id: "inv-1", agent: "agent:smoke", resourceId: "r-1", tool: "remote.exec", command: "echo INVOKE-OK", status: "pending_approval" },
    ],
  },
  error: undefined,
};

describe("Invocations route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists parked invocations with the command from GET /v1/invocations", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue(onePending as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.getByLabelText("command").textContent).toBe("echo INVOKE-OK");
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/invocations", {});
  });

  it("shows the Run context (FE-16) for a parked governed-run step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-2", agent: "operator:admin", resourceId: "r-1", tool: "remote.exec", command: "echo step", status: "pending_approval", runId: "run-abc", stepIndex: 0 },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/part of run/i)).toBeTruthy());
    expect(screen.getByText("run-abc")).toBeTruthy();
    expect(screen.getByText(/step 1/i)).toBeTruthy(); // zero-based index rendered 1-based
  });

  it("shows the delegated context (FE-18) for an agent-driven grant-bounded step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-3", agent: "agent:bot-1", resourceId: "r-1", tool: "remote.exec", command: "echo del", status: "pending_approval", runId: "run-xyz", stepIndex: 0, principal: "operator:admin" },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/on behalf of/i)).toBeTruthy());
    expect(screen.getByText("operator:admin")).toBeTruthy();
    expect(screen.getByText("run-xyz")).toBeTruthy(); // the FE-16 run context is still shown
  });

  it("omits the delegated context for an operator-direct run step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-4", agent: "operator:admin", resourceId: "r-1", tool: "remote.exec", command: "echo op", status: "pending_approval", runId: "run-op", stepIndex: 0 },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/part of run/i)).toBeTruthy());
    expect(screen.queryByText(/on behalf of/i)).toBeNull();
  });

  it("omits the Run context for a non-run agent invocation", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue(onePending as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.queryByText(/part of run/i)).toBeNull();
  });

  it("denies an invocation via POST /v1/invocations/{id}/resolve and refetches", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce(onePending as never)
      .mockResolvedValueOnce({ data: { invocations: [] }, error: undefined } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "resolved" }, error: undefined } as never);

    renderInvocations();
    await waitFor(() => expect(screen.getByRole("button", { name: /deny/i })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /deny/i }));

    await waitFor(() => expect(screen.getByText(/no tool invocations are awaiting approval/i)).toBeTruthy());
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/invocations/{id}/resolve", {
      params: { path: { id: "inv-1" } },
      body: { decision: "deny" },
    });
  });

  it("shows an error when listing fails", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({ data: undefined, error: { code: "bad_gateway", message: "down" } } as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });
});
