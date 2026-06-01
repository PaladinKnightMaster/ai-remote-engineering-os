// Tests for the Audit route. Mocks the module-level apiClient (same pattern as
// Sessions.test.tsx — openapi-fetch captures its fetch reference internally).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn() },
}));

import { Audit } from "./Audit";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const evt = (id: string, outcome: string) => ({
  id,
  timestamp: "2026-06-01T00:00:00Z",
  actor: "operator:admin",
  edge: "human-agent",
  target: "session:abc",
  outcome,
  evidencePacketId: "sha256:deadbeef",
});

function renderAudit() {
  return render(
    <MemoryRouter initialEntries={["/audit"]}>
      <Audit />
    </MemoryRouter>,
  );
}

describe("Audit route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists chain events from GET /v1/audit/events", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [evt("e1", "auth.login.success")], page: {} },
      error: undefined,
    } as never);

    renderAudit();

    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    expect(screen.getByText("operator:admin")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/audit/events", {
      params: { query: { limit: 50, cursor: undefined } },
    });
  });

  it("pages forward with the cursor on Load more", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({
        data: { items: [evt("e1", "auth.login.success")], page: { nextCursor: "10" } },
        error: undefined,
      } as never)
      .mockResolvedValueOnce({
        data: { items: [evt("e2", "session.open")], page: {} },
        error: undefined,
      } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /load more/i }));

    await waitFor(() => expect(screen.getByText("session.open")).toBeTruthy());
    // first event still present (appended, not replaced)
    expect(screen.getByText("auth.login.success")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: { query: { limit: 50, cursor: "10" } },
    });
  });

  it("shows an error when the timeline fails to load", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: undefined,
      error: { code: "bad_gateway", message: "boom" },
    } as never);

    renderAudit();

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });
});
