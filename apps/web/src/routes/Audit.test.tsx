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
//
// Commander-review follow-up (post-02b37be): the actor/outcome text inputs must
// NOT fetch per keystroke (a request storm on the evidence surface) and a
// stale response racing a newer one must never win — otherwise the EVD-8
// banner could describe a filter that did not produce the rows on screen.
// Both are covered below.

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

    // Now change the actor filter and commit it (blur) — a fresh request with
    // the cursor cleared, and the accumulated two-item list is REPLACED by the
    // new single-item page, not appended onto (which would misrepresent the
    // chain per FE-33). Typing alone (no blur/Enter) must not fetch — proven
    // separately below — so this test commits explicitly.
    const actorInput = screen.getByLabelText("actor");
    fireEvent.change(actorInput, { target: { value: "operator:someone" } });
    fireEvent.blur(actorInput);

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

  it("typing in the actor filter issues no requests; committing (blur) issues exactly one with the trimmed value", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [evt("e1", "auth.login.success")], page: { preset: "governed-actions@1", chainHead: 1 } },
      error: undefined,
    } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    const callsAfterMount = vi.mocked(apiClient.GET).mock.calls.length;

    // Several keystrokes — an onChange fires per character, and wiring that
    // directly to a request would fire a filtered-chain-read + a MAX(seq) per
    // character typed on the surface this release exists to make fast.
    const actorInput = screen.getByLabelText("actor");
    fireEvent.change(actorInput, { target: { value: "o" } });
    fireEvent.change(actorInput, { target: { value: "op" } });
    fireEvent.change(actorInput, { target: { value: "operator:admin" } });
    expect(vi.mocked(apiClient.GET).mock.calls.length).toBe(callsAfterMount);

    // Committing (blur) issues exactly one fresh request, with the trimmed value.
    fireEvent.blur(actorInput);
    await waitFor(() => expect(vi.mocked(apiClient.GET).mock.calls.length).toBe(callsAfterMount + 1));
    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: undefined,
          direction: "desc",
          preset: "governed-actions@1",
          actor: "operator:admin",
          outcome: undefined,
          edge: undefined,
        },
      },
    });
  });

  it('submitting the "Apply" button also commits the text filters (the form\'s onSubmit, same path as Enter)', async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [evt("e1", "auth.login.success")], page: { preset: "governed-actions@1", chainHead: 1 } },
      error: undefined,
    } as never);

    renderAudit();
    await waitFor(() => expect(screen.getByText("auth.login.success")).toBeTruthy());
    const callsAfterMount = vi.mocked(apiClient.GET).mock.calls.length;

    fireEvent.change(screen.getByLabelText("outcome"), { target: { value: "session.open" } });
    expect(vi.mocked(apiClient.GET).mock.calls.length).toBe(callsAfterMount); // still just a draft

    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(vi.mocked(apiClient.GET).mock.calls.length).toBe(callsAfterMount + 1));
    expect(apiClient.GET).toHaveBeenLastCalledWith("/v1/audit/events", {
      params: {
        query: {
          limit: 50,
          cursor: undefined,
          direction: "desc",
          preset: "governed-actions@1",
          actor: undefined,
          outcome: "session.open",
          edge: undefined,
        },
      },
    });
  });

  it("T-111/FE-32/EVD-8: an empty FILTERED response says no matches, not 'no events yet'", async () => {
    // The banner above already reports the chain size (`chainHead: 2413` here);
    // this empty-state message must not restate it — only the banner does.
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [], page: { preset: "governed-actions@1", chainHead: 2413 } },
      error: undefined,
    } as never);

    renderAudit();

    await waitFor(() => expect(screen.getByText("No events match this filter.")).toBeTruthy());
    expect(screen.queryByText("No audit events yet.")).toBeNull();
  });

  it("T-111: an empty UNFILTERED response keeps the v0.1.0 copy byte-for-byte", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [], page: {} },
      error: undefined,
    } as never);

    renderAudit();

    await waitFor(() => expect(screen.getByText("No audit events yet.")).toBeTruthy());
    expect(screen.queryByText("No events match this filter.")).toBeNull();
  });

  it("EVD-8: a stale response is discarded — the rendered rows and the banner both reflect the LATEST request, not arrival order", async () => {
    let resolveFirst!: (v: unknown) => void;
    let resolveSecond!: (v: unknown) => void;
    const first = new Promise((resolve) => {
      resolveFirst = resolve;
    });
    const second = new Promise((resolve) => {
      resolveSecond = resolve;
    });

    vi.mocked(apiClient.GET)
      .mockReturnValueOnce(first as never) // request #1: the initial mount load
      .mockReturnValueOnce(second as never); // request #2: triggered by the edge filter below

    renderAudit();
    await waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(1));

    // Trigger request #2 (a discrete select change — no commit step needed)
    // BEFORE #1 resolves.
    fireEvent.change(screen.getByLabelText("edge"), { target: { value: "human-agent" } });
    await waitFor(() => expect(apiClient.GET).toHaveBeenCalledTimes(2));

    // Resolve #2 (the NEWER request) first.
    resolveSecond({
      data: { items: [evt("e2", "session.open")], page: { edge: "human-agent", chainHead: 2 } },
      error: undefined,
    });
    await waitFor(() => expect(screen.getByText("session.open")).toBeTruthy());

    // Resolve #1 (the STALE request) after. It must be discarded in full —
    // events, page meta, and cursor move together — so the view stays exactly
    // what #2 produced; it must not revert, and the banner must not describe
    // #1's preset while #2's rows are on screen.
    resolveFirst({
      data: { items: [evt("e1", "auth.login.success")], page: { preset: "governed-actions@1", chainHead: 999 } },
      error: undefined,
    });
    await new Promise((r) => setTimeout(r, 10)); // flush the resolved microtask queue

    expect(screen.getByText("session.open")).toBeTruthy();
    expect(screen.queryByText("auth.login.success")).toBeNull();
    const banner = screen.getByRole("region", { name: "filtered view" });
    expect(banner.textContent).toMatch(/human-agent/);
    expect(banner.textContent).not.toMatch(/999/);
    expect(banner.textContent).not.toMatch(/governed-actions@1/);
  });
});
