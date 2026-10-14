// Tests for the Runs route. Mocks the module-level apiClient (same pattern as
// Sessions.test.tsx). jsdom-level only — the browser-rendered DoD evidence is the
// Playwright runs.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Runs } from "./Runs";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// The route fetches GET /v1/runs (the list) and GET /v1/resources (the picker), and
// polls GET /v1/runs/{id} when a run is selected; mock per path.
function mockGET(opts: { runs?: unknown; runsErr?: unknown; resources?: unknown[]; detail?: unknown }) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") {
      return Promise.resolve({ data: { items: opts.resources ?? [], page: {} }, error: undefined });
    }
    if (path === "/v1/runs/{id}") {
      return Promise.resolve({ data: opts.detail, error: undefined });
    }
    return Promise.resolve({ data: opts.runs, error: opts.runsErr });
  }) as never);
}

function renderRuns() {
  return render(
    <MemoryRouter initialEntries={["/runs"]}>
      <Routes>
        <Route path="/runs" element={<Runs />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Runs route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists the operator's runs from GET /v1/runs", async () => {
    mockGET({ runs: { items: [{ id: "run-1", status: "completed" }], page: {} } });

    renderRuns();

    await waitFor(() => expect(screen.getByText("run-1")).toBeTruthy());
    expect(screen.getByText("completed")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/runs", {});
  });

  it("labels a delegated (agent-driven) run with its actor + grant (FE-19)", async () => {
    mockGET({ runs: { items: [{ id: "run-d", status: "running", actor: "agent:bot-1", grantId: "grant-1" }], page: {} } });

    renderRuns();

    await waitFor(() => expect(screen.getByText("run-d")).toBeTruthy());
    expect(screen.getByText(/driven by/i)).toBeTruthy();
    expect(screen.getByText("agent:bot-1")).toBeTruthy();
    expect(screen.getByText("grant-1")).toBeTruthy();
  });

  it("omits the agent-driven label for an operator-direct run", async () => {
    mockGET({ runs: { items: [{ id: "run-o", status: "completed" }], page: {} } });

    renderRuns();

    await waitFor(() => expect(screen.getByText("run-o")).toBeTruthy());
    expect(screen.queryByText(/driven by/i)).toBeNull();
  });

  it("composes a plan and launches it via POST /v1/runs after the review gate", async () => {
    mockGET({ runs: { items: [], page: {} }, resources: [{ id: "res-1", type: "ssh", name: "edge-01" }] });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { id: "run-new", status: "pending", steps: [] }, error: undefined } as never);

    renderRuns();
    await waitFor(() => expect(screen.getByRole("option", { name: /edge-01/ })).toBeTruthy());

    // Build a one-step plan.
    fireEvent.change(screen.getByLabelText("resource"), { target: { value: "res-1" } });
    fireEvent.change(screen.getByLabelText("command"), { target: { value: "echo hello" } });
    fireEvent.click(screen.getByRole("button", { name: /add step/i }));

    // Informed-launch gate: launch is a two-step (review → confirm), not a direct POST.
    expect(apiClient.POST).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /review & launch/i }));
    expect(screen.getByRole("region", { name: "launch review" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /confirm launch/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/runs", { body: { steps: [{ resourceId: "res-1", command: "echo hello" }] } }),
    );
  });

  it("cancels a running run via POST /v1/runs/{id}/cancel", async () => {
    mockGET({ runs: { items: [{ id: "run-9", status: "running" }], page: {} } });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "canceled" }, error: undefined } as never);

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-9")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^cancel$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/runs/{id}/cancel", { params: { path: { id: "run-9" } } }),
    );
  });

  it("shows an error when listing fails", async () => {
    mockGET({ runs: undefined, runsErr: { code: "internal", message: "boom" } });

    renderRuns();

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });

  // --- T-091: FE-24 (the plan-preview approve/deny for a 'proposed' Run) ---

  it("renders the 'proposed' chip + full plan preview (command visible), and approves it after a confirm", async () => {
    mockGET({
      runs: { items: [{ id: "run-p", status: "proposed" }], page: {} },
      detail: {
        id: "run-p",
        status: "proposed",
        steps: [{ stepIndex: 0, resourceId: "res-1", status: "pending", command: "echo hi" }],
      },
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { id: "run-p", status: "pending", steps: [] }, error: undefined } as never);

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-p")).toBeTruthy());
    expect(screen.getByText("proposed")).toBeTruthy();

    fireEvent.click(screen.getByText("run-p"));
    const preview = await screen.findByRole("region", { name: "plan preview" });
    expect(within(preview).getByLabelText("command").textContent).toBe("echo hi");

    fireEvent.click(within(preview).getByRole("button", { name: /^approve$/i }));
    expect(window.confirm).toHaveBeenCalledWith(expect.stringMatching(/launches this agent-composed plan/i));
    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/runs/{id}/approve", { params: { path: { id: "run-p" } } }),
    );
  });

  it("does not approve when the confirm is dismissed", async () => {
    mockGET({
      runs: { items: [{ id: "run-p", status: "proposed" }], page: {} },
      detail: { id: "run-p", status: "proposed", steps: [{ stepIndex: 0, resourceId: "res-1", status: "pending", command: "echo hi" }] },
    });
    vi.spyOn(window, "confirm").mockReturnValue(false);

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-p")).toBeTruthy());
    fireEvent.click(screen.getByText("run-p"));
    const preview = await screen.findByRole("region", { name: "plan preview" });
    fireEvent.click(within(preview).getByRole("button", { name: /^approve$/i }));

    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("denies a 'proposed' Run WITHOUT a confirm — deny stays first-class, no extra friction (GOV-10)", async () => {
    mockGET({
      runs: { items: [{ id: "run-p", status: "proposed" }], page: {} },
      detail: { id: "run-p", status: "proposed", steps: [{ stepIndex: 0, resourceId: "res-1", status: "pending", command: "echo hi" }] },
    });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { id: "run-p", status: "canceled", steps: [] }, error: undefined } as never);
    const confirmSpy = vi.spyOn(window, "confirm");

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-p")).toBeTruthy());
    fireEvent.click(screen.getByText("run-p"));
    const preview = await screen.findByRole("region", { name: "plan preview" });

    fireEvent.click(within(preview).getByRole("button", { name: /^deny$/i }));
    expect(confirmSpy).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/runs/{id}/deny", { params: { path: { id: "run-p" } } }),
    );
  });

  it("renders the ordinary per-step status list (no preview, no command) for a non-proposed Run", async () => {
    mockGET({
      runs: { items: [{ id: "run-r", status: "running" }], page: {} },
      detail: {
        id: "run-r",
        status: "running",
        steps: [{ stepIndex: 0, resourceId: "res-1", status: "running" }],
      },
    });

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-r")).toBeTruthy());
    fireEvent.click(screen.getByText("run-r"));

    expect(await screen.findByRole("list", { name: "run steps" })).toBeTruthy();
    expect(screen.queryByRole("region", { name: "plan preview" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^approve$/i })).toBeNull();
  });

  it("names the resource (not just its id) in an expanded step, and says output is not stored with an Audit link (PUB-6 P15a)", async () => {
    mockGET({
      runs: { items: [{ id: "run-n", status: "completed" }], page: {} },
      resources: [{ id: "res-9", name: "loopback-demo", type: "ssh" }],
      detail: {
        id: "run-n",
        status: "completed",
        steps: [
          { stepIndex: 0, resourceId: "res-9", status: "completed", outcome: "tool.result" },
          { stepIndex: 1, resourceId: "res-gone", status: "pending" },
        ],
      },
    });

    renderRuns();
    await waitFor(() => expect(screen.getByText("run-n")).toBeTruthy());
    fireEvent.click(screen.getByText("run-n"));

    // Re-query inside waitFor: a terminal Run triggers loadRuns(), which remounts the list.
    const stepsText = () => screen.getByRole("list", { name: "run steps" }).textContent ?? "";
    await waitFor(() => expect(stepsText()).toContain("1. loopback-demo"));
    // A resource the list does not know falls back to its id rather than rendering nothing.
    expect(stepsText()).toContain("2. res-gone");
    expect(screen.getByText(/command output is not stored/i)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Audit timeline" }).getAttribute("href")).toBe("/audit");
  });
});
