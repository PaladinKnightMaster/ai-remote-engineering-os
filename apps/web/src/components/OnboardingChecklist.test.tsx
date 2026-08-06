// Tests for <OnboardingChecklist> (T-118a; ADR-0068 §D8, ONB-1/ONB-2). Mocks
// the module-level apiClient (the Grants/Runs pattern). This is where the
// UN-SEEDED first-run state actually gets tested — task smoke runs against a
// seeded backend and can only ever exercise the "both conditions met" case
// (see tests/smoke/onboarding.smoke.spec.ts); jsdom is the only layer that
// can drive "neither condition met" and "one condition met" at all.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() },
}));

import { OnboardingChecklist } from "./OnboardingChecklist";
import { apiClient } from "../api";

const DISMISSED_KEY = "aireos.onboarding.dismissed";

// `created` is mutable so a test can simulate the real sequence: POST
// /v1/resources succeeds, then the NEXT /v1/resources GET (triggered by
// CreateLoopbackDemoTarget's onCreated -> refresh()) reflects it — no
// special-cased "mark done", the same derived-state read T-118a already uses.
// deleteRefusal, when set, simulates the store's 409 in-use refusal (T-118c)
// instead of letting the DELETE succeed — the resource stays `created`.
function mockClient(opts: { resourceExists?: boolean; actionCompleted?: boolean; deleteRefusal?: boolean }) {
  let created = opts.resourceExists ?? false;
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") {
      return Promise.resolve({
        data: { items: created ? [{ id: "r1", name: "loopback-demo" }] : [], page: {} },
        error: undefined,
      });
    }
    if (path === "/v1/audit/events") {
      return Promise.resolve({
        data: {
          items: opts.actionCompleted
            ? [{ id: "e1", timestamp: "t", actor: "a", edge: "human-agent", target: "resource:r1", outcome: "tool.result" }]
            : [],
          page: {},
        },
        error: undefined,
      });
    }
    throw new Error(`unexpected GET ${path}`);
  }) as never);
  vi.mocked(apiClient.POST).mockImplementation(((path: string, init: { body: Record<string, unknown> }) => {
    if (path === "/v1/resources") {
      created = true;
      return Promise.resolve({
        data: { id: "r1", type: "ssh", name: "loopback-demo", labels: init.body.labels },
        error: undefined,
      });
    }
    throw new Error(`unexpected POST ${path}`);
  }) as never);
  vi.mocked(apiClient.DELETE).mockImplementation(((path: string) => {
    if (path === "/v1/resources/{id}") {
      if (opts.deleteRefusal) {
        return Promise.resolve({ data: undefined, error: { message: "resource is in use" } });
      }
      created = false;
      return Promise.resolve({ data: undefined, error: undefined });
    }
    throw new Error(`unexpected DELETE ${path}`);
  }) as never);
}

function renderChecklist() {
  return render(
    <MemoryRouter>
      <OnboardingChecklist />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  window.localStorage.clear();
});

describe("OnboardingChecklist — ONB-1 derived conditions", () => {
  it("shows both items unmet when neither condition holds", async () => {
    mockClient({ resourceExists: false, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.queryByText("Checking current status…")).toBeNull());

    expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("○");
    expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("○");
    expect(screen.queryByText(/all set/i)).toBeNull();
  });

  it("shows only the resource item met when a resource exists but no action has completed", async () => {
    mockClient({ resourceExists: true, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("○");
    expect(screen.queryByText(/all set/i)).toBeNull();
  });

  it("shows both items met once a resource exists and a governed action has completed", async () => {
    mockClient({ resourceExists: true, actionCompleted: true });
    renderChecklist();

    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    await waitFor(() => expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("✓"));
    await waitFor(() => expect(screen.getByText(/all set/i)).toBeTruthy());
  });

  it("derives 'completed' from the outcome=tool.result exact-match filter, not a preset or new param", async () => {
    mockClient({ resourceExists: true, actionCompleted: true });
    renderChecklist();

    await waitFor(() =>
      expect(apiClient.GET).toHaveBeenCalledWith(
        "/v1/audit/events",
        expect.objectContaining({ params: { query: { limit: 1, outcome: "tool.result" } } }),
      ),
    );
  });
});

describe("OnboardingChecklist — ONB-2 per-browser dismissal", () => {
  it("hides the panel and persists to localStorage when Dismiss is clicked", async () => {
    mockClient({ resourceExists: false, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByRole("region", { name: "first-run checklist" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));

    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
    expect(window.localStorage.getItem(DISMISSED_KEY)).toBe("1");
  });

  it("stays hidden across a remount once dismissed (no server round trip involved)", async () => {
    window.localStorage.setItem(DISMISSED_KEY, "1");
    mockClient({ resourceExists: true, actionCompleted: true });

    const { unmount } = renderChecklist();
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
    unmount();

    renderChecklist();
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
  });

  it("a dismissed panel does not imply the derived state is complete — completion and visibility are independent", async () => {
    window.localStorage.setItem(DISMISSED_KEY, "1");
    mockClient({ resourceExists: false, actionCompleted: false });

    renderChecklist();
    // The panel is hidden (dismissed), but nothing here asserts the
    // conditions became true — an un-seeded stack stays un-seeded regardless
    // of dismissal (ONB-2 asymmetry).
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
  });
});

describe("OnboardingChecklist — T-118b loopback demo target create action", () => {
  it("offers the create action only while no resource exists yet", async () => {
    mockClient({ resourceExists: false, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByRole("button", { name: "Create loopback demo target" })).toBeTruthy());
  });

  it("does not offer the create action once a resource already exists", async () => {
    mockClient({ resourceExists: true, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    expect(screen.queryByRole("button", { name: "Create loopback demo target" })).toBeNull();
  });

  it("points at Runs once a resource exists but no action has completed yet", async () => {
    mockClient({ resourceExists: true, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByRole("link", { name: "Runs" })).toBeTruthy());
  });

  it("creates an unflagged ssh resource with the fixed loopback coordinates and the operator's own inputs, then flips the checklist without a remount", async () => {
    mockClient({ resourceExists: false, actionCompleted: false });
    renderChecklist();

    fireEvent.click(await screen.findByRole("button", { name: "Create loopback demo target" }));
    fireEvent.change(screen.getByLabelText("username"), { target: { value: "aireos-demo" } });
    fireEvent.change(screen.getByLabelText("ssh host key"), {
      target: { value: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDBW1UcckoHRnBiFg8uDK8QWN0hpPHzWKFGhwim2P5Cr aireos-loopback-demo" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith(
        "/v1/resources",
        expect.objectContaining({
          body: expect.objectContaining({
            type: "ssh",
            labels: expect.objectContaining({
              ssh_host: "127.0.0.1",
              ssh_port: "22",
              ssh_user: "aireos-demo",
              ssh_host_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDBW1UcckoHRnBiFg8uDK8QWN0hpPHzWKFGhwim2P5Cr aireos-loopback-demo",
            }),
          }),
        }),
      ),
    );
    // Create it unflagged: no requires_approval label at all (see the
    // component's own comment — absent reads as ungated, same as any
    // non-"true" value, but omitting it entirely is the honest way to say
    // "no flag was set" rather than writing a false value).
    const call = vi.mocked(apiClient.POST).mock.calls.find((c) => c[0] === "/v1/resources");
    expect(call).toBeDefined();
    const body = (call![1] as { body: { labels?: Record<string, string> } }).body;
    expect(body.labels).not.toHaveProperty("requires_approval");

    // ONB-1 flips on its own from the next derived-state read (refresh()),
    // not a special case — the create action never sets a "done" flag itself.
    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    expect(screen.queryByRole("form", { name: "create loopback demo target" })).toBeNull();
  });
});

describe("OnboardingChecklist — T-118c loopback demo target remove action (ONB-4)", () => {
  it("does not offer the remove action while no demo resource exists yet", async () => {
    mockClient({ resourceExists: false, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByRole("button", { name: "Create loopback demo target" })).toBeTruthy());
    expect(screen.queryByRole("button", { name: "Remove the demo target" })).toBeNull();
  });

  it("offers the remove action once the demo resource exists", async () => {
    mockClient({ resourceExists: true, actionCompleted: false });
    renderChecklist();

    await waitFor(() => expect(screen.getByRole("button", { name: "Remove the demo target" })).toBeTruthy());
  });

  it("removes the demo target through DELETE /v1/resources/{id} and flips the checklist back without a remount", async () => {
    mockClient({ resourceExists: true, actionCompleted: false });
    renderChecklist();

    fireEvent.click(await screen.findByRole("button", { name: "Remove the demo target" }));

    await waitFor(() =>
      expect(apiClient.DELETE).toHaveBeenCalledWith("/v1/resources/{id}", expect.objectContaining({ params: { path: { id: "r1" } } })),
    );

    // ONB-1 flips back on its own from the next derived-state read — not
    // special-cased, the same as the create action's own flip (T-118b).
    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("○"));
    expect(screen.queryByRole("button", { name: "Remove the demo target" })).toBeNull();
  });

  it("surfaces the 409 in-use refusal honestly instead of pretending the removal worked", async () => {
    mockClient({ resourceExists: true, actionCompleted: false, deleteRefusal: true });
    renderChecklist();

    fireEvent.click(await screen.findByRole("button", { name: "Remove the demo target" }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("Could not remove the demo target: resource is in use"));
    // Refused, so the checklist item stays exactly as it was — still done,
    // the button still offered, nothing silently disappeared.
    expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓");
    expect(screen.getByRole("button", { name: "Remove the demo target" })).toBeTruthy();
  });
});
