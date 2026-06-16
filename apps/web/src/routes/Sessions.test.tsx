// Tests for the Sessions route. Mocks the module-level apiClient (same pattern
// as Login.test.tsx — openapi-fetch captures its fetch reference internally).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Sessions } from "./Sessions";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderSessions() {
  return render(
    <MemoryRouter initialEntries={["/sessions"]}>
      <Routes>
        <Route path="/sessions" element={<Sessions />} />
        <Route path="/sessions/:id" element={<div data-testid="session-view">terminal</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Sessions route", () => {
  beforeEach(() => vi.clearAllMocks());

  // The route now fetches BOTH /v1/sessions and /v1/resources (the picker); mock per path.
  function mockGET(opts: { sessions?: unknown; sessionsErr?: unknown; resources?: unknown[] }) {
    vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
      if (path === "/v1/resources") {
        return Promise.resolve({ data: { items: opts.resources ?? [], page: {} }, error: undefined });
      }
      return Promise.resolve({ data: opts.sessions, error: opts.sessionsErr });
    }) as never);
  }

  it("lists the operator's sessions from GET /v1/sessions", async () => {
    mockGET({
      sessions: { items: [{ id: "sess-1", resourceId: "r", operatorId: "o", mode: "terminal", status: "active", startedAt: "2026-05-30T00:00:00Z" }], page: {} },
    });

    renderSessions();

    await waitFor(() => {
      expect(screen.getByText("sess-1")).toBeTruthy();
    });
    expect(screen.getByText("active")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/sessions", {});
  });

  it("starts a session by picking a resource and navigates to its terminal", async () => {
    mockGET({ sessions: { items: [], page: {} }, resources: [{ id: "res-1", type: "ssh", name: "edge-01" }] });
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { id: "sess-new", resourceId: "res-1", operatorId: "o", mode: "terminal", status: "pending", startedAt: "2026-05-30T00:00:00Z" },
      error: undefined,
    } as never);

    renderSessions();
    await waitFor(() => expect(screen.getByText(/no sessions yet/i)).toBeTruthy());
    // The picker is populated from /v1/resources.
    await waitFor(() => expect(screen.getByRole("option", { name: /edge-01/ })).toBeTruthy());

    fireEvent.change(screen.getByLabelText("resource"), { target: { value: "res-1" } });
    fireEvent.click(screen.getByRole("button", { name: /start session/i }));

    await waitFor(() => {
      expect(screen.getByTestId("session-view")).toBeTruthy();
    });
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/sessions", {
      body: { resourceId: "res-1", mode: "terminal" },
    });
  });

  it("shows an error when listing fails", async () => {
    mockGET({ sessions: undefined, sessionsErr: { code: "internal", message: "boom" } });

    renderSessions();

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toMatch(/could not load/i);
    });
  });
});
