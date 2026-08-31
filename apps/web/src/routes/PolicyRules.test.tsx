// Tests for the PolicyRules route (T-152; FE-24; FE-25 `block`). Mocks the
// module-level apiClient (the Grants/Runs pattern). jsdom-level only — the
// browser-rendered DoD evidence is captured separately (task smoke, per the T-152 brief;
// CLAUDE.md DoD: jsdom unit tests do not satisfy it on their own).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn(), PATCH: vi.fn(), DELETE: vi.fn() },
}));

import { PolicyRules, describeBlastRadius } from "./PolicyRules";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// GET /v1/policy-rules returns a bare PolicyRule[] — no pagination envelope (CON-2).
const sampleRule = {
  id: "rule-1",
  label: "block prod-db writes",
  subject: "agent:bot-1",
  edge: "agent-remote-session",
  action: "remote.exec",
  target: "res-1",
  outcome: "deny",
  enabled: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

function mockGET(rules: unknown, err?: unknown) {
  vi.mocked(apiClient.GET).mockResolvedValue({ data: rules, error: err } as never);
}

function fillMatchFields(subject: string, edge: string, action: string, target: string) {
  fireEvent.change(screen.getByLabelText("Subject"), { target: { value: subject } });
  fireEvent.change(screen.getByLabelText("Edge"), { target: { value: edge } });
  fireEvent.change(screen.getByLabelText("Action"), { target: { value: action } });
  fireEvent.change(screen.getByLabelText("Target"), { target: { value: target } });
}

function renderRoute() {
  return render(
    <MemoryRouter initialEntries={["/policy-rules"]}>
      <Routes>
        <Route path="/policy-rules" element={<PolicyRules />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("PolicyRules route", () => {
  beforeEach(() => vi.clearAllMocks());

  it("lists rules from GET /v1/policy-rules", async () => {
    mockGET([sampleRule]);
    renderRoute();

    await waitFor(() => expect(screen.getByText("block prod-db writes")).toBeTruthy());
    // "deny" also names an <option> in the outcome select above, so scope to the list.
    const list = within(screen.getByRole("list"));
    expect(list.getByText("deny")).toBeTruthy();
    expect(list.getByText("enabled")).toBeTruthy();
    expect(apiClient.GET).toHaveBeenCalledWith("/v1/policy-rules", {});
  });

  it("shows an error when listing fails", async () => {
    mockGET(undefined, { code: "internal", message: "boom" });
    renderRoute();

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not load/i));
  });

  it("creates a rule via POST /v1/policy-rules", async () => {
    mockGET([]);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: sampleRule, error: undefined } as never);
    renderRoute();
    await waitFor(() => expect(screen.getByLabelText("label")).toBeTruthy());

    fireEvent.change(screen.getByLabelText("label"), { target: { value: "block prod-db writes" } });
    fillMatchFields("agent:bot-1", "agent-remote-session", "remote.exec", "res-1");
    fireEvent.change(screen.getByLabelText("outcome"), { target: { value: "deny" } });
    fireEvent.click(screen.getByRole("button", { name: /create rule/i }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith(
        "/v1/policy-rules",
        expect.objectContaining({
          body: {
            label: "block prod-db writes",
            subject: "agent:bot-1",
            edge: "agent-remote-session",
            action: "remote.exec",
            target: "res-1",
            outcome: "deny",
          },
        }),
      ),
    );
  });

  it("shows an error when create fails", async () => {
    mockGET([]);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "conflict", message: "a rule already exists for this tuple" },
    } as never);
    renderRoute();
    await waitFor(() => expect(screen.getByLabelText("label")).toBeTruthy());

    fireEvent.change(screen.getByLabelText("label"), { target: { value: "dup" } });
    fillMatchFields("agent:bot-1", "agent-remote-session", "remote.exec", "res-1");
    fireEvent.click(screen.getByRole("button", { name: /create rule/i }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not create/i));
  });

  it("disables a rule via PATCH /v1/policy-rules/{id}", async () => {
    mockGET([sampleRule]);
    vi.mocked(apiClient.PATCH).mockResolvedValue({ data: { ...sampleRule, enabled: false }, error: undefined } as never);
    renderRoute();
    await waitFor(() => expect(screen.getByText("block prod-db writes")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^disable$/i }));

    await waitFor(() =>
      expect(apiClient.PATCH).toHaveBeenCalledWith("/v1/policy-rules/{id}", {
        params: { path: { id: "rule-1" } },
        body: { enabled: false },
      }),
    );
  });

  it("deletes a rule via DELETE /v1/policy-rules/{id}", async () => {
    mockGET([sampleRule]);
    vi.mocked(apiClient.DELETE).mockResolvedValue({ data: undefined, error: undefined } as never);
    renderRoute();
    await waitFor(() => expect(screen.getByText("block prod-db writes")).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: /^delete$/i }));

    await waitFor(() =>
      expect(apiClient.DELETE).toHaveBeenCalledWith("/v1/policy-rules/{id}", { params: { path: { id: "rule-1" } } }),
    );
  });

  // FE-25 (block criterion) — the mutation-provable assertion: the blast-radius text
  // renders inline for a wildcard-bearing rule and is ABSENT for a fully-specific one,
  // continuously as the form is filled in, never gated behind submit.
  describe("FE-25 blast radius", () => {
    it("renders inline the moment a field is wildcarded", async () => {
      mockGET([]);
      renderRoute();
      await waitFor(() => expect(screen.getByLabelText("Subject")).toBeTruthy());

      fillMatchFields("*", "agent-remote-session", "remote.exec", "res-1");

      expect(screen.getByRole("note").textContent).toMatch(/every subject/i);
    });

    it("states the severity-beats-specificity consequence for a wildcard-bearing deny", async () => {
      mockGET([]);
      renderRoute();
      await waitFor(() => expect(screen.getByLabelText("Target")).toBeTruthy());

      fillMatchFields("agent:bot-1", "agent-remote-session", "remote.exec", "*");
      fireEvent.change(screen.getByLabelText("outcome"), { target: { value: "deny" } });

      const text = screen.getByRole("note").textContent ?? "";
      expect(text).toMatch(/every target/i);
      expect(text).toMatch(/severity beats specificity/i);
      expect(text).toMatch(/no more-specific allow rule/i);
    });

    it("does NOT render for a fully-specific rule", async () => {
      mockGET([]);
      renderRoute();
      await waitFor(() => expect(screen.getByLabelText("Subject")).toBeTruthy());

      fillMatchFields("agent:bot-1", "agent-remote-session", "remote.exec", "res-1");

      expect(screen.queryByRole("note")).toBeNull();
    });
  });
});

// Unit coverage for the pure helper, independent of rendering — the mutation proof
// targets this function's presence directly (see the T-152 report for the named
// firing assertion).
describe("describeBlastRadius", () => {
  it("returns null when no field is wildcarded", () => {
    expect(describeBlastRadius("a", "b", "c", "d", "allow")).toBeNull();
  });

  it("names every wildcarded field and leaves specific fields alone", () => {
    const text = describeBlastRadius("*", "edge-x", "*", "target-x", "allow");
    expect(text).toMatch(/every subject/i);
    expect(text).toMatch(/every action/i);
    expect(text).toMatch(/edge-x/);
    expect(text).toMatch(/target-x/);
    expect(text).not.toMatch(/every edge/i);
    expect(text).not.toMatch(/every target/i);
  });

  it("adds the severity warning only when the outcome is deny", () => {
    const allowText = describeBlastRadius("*", "e", "a", "t", "allow");
    const denyText = describeBlastRadius("*", "e", "a", "t", "deny");
    expect(allowText).not.toMatch(/severity beats specificity/i);
    expect(denyText).toMatch(/severity beats specificity/i);
  });
});
