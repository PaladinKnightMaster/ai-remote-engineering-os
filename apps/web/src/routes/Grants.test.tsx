// Tests for the Grants route (FE-17). Mocks the module-level apiClient (the Runs/Sessions
// pattern). jsdom-level only — the browser-rendered DoD evidence is grants.smoke.spec.ts
// (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Grants } from "./Grants";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// The route fetches GET /v1/grants (the list) and GET /v1/resources (the picker).
function mockGET(opts: { grants?: unknown; grantsErr?: unknown; resources?: unknown[] }) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") {
      return Promise.resolve({ data: { items: opts.resources ?? [], page: {} }, error: undefined });
    }
    return Promise.resolve({ data: opts.grants, error: opts.grantsErr });
  }) as never);
}

function renderGrants() {
  return render(
    <MemoryRouter initialEntries={["/grants"]}>
      <Routes>
        <Route path="/grants" element={<Grants />} />
      </Routes>
    </MemoryRouter>,
  );
}

const sampleGrant = {
  id: "grant-1",
  agent: "agent:bot-1",
  resources: ["res-1"],
  verbs: ["exec"],
  expiresAt: "2026-01-01T00:00:00Z",
  maxStepsPerRun: 10,
  maxConcurrentRuns: 4,
  stepBudget: 50,
  status: "active",
  createdAt: "2026-01-01T00:00:00Z",
};

describe("Grants route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists grants from GET /v1/grants", async () => {
    mockGET({ grants: { items: [sampleGrant], page: {} } });

    renderGrants();

    await waitFor(() => expect(screen.getByText("agent:bot-1")).toBeTruthy());
    expect(screen.getByText("active")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/grants", {});
  });

  it("issues a grant via POST /v1/grants (agent + a picked resource)", async () => {
    mockGET({ grants: { items: [], page: {} }, resources: [{ id: "res-1", type: "ssh", name: "edge-01" }] });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: sampleGrant, error: undefined } as never);

    renderGrants();
    await waitFor(() => expect(screen.getByRole("option", { name: /edge-01/ })).toBeTruthy());

    fireEvent.change(screen.getByLabelText("agent"), { target: { value: "agent:bot-1" } });
    fireEvent.change(screen.getByLabelText("resource"), { target: { value: "res-1" } });
    fireEvent.click(screen.getByRole("button", { name: /add resource/i }));
    fireEvent.click(screen.getByRole("button", { name: /issue grant/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith(
        "/v1/grants",
        expect.objectContaining({
          body: expect.objectContaining({ agent: "agent:bot-1", resources: ["res-1"], verbs: ["exec"] }),
        }),
      ),
    );
  });

  it("revokes a grant via POST /v1/grants/{id}/revoke", async () => {
    mockGET({ grants: { items: [sampleGrant], page: {} } });
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "revoked" }, error: undefined } as never);

    renderGrants();
    await waitFor(() => expect(screen.getByText("agent:bot-1")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^revoke$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/grants/{id}/revoke", { params: { path: { id: "grant-1" } } }),
    );
  });

  it("shows an error when listing fails", async () => {
    mockGET({ grants: undefined, grantsErr: { code: "internal", message: "boom" } });

    renderGrants();

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });
});
