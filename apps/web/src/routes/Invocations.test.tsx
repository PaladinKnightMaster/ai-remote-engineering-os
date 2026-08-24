// Tests for the Invocations route (jsdom). Mocks the module-level apiClient (same
// pattern as Sessions.test.tsx). jsdom does NOT satisfy the DoD addendum for the
// rendering change — the browser-rendered evidence is the T-040c smoke; these cover
// the list + resolve logic.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Invocations } from "./Invocations";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderInvocations() {
  return render(
    <MemoryRouter initialEntries={["/invocations"]}>
      <Invocations />
    </MemoryRouter>,
  );
}

const onePending = {
  data: {
    invocations: [
      { id: "inv-1", agent: "agent:smoke", resourceId: "r-1", tool: "remote.exec", command: "echo INVOKE-OK", status: "pending_approval" },
    ],
  },
  error: undefined,
};

describe("Invocations route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists parked invocations with the command from GET /v1/invocations", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue(onePending as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.getByLabelText("command").textContent).toBe("echo INVOKE-OK");
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/invocations", {});
  });

  it("shows the Run context (FE-16) for a parked governed-run step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-2", agent: "operator:admin", resourceId: "r-1", tool: "remote.exec", command: "echo step", status: "pending_approval", runId: "run-abc", stepIndex: 0 },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/part of run/i)).toBeTruthy());
    expect(screen.getByText("run-abc")).toBeTruthy();
    expect(screen.getByText(/step 1/i)).toBeTruthy(); // zero-based index rendered 1-based
  });

  it("shows the delegated context (FE-18/GOV-6) for an agent-driven grant-bounded step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-3", agent: "agent:bot-1", resourceId: "r-1", tool: "remote.exec", command: "echo del", status: "pending_approval", runId: "run-xyz", stepIndex: 0, principal: "operator:admin", grantId: "grant-42" },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/on behalf of/i)).toBeTruthy());
    expect(screen.getByText("operator:admin")).toBeTruthy();
    expect(screen.getByText("run-xyz")).toBeTruthy(); // the FE-16 run context is still shown
    expect(screen.getByText(/· grant/i)).toBeTruthy(); // GOV-6: the bounding grant id
    expect(screen.getByText("grant-42")).toBeTruthy();
  });

  it("labels a schedule-fired step 'Scheduled by' its workstream (FE-21, ADR-0063)", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-5", agent: "workstream:ws-1", resourceId: "r-1", tool: "remote.exec", command: "echo tick", status: "pending_approval", runId: "run-ws", stepIndex: 0, principal: "operator:admin", grantId: "grant-77" },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/scheduled by/i)).toBeTruthy());
    expect(screen.getAllByText("workstream:ws-1").length).toBeGreaterThan(0);
    // The FE-18 delegated context is unchanged beside it.
    expect(screen.getByText(/on behalf of/i)).toBeTruthy();
    expect(screen.getByText("grant-77")).toBeTruthy();
  });

  it("does not label an interactive agent's step as scheduled (FE-21 is additive)", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-6", agent: "agent:bot-1", resourceId: "r-1", tool: "remote.exec", command: "echo del", status: "pending_approval", runId: "run-a", stepIndex: 0, principal: "operator:admin", grantId: "grant-1" },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/part of run/i)).toBeTruthy());
    expect(screen.queryByText(/scheduled by/i)).toBeNull();
  });

  it("omits the delegated context for an operator-direct run step", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          { id: "inv-4", agent: "operator:admin", resourceId: "r-1", tool: "remote.exec", command: "echo op", status: "pending_approval", runId: "run-op", stepIndex: 0 },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/part of run/i)).toBeTruthy());
    expect(screen.queryByText(/on behalf of/i)).toBeNull();
    expect(screen.queryByText(/· grant/i)).toBeNull();
  });

  it("omits the Run context for a non-run agent invocation", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue(onePending as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.queryByText(/part of run/i)).toBeNull();
  });

  it("denies an invocation via POST /v1/invocations/{id}/resolve and refetches", async () => {
    vi.mocked(apiClient.GET)
      .mockResolvedValueOnce(onePending as never)
      .mockResolvedValueOnce({ data: { invocations: [] }, error: undefined } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { status: "resolved" }, error: undefined } as never);

    renderInvocations();
    await waitFor(() => expect(screen.getByRole("button", { name: /deny/i })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: /deny/i }));

    await waitFor(() => expect(screen.getByText(/no tool invocations are awaiting approval/i)).toBeTruthy());
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/invocations/{id}/resolve", {
      params: { path: { id: "inv-1" } },
      body: { decision: "deny" },
    });
  });

  it("shows an error when listing fails", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({ data: undefined, error: { code: "bad_gateway", message: "down" } } as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });

  // T-143 (FE-43/FE-44): the description must be accurate per tool, not the
  // hardcoded "run a command" sentence — that sentence is now WRONG for a file
  // invocation. Also proves FE-44: a file write renders its verbatim §D3
  // descriptor (path/size/digest/mode) without leaking content, and is visually
  // distinguishable as mutating.
  it("describes a remote.file.write invocation accurately and marks it as mutating (FE-43/44)", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: {
        invocations: [
          {
            id: "inv-7",
            agent: "agent:smoke",
            resourceId: "r-1",
            tool: "remote.file.write",
            command: "remote.file.write /etc/nginx/nginx.conf (4218 bytes, sha256:9f2c00000000000000000000000000000000000000000000000000000a41b, mode 0644)",
            status: "pending_approval",
          },
        ],
      },
      error: undefined,
    } as never);
    renderInvocations();

    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.getByText(/requesting to write a file/i)).toBeTruthy();
    expect(screen.queryByText(/requesting to run a command/i)).toBeNull(); // the fixed falsehood
    expect(screen.getByRole("status").textContent).toMatch(/mutates target host/i);
    expect(screen.getByLabelText("file descriptor").textContent).toBe(
      "remote.file.write /etc/nginx/nginx.conf (4218 bytes, sha256:9f2c00000000000000000000000000000000000000000000000000000a41b, mode 0644)",
    );
    expect(screen.queryByLabelText("command")).toBeNull();
  });

  it("still describes a remote.exec invocation as running a command, unregressed", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue(onePending as never);
    renderInvocations();
    await waitFor(() => expect(screen.getByText(/agent:smoke/)).toBeTruthy());
    expect(screen.getByText(/requesting to run a command on a governed resource/i)).toBeTruthy();
    expect(screen.queryByRole("status")).toBeNull();
  });
});
