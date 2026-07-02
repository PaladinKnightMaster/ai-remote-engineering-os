// Tests for the Runs route. Mocks the module-level apiClient (same pattern as
// Sessions.test.tsx). jsdom-level only — the browser-rendered DoD evidence is the
// Playwright runs.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
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
});
