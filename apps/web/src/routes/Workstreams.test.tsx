// Tests for the Workstreams route (FE-20; ADR-0063). Mocks the module-level apiClient (the
// Grants/Runs pattern). jsdom-level only — the browser-rendered DoD evidence is
// workstreams.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() },
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
  contextSources?: unknown[];
  wsErr?: unknown;
}) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") return Promise.resolve({ data: { items: opts.resources ?? [], page: {} }, error: undefined });
    if (path === "/v1/grants") return Promise.resolve({ data: { items: opts.grants ?? [], page: {} }, error: undefined });
    if (path === "/v1/runs") return Promise.resolve({ data: { items: opts.runs ?? [], page: {} }, error: undefined });
    if (path === "/v1/workstreams/{id}") return Promise.resolve({ data: opts.detail, error: undefined });
    if (path === "/v1/memories") return Promise.resolve({ data: opts.memories ?? [], error: undefined });
    if (path === "/v1/context-sources") return Promise.resolve({ data: opts.contextSources ?? [], error: undefined });
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

  // --- T-091: FE-25 (the share control on the charter detail's memory rows) ---

  it("shares an authored memory row on the charter detail (share/unshare live here; redact/pin still do not)", async () => {
    mockGET({
      workstreams: [sampleWS],
      grants: [sampleGrant],
      detail: { ...sampleWS, steps: [] },
      memories: [
        {
          id: "mem-authored",
          scopeType: "workstream",
          scopeId: "ws-1",
          key: "note",
          ref: "",
          hasContent: true,
          content: "a note",
          sourceTrace: "operator",
          confidence: 1,
          pinned: false,
          agentVisible: false,
          createdAt: "2026-07-06T00:00:00Z",
        },
      ],
    });
    vi.spyOn(window, "confirm").mockReturnValue(true);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { id: "mem-authored", agentVisible: true, hasContent: true, pinned: false },
      error: undefined,
    } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    const shareBtn = await screen.findByRole("button", { name: /^share$/i });
    fireEvent.click(shareBtn);

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/memories/{id}/share", { params: { path: { id: "mem-authored" } } }),
    );
    expect(screen.queryByRole("button", { name: /^redact$/i })).toBeNull();
    expect(screen.queryByRole("button", { name: /^pin$/i })).toBeNull();
  });

  // --- T-091: D4 (the adaptive charter form) ---

  it("hides the step editor and requires a composer for an adaptive charter, then charters with kind+composer and no steps", async () => {
    mockGET({ resources: [{ id: "res-1", type: "ssh", name: "edge-01" }] });
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { ...sampleWS, kind: "adaptive", composer: "agent:composer-1", steps: [] },
      error: undefined,
    } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByRole("option", { name: /edge-01/ })).toBeTruthy());

    fireEvent.change(screen.getByLabelText("objective"), { target: { value: "keep hosts healthy" } });
    fireEvent.change(screen.getByLabelText("scope resource", { exact: true }), { target: { value: "res-1" } });
    fireEvent.click(screen.getByRole("button", { name: /add to scope/i }));

    fireEvent.change(screen.getByLabelText("workstream kind"), { target: { value: "adaptive" } });
    // The step editor is gone; only the composer field remains.
    expect(screen.queryByLabelText("step resource", { exact: true })).toBeNull();
    expect(screen.queryByRole("button", { name: /add step/i })).toBeNull();
    expect((screen.getByRole("button", { name: /charter workstream/i }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.change(screen.getByLabelText("composer agent"), { target: { value: "agent:composer-1" } });
    expect((screen.getByRole("button", { name: /charter workstream/i }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /charter workstream/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith(
        "/v1/workstreams",
        expect.objectContaining({
          body: expect.objectContaining({ kind: "adaptive", composer: "agent:composer-1", resources: ["res-1"] }),
        }),
      ),
    );
    const charterCall = vi.mocked(apiClient.POST).mock.calls.find((c) => c[0] === "/v1/workstreams");
    const body = (charterCall![1] as { body: Record<string, unknown> }).body;
    expect(body.steps).toBeUndefined();
  });

  it("shows the GOV-10 consent copy for an adaptive charter (parks for review; memory access named)", async () => {
    mockGET({});
    renderRoute();
    await waitFor(() => expect(screen.getByLabelText("workstream kind")).toBeTruthy());
    fireEvent.change(screen.getByLabelText("workstream kind"), { target: { value: "adaptive" } });
    expect(screen.getByText(/always parks for your review/i)).toBeTruthy();
    expect(screen.getByText(/accrued memory/i)).toBeTruthy();
  });

  // --- T-091: D3 (the plan-request history) + kind badge/composer on the detail ---

  it("renders the adaptive charter's kind badge + composer, and its plan-request history (open/submitted/expired)", async () => {
    const adaptiveWS = { ...sampleWS, kind: "adaptive" as const, composer: "agent:composer-1" };
    mockGET({
      workstreams: [adaptiveWS],
      grants: [sampleGrant],
      detail: {
        ...adaptiveWS,
        steps: [],
        planRequests: [
          { id: "pr-1", status: "open", expiresAt: "2026-07-06T00:05:00Z" },
          { id: "pr-2", status: "submitted", expiresAt: "2026-07-06T00:05:00Z", runId: "run-9" },
          { id: "pr-3", status: "expired", expiresAt: "2026-07-05T00:05:00Z" },
        ],
      },
    });
    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    expect(await screen.findByText("adaptive")).toBeTruthy();
    expect(screen.getByText("agent:composer-1")).toBeTruthy();
    expect(await screen.findByText(/plan requests/i)).toBeTruthy();
    expect(screen.getByText(/open · expires/i)).toBeTruthy();
    expect(screen.getByText(/submitted ·/i)).toBeTruthy();
    expect(screen.getByText("run-9")).toBeTruthy();
    expect(screen.getByText("expired")).toBeTruthy();
  });

  // --- T-100b: FE-26 / GOV-11 (the charter's bound context sources) ---

  it("lists the charter's context sources with their staleness signal (active/errored + never-indexed)", async () => {
    mockGET({
      workstreams: [sampleWS],
      grants: [sampleGrant],
      detail: { ...sampleWS, steps: [] },
      contextSources: [
        { id: "cs-1", rootPath: "/docs/runbooks", scopeType: "workstream", scopeId: "ws-1", status: "active", lastIndexedAt: "2026-07-06T00:00:00Z", createdAt: "2026-07-06T00:00:00Z", updatedAt: "2026-07-06T00:00:00Z" },
        { id: "cs-2", rootPath: "/missing/root", scopeType: "workstream", scopeId: "ws-1", status: "errored", createdAt: "2026-07-06T00:00:00Z", updatedAt: "2026-07-06T00:00:00Z" },
      ],
    });
    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    const csHeading = await screen.findByText(/context sources/i);
    const csSection = csHeading.closest("div")!;
    expect(within(csSection).getByText("/docs/runbooks")).toBeTruthy();
    expect(within(csSection).getByText("active")).toBeTruthy();
    expect(within(csSection).getByText("/missing/root")).toBeTruthy();
    expect(within(csSection).getByText("errored")).toBeTruthy();
    expect(within(csSection).getByText(/never indexed \/ pending/i)).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/context-sources", { params: { query: { workstreamId: "ws-1" } } });
  });

  it("shows the GOV-11 confirm copy and registers a context source via POST /v1/context-sources", async () => {
    mockGET({ workstreams: [sampleWS], grants: [sampleGrant], detail: { ...sampleWS, steps: [] } });
    let confirmMessage = "";
    vi.spyOn(window, "confirm").mockImplementation((msg) => {
      confirmMessage = msg ?? "";
      return true;
    });
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: {
        id: "cs-new",
        rootPath: "/docs/tickets",
        scopeType: "workstream",
        scopeId: "ws-1",
        status: "active",
        createdAt: "2026-07-06T00:00:00Z",
        updatedAt: "2026-07-06T00:00:00Z",
      },
      error: undefined,
    } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    // GOV-11: the consent copy renders on the surface itself (not only the confirm dialog).
    expect(await screen.findByText(/readable by this charter's composer/i)).toBeTruthy();

    fireEvent.change(await screen.findByLabelText("root path"), { target: { value: "/docs/tickets" } });
    fireEvent.click(screen.getByRole("button", { name: /^register$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/context-sources", {
        body: { rootPath: "/docs/tickets", workstreamId: "ws-1" },
      }),
    );
    expect(confirmMessage).toMatch(/readable by this charter's composer agent/i);
    expect(confirmMessage).toMatch(/prompt-injection/i);
    expect(await screen.findByText("/docs/tickets")).toBeTruthy();
  });

  it("does not register when the GOV-11 confirm is dismissed", async () => {
    mockGET({ workstreams: [sampleWS], grants: [sampleGrant], detail: { ...sampleWS, steps: [] } });
    vi.spyOn(window, "confirm").mockReturnValue(false);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    fireEvent.change(await screen.findByLabelText("root path"), { target: { value: "/docs/tickets" } });
    fireEvent.click(screen.getByRole("button", { name: /^register$/i }));

    await waitFor(() => expect(window.confirm).toHaveBeenCalled());
    expect(apiClient.POST).not.toHaveBeenCalledWith("/v1/context-sources", expect.anything());
  });

  it("shows the GOV-11 removal confirm copy and removes a context source via DELETE /v1/context-sources/{id}", async () => {
    mockGET({
      workstreams: [sampleWS],
      grants: [sampleGrant],
      detail: { ...sampleWS, steps: [] },
      contextSources: [
        { id: "cs-1", rootPath: "/docs/runbooks", scopeType: "workstream", scopeId: "ws-1", status: "active", createdAt: "2026-07-06T00:00:00Z", updatedAt: "2026-07-06T00:00:00Z" },
      ],
    });
    let confirmMessage = "";
    vi.spyOn(window, "confirm").mockImplementation((msg) => {
      confirmMessage = msg ?? "";
      return true;
    });
    vi.mocked(apiClient.DELETE).mockResolvedValue({ data: undefined, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("nightly health")).toBeTruthy());
    fireEvent.click(screen.getByText("nightly health"));

    const row = (await screen.findByText("/docs/runbooks")).closest("li")!;
    fireEvent.click(within(row).getByRole("button", { name: /^remove$/i }));

    await waitFor(() =>
      expect(apiClient.DELETE).toHaveBeenCalledWith("/v1/context-sources/{id}", { params: { path: { id: "cs-1" } } }),
    );
    expect(confirmMessage).toMatch(/stops future agent reads/i);
    expect(confirmMessage).toMatch(/cannot recall/i);
    expect(screen.queryByText("/docs/runbooks")).toBeNull();
  });
});
