// Tests for the Dashboard route's scope-aware shell (T-115b; ADR-0068 §D5,
// §3 of the ticket). A live "approvals@1" paired session must not be offered
// navigation it cannot use (Sessions-as-terminal, Runs, Grants, Workstreams,
// Memories, the desk /pairing surface) — only the approval surface
// (Invocations) and the audit read surface remain. The desktop Dashboard
// must render byte-identical to before this ticket (no scope at all).

import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { CapabilityProvider, type Capability } from "@aireos/ui";

import { Dashboard } from "./Dashboard";

afterEach(() => cleanup());

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
});
