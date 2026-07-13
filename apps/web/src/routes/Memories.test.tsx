// Tests for the Memories route (FE-22/FE-23; GOV-9; ADR-0064). Mocks the module-level
// apiClient (the Grants/Workstreams pattern) and wraps in <CapabilityProvider> (the
// Login.test.tsx pattern) since the route derives its default operator scope from
// useCapability. jsdom-level only — the browser-rendered DoD evidence is
// memories.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not satisfy it).

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { CapabilityProvider } from "@aireos/ui";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Memories, MemoryRow, type MemoryView } from "./Memories";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const CAPABILITY = { subject: "operator:admin", edge: "human-agent" };

function mockGET(memories: unknown[], opts: { err?: unknown } = {}) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/memories") {
      if (opts.err) return Promise.resolve({ data: undefined, error: opts.err });
      return Promise.resolve({ data: memories, error: undefined });
    }
    return Promise.resolve({ data: undefined, error: undefined });
  }) as never);
}

function renderRoute() {
  return render(
    <CapabilityProvider fetchCapability={async () => CAPABILITY} initialValue={CAPABILITY}>
      <MemoryRouter initialEntries={["/memories"]}>
        <Routes>
          <Route path="/memories" element={<Memories />} />
        </Routes>
      </MemoryRouter>
    </CapabilityProvider>,
  );
}

const sampleMemory: MemoryView = {
  id: "mem-1",
  scopeType: "operator",
  scopeId: "operator:admin",
  key: "note-1",
  ref: "run:run-1",
  hasContent: true,
  content: "remember this",
  sourceTrace: "operator",
  confidence: 1,
  pinned: false,
  createdAt: "2026-07-06T00:00:00Z",
};

describe("Memories route", () => {
  it("lists memories for the default operator scope (derived from the capability)", async () => {
    mockGET([sampleMemory]);
    renderRoute();

    await waitFor(() =>
      expect(apiClient.GET).toHaveBeenCalledWith("/v1/memories", {
        params: { query: { scopeType: "operator", scopeId: "operator:admin" } },
      }),
    );
    expect(await screen.findByText("note-1")).toBeTruthy();
    expect(screen.getByText("remember this")).toBeTruthy();
  });

  it("shows the GOV-9 consent copy on the authoring form", async () => {
    mockGET([]);
    renderRoute();
    await waitFor(() => expect(screen.getByText(/no memories for this scope/i)).toBeTruthy());
    expect(screen.getByText(/sealed and operator-only/i)).toBeTruthy();
    expect(screen.getByText(/does not and cannot rewrite the audit chain/i)).toBeTruthy();
    expect(screen.getByText(/content-free/i)).toBeTruthy();
  });

  it("authors a memory via POST /v1/memories and renders the new row", async () => {
    mockGET([]);
    const created: MemoryView = { ...sampleMemory, id: "mem-2", key: "new-note", content: "hello" };
    vi.mocked(apiClient.POST).mockResolvedValue({ data: created, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText(/no memories for this scope/i)).toBeTruthy());

    fireEvent.change(screen.getByLabelText("key", { exact: true }), { target: { value: "new-note" } });
    fireEvent.change(screen.getByLabelText("content", { exact: true }), { target: { value: "hello" } });
    fireEvent.click(screen.getByRole("button", { name: /author memory/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/memories", {
        body: expect.objectContaining({ scopeType: "operator", scopeId: "operator:admin", key: "new-note", content: "hello" }),
      }),
    );
    expect(await screen.findByText("new-note")).toBeTruthy();
  });

  it("surfaces a 422 error from the authoring form", async () => {
    mockGET([]);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "unprocessable", message: "content is required" },
    } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText(/no memories for this scope/i)).toBeTruthy());

    fireEvent.change(screen.getByLabelText("content", { exact: true }), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: /author memory/i }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/content is required/i));
  });

  it("redacts a memory via confirm → POST → tombstone rendering (content never blank)", async () => {
    mockGET([sampleMemory]);
    const redacted: MemoryView = { ...sampleMemory, content: undefined, redactedAt: "2026-07-10T00:00:00Z" };
    vi.mocked(apiClient.POST).mockResolvedValue({ data: redacted, error: undefined } as never);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderRoute();
    await waitFor(() => expect(screen.getByText("remember this")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^redact$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/memories/{id}/redact", { params: { path: { id: "mem-1" } } }),
    );
    await waitFor(() => expect(screen.getByText(/^redacted/i)).toBeTruthy());
    expect(screen.queryByText("remember this")).toBeNull();
  });

  it("does not call redact when the confirm is dismissed", async () => {
    mockGET([sampleMemory]);
    vi.spyOn(window, "confirm").mockReturnValue(false);

    renderRoute();
    await waitFor(() => expect(screen.getByText("remember this")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /^redact$/i }));

    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("pins a memory via POST /v1/memories/{id}/pin", async () => {
    mockGET([sampleMemory]);
    const pinned: MemoryView = { ...sampleMemory, pinned: true };
    vi.mocked(apiClient.POST).mockResolvedValue({ data: pinned, error: undefined } as never);

    renderRoute();
    await waitFor(() => expect(screen.getByText("note-1")).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /^pin$/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/memories/{id}/pin", { params: { path: { id: "mem-1" } } }),
    );
    expect(await screen.findByText("Pinned")).toBeTruthy();
  });

  it("exports the listed scope to a downloaded markdown manifest (anchor + blob, not the file)", async () => {
    mockGET([sampleMemory]);
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    // jsdom has no Blob-URL support; stub the two static methods the export path calls.
    const createObjectURL = vi.fn(() => "blob:mock-url");
    const revokeObjectURL = vi.fn();
    const originalCreate = URL.createObjectURL;
    const originalRevoke = URL.revokeObjectURL;
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;

    try {
      renderRoute();
      await waitFor(() => expect(screen.getByText("note-1")).toBeTruthy());
      fireEvent.click(screen.getByRole("button", { name: /^export$/i }));

      expect(createObjectURL).toHaveBeenCalled();
      expect(clickSpy).toHaveBeenCalled();
    } finally {
      URL.createObjectURL = originalCreate;
      URL.revokeObjectURL = originalRevoke;
    }
  });
});

describe("MemoryRow tombstone label (FE-23)", () => {
  it("labels Expired when expiresAt had already passed at redaction time (the sweep)", () => {
    const expired: MemoryView = {
      ...sampleMemory,
      content: undefined,
      expiresAt: "2026-07-01T00:00:00Z",
      redactedAt: "2026-07-02T00:00:00Z",
    };
    render(
      <ul>
        <MemoryRow m={expired} />
      </ul>,
    );
    expect(screen.getByText(/^expired/i)).toBeTruthy();
  });

  it("labels Redacted for an operator redact (expiresAt unset or still in the future)", () => {
    const redactedFuture: MemoryView = {
      ...sampleMemory,
      content: undefined,
      expiresAt: "2026-08-01T00:00:00Z",
      redactedAt: "2026-07-02T00:00:00Z",
    };
    render(
      <ul>
        <MemoryRow m={redactedFuture} />
      </ul>,
    );
    expect(screen.getByText(/^redacted/i)).toBeTruthy();
  });

  it("never renders blank content for a tombstoned row, and shows content-free accrued rows as pointers", () => {
    const accrued: MemoryView = {
      id: "mem-3",
      scopeType: "run",
      scopeId: "run-1",
      ref: "run:run-1/step:0",
      hasContent: false,
      sourceTrace: "run:run-1",
      confidence: 1,
      pinned: false,
      createdAt: "2026-07-06T00:00:00Z",
    };
    const { container } = render(
      <ul>
        <MemoryRow m={accrued} />
      </ul>,
    );
    expect(screen.getByText(/run:run-1\/step:0/)).toBeTruthy();
    // No content block rendered for a content-free accrued pointer (hasContent=false).
    expect(container.querySelector("pre")).toBeNull();
  });
});
