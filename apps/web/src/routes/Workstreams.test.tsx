// Tests for the Workstreams route (FE-20; ADR-0063). Mocks the module-level apiClient (the
// Grants/Runs pattern). jsdom-level only — the browser-rendered DoD evidence is
// workstreams.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Workstreams } from "./Workstreams";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// The route fetches /v1/workstreams (list) + /v1/grants + /v1/runs (client-side joins) +
// /v1/resources (the picker), and /v1/workstreams/{id} + /v1/memories for the detail
// (the immutable template and the FE-22 scoped-memory promotion, respectively).
function mockGET(opts: {
  workstreams?: unknown[];
  grants?: unknown[];
  runs?: unknown[];
  resources?: unknown[];
  detail?: unknown;
  memories?: unknown[];
  wsErr?: unknown;
}) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") return Promise.resolve({ data: { items: opts.resources ?? [], page: {} }, error: undefined });
    if (path === "/v1/grants") return Promise.resolve({ data: { items: opts.grants ?? [], page: {} }, error: undefined });
    if (path === "/v1/runs") return Promise.resolve({ data: { items: opts.runs ?? [], page: {} }, error: undefined });
    if (path === "/v1/workstreams/{id}") return Promise.resolve({ data: opts.detail, error: undefined });
    if (path === "/v1/memories") return Promise.resolve({ data: opts.memories ?? [], error: undefined });
    if (opts.wsErr) return Promise.resolve({ data: undefined, error: opts.wsErr });
    return Promise.resolve({ data: { items: opts.workstreams ?? [], page: {} }, error: undefined });
  }) as never);
}

function renderRoute() {
  return render(
    <MemoryRouter initialEntries={["/workstreams"]}>
      <Routes>
        <Route path="/workstreams" element={<Workstreams />} />
      </Routes>
    </MemoryRouter>,
  );
}

const sampleWS = {
  id: "ws-1",
  label: "nightly health",
  objective: "keep the fleet healthy",
  grantId: "grant-9",
  intervalSeconds: 300,
  nextFireAt: "2026-07-06T00:05:00Z",
  status: "active",
  createdAt: "2026-07-06T00:00:00Z",
};
const sampleGrant = {
  id: "grant-9",
  agent: "workstream:ws-1",
  resources: ["res-1"],
  verbs: ["exec"],
  expiresAt: "2026-08-01T00:00:00Z",
  maxStepsPerRun: 5,
  maxConcurrentRuns: 2,
  stepBudget: 50,
  status: "active",
  createdAt: "2026-07-06T00:00:00Z",
};

describe("Workstreams route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists charters and joins grant expiry + last-run status client-side", async () => {
    mockGET({
      workstreams: [sampleWS],
      grants: [sampleGrant],
      runs: [{ id: "run-1", status: "completed", grantId: "grant-9", steps: [] }],
    });
    renderRoute();

    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    expect(screen.getByText("active")).toBeTruthy();
    expect(screen.getByText(/every 300s/)).toBeTruthy();
    // The joins: last-run status + grant expiry appear in the row.
    expect(screen.getByText(/last run/)).toBeTruthy();
    expect(screen.getByText("completed")).toBeTruthy();
    expect(screen.getByText("2026-08-01T00:00:00Z")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/workstreams", {});
  });

  it("shows the GOV-8 consent copy and caps max-concurrent-runs at 4", async () => {
    mockGET({});
    renderRoute();
    await waitFor(() => expect(screen.getByLabelText("Max concurrent runs")).toBeTruthy());
    expect(screen.getByLabelText("Max concurrent runs").getAttribute("max")).toBe("4");
    expect(screen.getByText(/unattended, scheduled execution/i)).toBeTruthy();
    expect(screen.getByText(/immutable/i)).toBeTruthy();
  });

  it("charters via POST /v1/workstreams (objective + scope + a template step), window defaulting to the interval", async () => {
    mockGET({ resources: [{ id: "res-1", type: "ssh", name: "edge-01" }] });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: sampleWS, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByRole("option", { name: /edge-01/ })).toBeTruthy());

    fireEvent.change(screen.getByLabelText("objective"), { target: { value: "keep hosts healthy" } });
    fireEvent.change(screen.getByLabelText("scope resource"), { target: { value: "res-1" } });
    fireEvent.click(screen.getByRole("button", { name: /add to scope/i }));
    // The step-resource select is now populated from the scope.
    fireEvent.change(screen.getByLabelText("step resource"), { target: { value: "res-1" } });
    fireEvent.change(screen.getByLabelText("step command"), { target: { value: "uptime" } });
    fireEvent.click(screen.getByRole("button", { name: /add step/i }));
    fireEvent.click(screen.getByRole("button", { name: /charter workstream/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith(
        "/v1/workstreams",
        expect.objectContaining({
          body: expect.objectContaining({
            objective: "keep hosts healthy",
            resources: ["res-1"],
            steps: [{ resourceId: "res-1", command: "uptime" }],
          }),
        }),
      ),
    );
    // budgetWindowSeconds omitted (0 → server defaults to the interval).
    const charterCall = vi.mocked(apiClient.POST).mock.calls.find((c) => c[0] === "/v1/workstreams");
    expect(charterCall).toBeDefined();
    const body = (charterCall![1] as { body: Record<string, unknown> }).body;
    expect(body.budgetWindowSeconds).toBeUndefined();
  });

  it("pauses a charter via POST /v1/workstreams/{id}/pause", async () => {
    mockGET({ workstreams: [sampleWS], grants: [sampleGrant] });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "paused" }, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /^pause$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/workstreams/{id}/pause", { params: { path: { id: "ws-1" } } }),
    );
  });

  it("retires a charter via POST /v1/workstreams/{id}/retire", async () => {
    mockGET({ workstreams: [sampleWS], grants: [sampleGrant] });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "retired" }, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /^retire$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/workstreams/{id}/retire", { params: { path: { id: "ws-1" } } }),
    );
  });

  it("shows a paused charter's Resume control instead of Pause", async () => {
    mockGET({ workstreams: [{ ...sampleWS, status: "paused" }], grants: [sampleGrant] });
    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    expect(screen.getByRole("button", { name: /^resume$/i })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^pause$/i })).toBeNull();
  });

  it("shows the charter's scoped memory (FE-22: an accrued content-free pointer, read-only)", async () => {
    mockGET({
      workstreams: [sampleWS],
      grants: [sampleGrant],
      detail: { ...sampleWS, steps: [] },
      memories: [
        {
          id: "mem-1",
          scopeType: "workstream",
          scopeId: "ws-1",
          ref: "run:run-1/step:0",
          hasContent: false,
          sourceTrace: "run:run-1",
          confidence: 1,
          pinned: false,
          createdAt: "2026-07-06T00:00:00Z",
        },
      ],
    });
    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    expect(await screen.findByText(/scoped memory/i)).toBeTruthy();
    // The accrued pointer renders as ref + sourceTrace (content-free) — never as empty
    // content, and with NO verb buttons (read-only here; verbs live on /memories).
    expect(await screen.findByText(/run:run-1\/step:0/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /^redact$/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^pin$/i })).toBeNull();
  });

  it("shows an error when listing fails", async () => {
    mockGET({ wsErr: { code: "internal", message: "boom" } });
    renderRoute();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });
});
