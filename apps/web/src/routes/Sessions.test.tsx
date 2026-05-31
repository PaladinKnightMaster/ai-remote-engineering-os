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

  it("lists the operator's sessions from GET /v1/sessions", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [{ id: "sess-1", resourceId: "r", operatorId: "o", mode: "terminal", status: "active", startedAt: "2026-05-30T00:00:00Z" }], page: {} },
      error: undefined,
    } as never);

    renderSessions();

    await waitFor(() => {
      expect(screen.getByText("sess-1")).toBeTruthy();
    });
    expect(screen.getByText("active")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/sessions", {});
  });

  it("starts a session via POST and navigates to its terminal", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({ data: { items: [], page: {} }, error: undefined } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { id: "sess-new", resourceId: "res-1", operatorId: "o", mode: "terminal", status: "pending", startedAt: "2026-05-30T00:00:00Z" },
      error: undefined,
    } as never);

    renderSessions();
    await waitFor(() => expect(screen.getByText(/no sessions yet/i)).toBeTruthy());

    fireEvent.change(screen.getByPlaceholderText(/resource id/i), { target: { value: "res-1" } });
    fireEvent.click(screen.getByRole("button", { name: /start session/i }));

    await waitFor(() => {
      expect(screen.getByTestId("session-view")).toBeTruthy();
    });
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/sessions", {
      body: { resourceId: "res-1", mode: "terminal" },
    });
  });

  it("shows an error when listing fails", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: undefined,
      error: { code: "internal", message: "boom" },
    } as never);

    renderSessions();

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toMatch(/could not load/i);
    });
  });
});
