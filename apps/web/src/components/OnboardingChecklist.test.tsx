// Tests for <OnboardingChecklist> (T-118a; ADR-0068 §D8, ONB-1/ONB-2). Mocks
// the module-level apiClient (the Grants/Runs pattern). This is where the
// UN-SEEDED first-run state actually gets tested — task smoke runs against a
// seeded backend and can only ever exercise the "both conditions met" case
// (see tests/smoke/onboarding.smoke.spec.ts); jsdom is the only layer that
// can drive "neither condition met" and "one condition met" at all.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn() },
}));

import { OnboardingChecklist } from "./OnboardingChecklist";
import { apiClient } from "../api";

const DISMISSED_KEY = "aireos.onboarding.dismissed";

function mockGET(opts: { resourceExists?: boolean; actionCompleted?: boolean }) {
  vi.mocked(apiClient.GET).mockImplementation(((path: string) => {
    if (path === "/v1/resources") {
      return Promise.resolve({
        data: { items: opts.resourceExists ? [{ id: "r1", name: "res-1" }] : [], page: {} },
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
    mockGET({ resourceExists: false, actionCompleted: false });
    render(<OnboardingChecklist />);

    await waitFor(() => expect(screen.queryByText("Checking current status…")).toBeNull());

    expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("○");
    expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("○");
    expect(screen.queryByText(/all set/i)).toBeNull();
  });

  it("shows only the resource item met when a resource exists but no action has completed", async () => {
    mockGET({ resourceExists: true, actionCompleted: false });
    render(<OnboardingChecklist />);

    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("○");
    expect(screen.queryByText(/all set/i)).toBeNull();
  });

  it("shows both items met once a resource exists and a governed action has completed", async () => {
    mockGET({ resourceExists: true, actionCompleted: true });
    render(<OnboardingChecklist />);

    await waitFor(() => expect(screen.getByText("Register a resource").previousSibling?.textContent).toBe("✓"));
    await waitFor(() => expect(screen.getByText("Complete a governed action").previousSibling?.textContent).toBe("✓"));
    await waitFor(() => expect(screen.getByText(/all set/i)).toBeTruthy());
  });

  it("derives 'completed' from the outcome=tool.result exact-match filter, not a preset or new param", async () => {
    mockGET({ resourceExists: true, actionCompleted: true });
    render(<OnboardingChecklist />);

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
    mockGET({ resourceExists: false, actionCompleted: false });
    render(<OnboardingChecklist />);

    await waitFor(() => expect(screen.getByRole("region", { name: "first-run checklist" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));

    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
    expect(window.localStorage.getItem(DISMISSED_KEY)).toBe("1");
  });

  it("stays hidden across a remount once dismissed (no server round trip involved)", async () => {
    window.localStorage.setItem(DISMISSED_KEY, "1");
    mockGET({ resourceExists: true, actionCompleted: true });

    const { unmount } = render(<OnboardingChecklist />);
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
    unmount();

    render(<OnboardingChecklist />);
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
  });

  it("a dismissed panel does not imply the derived state is complete — completion and visibility are independent", async () => {
    window.localStorage.setItem(DISMISSED_KEY, "1");
    mockGET({ resourceExists: false, actionCompleted: false });

    render(<OnboardingChecklist />);
    // The panel is hidden (dismissed), but nothing here asserts the
    // conditions became true — an un-seeded stack stays un-seeded regardless
    // of dismissal (ONB-2 asymmetry).
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
  });
});
