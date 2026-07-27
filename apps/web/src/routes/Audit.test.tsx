// Tests for the Audit route. Mocks the module-level apiClient (same pattern as
// Sessions.test.tsx — openapi-fetch captures its fetch reference internally).
//
// T-107 additions: the newest-first + preset default request shape (AUD-24,
// FE-31), the disclosure banner rendering from RESPONSE fields rather than local
// request state (FE-32, EVD-8 — the point being that a banner driven by request
// state could lie about what the server actually did), and that any control
// change (direction/preset/actor/outcome/edge) issues a fresh request with the
// cursor cleared and RESETS the accumulated list rather than appending onto it
// (FE-33, AUD-26).

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

  it("opens newest-first on the preset (AUD-24/FE-31)", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [evt("e1", "auth.login.success")], page: { preset: "governed-actions@1", chainHead: 42 } },
      error: undefined,
    } as never);

    renderAudit();

    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    expect(screen.getByText("operator:admin")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: undefined,
          direction: "desc",
          preset: "governed-actions@1",
          actor: undefined,
          outcome: undefined,
          edge: undefined,
        },
      },
    });
  });

  it("pages forward with the cursor on Load more, within the same request set", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({
        data: { items: [evt("e1", "auth.login.success")], page: { nextCursor: "10", preset: "governed-actions@1", chainHead: 42 } },
        error: undefined,
      } as never)
      .mockResolvedValueOnce({
        data: { items: [evt("e2", "session.open")], page: { preset: "governed-actions@1", chainHead: 42 } },
        error: undefined,
      } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /load more/i }));

    await waitFor(() => expect(screen.getByText("session.open")).toBeTruthy());
    // first event still present (appended, not replaced)
    expect(screen.getByText("auth.login.success")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: "10",
          direction: "desc",
          preset: "governed-actions@1",
          actor: undefined,
          outcome: undefined,
          edge: undefined,
        },
      },
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

  it("renders the disclosure banner from the RESPONSE, not from local request state (FE-32/EVD-8)", async () => {
    // The component never requested an actor filter, yet the mocked response
    // reports one was applied — proving the banner reflects what the server
    // says happened, not what the client asked for. A banner driven by request
    // state would never be able to show this.
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        items: [evt("e1", "auth.login.success")],
        page: { preset: "governed-actions@1", actor: "workstream:server-said-so", chainHead: 999 },
      },
      error: undefined,
    } as never);

    renderAudit();

    const banner = await screen.findByRole("region", { name: "filtered view" });
    expect(banner.textContent).toMatch(/governed-actions@1/);
    expect(banner.textContent).toMatch(/workstream:server-said-so/);
    expect(banner.textContent).toMatch(/999/);
  });

  it("shows no banner when the response reports nothing applied", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [evt("e1", "auth.login.success")], page: { chainHead: 5 } },
      error: undefined,
    } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    expect(screen.queryByRole("region", { name: "filtered view" })).toBeNull();
  });

  it("resets the list (never appends) and clears the cursor when a filter changes (FE-33/AUD-26)", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({
        data: { items: [evt("e1", "auth.login.success")], page: { nextCursor: "10", preset: "governed-actions@1", chainHead: 42 } },
        error: undefined,
      } as never)
      .mockResolvedValueOnce({
        data: { items: [evt("e2", "session.open")], page: { preset: "governed-actions@1", chainHead: 42 } },
        error: undefined,
      } as never)
      .mockResolvedValueOnce({
        data: { items: [evt("e3", "resource.created")], page: { preset: "governed-actions@1", actor: "operator:someone", chainHead: 42 } },
        error: undefined,
      } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());

    // Accumulate a second page via Load more (append behavior, unchanged).
    fireEvent.click(screen.getByRole("button", { name: /load more/i }));
    await waitFor(() => expect(screen.getByText("session.open")).toBeTruthy());
    expect(screen.getByText("auth.login.success")).toBeTruthy();

    // Now change the actor filter — a fresh request with the cursor cleared, and
    // the accumulated two-item list is REPLACED by the new single-item page, not
    // appended onto (which would misrepresent the chain per FE-33).
    fireEvent.change(screen.getByLabelText("actor"), { target: { value: "operator:someone" } });

    await waitFor(() => expect(screen.getByText("resource.created")).toBeTruthy());
    expect(screen.queryByText("auth.login.success")).toBeNull();
    expect(screen.queryByText("session.open")).toBeNull();

    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: undefined,
          direction: "desc",
          preset: "governed-actions@1",
          actor: "operator:someone",
          outcome: undefined,
          edge: undefined,
        },
      },
    });
  });

  it('"Show all events" clears the preset and every filter in one interaction and re-reads', async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce({
        data: { items: [evt("e1", "auth.login.success")], page: { preset: "governed-actions@1", chainHead: 42 } },
        error: undefined,
      } as never)
      .mockResolvedValueOnce({
        data: { items: [evt("e2", "plan.expired")], page: { chainHead: 42 } },
        error: undefined,
      } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    expect(await screen.findByRole("region", { name: "filtered view" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /show all events/i }));

    await waitFor(() => expect(screen.getByText("plan.expired")).toBeTruthy());
    expect(screen.queryByRole("region", { name: "filtered view" })).toBeNull();
    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: undefined,
          direction: "desc",
          preset: undefined,
          actor: undefined,
          outcome: undefined,
          edge: undefined,
        },
      },
    });
  });
});
