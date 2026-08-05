// Tests for the Dashboard route's scope-aware shell (T-115b; ADR-0068 §D5,
// §3 of the ticket). A live "approvals@1" paired session must not be offered
// navigation it cannot use (Sessions-as-terminal, Runs, Grants, Workstreams,
// Memories, the desk /pairing surface) — only the approval surface
// (Invocations) and the audit read surface remain. The desktop Dashboard
// must render byte-identical to before this ticket (no scope at all).
//
// T-118a adds <OnboardingChecklist>, which calls apiClient.GET on mount — the
// module-level apiClient is mocked below (the Grants/Runs pattern) so these
// pre-existing assertions keep running with no real network call. The
// checklist's own derived-state permutations are OnboardingChecklist.test.tsx's
// job; this file only proves the desk-only gating (ADR-0068 §D5).

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { CapabilityProvider, type Capability } from "@aireos/ui";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(() => Promise.resolve({ data: { items: [], page: {} }, error: undefined })) },
}));

import { Dashboard } from "./Dashboard";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderDashboard(capability: Capability | null) {
  return render(
    <CapabilityProvider fetchCapability={() => Promise.resolve(null)} initialValue={capability}>
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/" element={<Dashboard />} />
        </Routes>
      </MemoryRouter>
    </CapabilityProvider>,
  );
}

describe("Dashboard — desktop session (no scope)", () => {
  it("shows every operator-action link", () => {
    renderDashboard({ subject: "operator:admin", edge: "human-agent" });
    for (const name of [
      /sessions/i,
      /pending tool invocations/i,
      /runs/i,
      /delegation grants/i,
      /workstreams/i,
      /memory/i,
      /audit timeline/i,
      /device pairing/i,
    ]) {
      expect(screen.getByRole("link", { name })).toBeTruthy();
    }
    expect(screen.queryByText(/paired device session/i)).toBeNull();
  });

  // T-118a/ADR-0068 §D8: the desk session is the only one that sees the
  // first-run checklist.
  it("renders the first-run checklist", async () => {
    renderDashboard({ subject: "operator:admin", edge: "human-agent" });
    await waitFor(() => expect(screen.getByRole("region", { name: "first-run checklist" })).toBeTruthy());
  });
});

describe("Dashboard — paired session (approvals@1, ADR-0068 §D5)", () => {
  it("shows ONLY Invocations + Audit, hiding every action a paired session cannot use", () => {
    renderDashboard({ subject: "operator:zelda", edge: "human-agent", scope: "approvals@1" });

    expect(screen.getByRole("link", { name: /pending tool invocations/i })).toBeTruthy();
    expect(screen.getByRole("link", { name: /audit timeline/i })).toBeTruthy();

    for (const name of [/^sessions/i, /^runs/i, /delegation grants/i, /workstreams/i, /^memory/i, /device pairing/i]) {
      expect(screen.queryByRole("link", { name })).toBeNull();
    }
    expect(screen.getByText(/paired device session/i)).toBeTruthy();
  });

  // T-118a: a paired phone must never see the onboarding panel — almost
  // everything it points at (registering a resource, running a governed
  // action) is out of scope for this session (ADR-0068 §D5). It must not
  // even mount <OnboardingChecklist> — no fetch fired on its behalf.
  it("never renders the first-run checklist, and never fetches for it", () => {
    renderDashboard({ subject: "operator:zelda", edge: "human-agent", scope: "approvals@1" });
    expect(screen.queryByRole("region", { name: "first-run checklist" })).toBeNull();
    expect(apiClient.GET).not.toHaveBeenCalled();
  });
});
