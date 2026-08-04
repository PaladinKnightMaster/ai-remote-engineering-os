// Tests for the Pair route (T-115b; ADR-0068 §D6, GOV-13). jsdom-level only —
// the browser-rendered DoD evidence is the mobile leg of
// tests/smoke/pairing.mobile.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests
// do not satisfy it). This file targets what a browser test structurally
// can't assert as cleanly: the fragment is read once and stripped
// immediately (GOV-13 hygiene), a malformed fragment is refused the same as
// a wrong secret (no oracle), and the secret is never rendered anywhere —
// including in the DOM after a failed redeem.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { CapabilityProvider } from "@aireos/ui";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Pair } from "./Pair";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/pair");
});

function renderPair() {
  return render(
    <CapabilityProvider fetchCapability={() => Promise.resolve(null)} initialValue={null}>
      <MemoryRouter initialEntries={["/pair"]}>
        <Routes>
          <Route path="/pair" element={<Pair />} />
          <Route path="/invocations" element={<div data-testid="approval-surface">approval surface</div>} />
        </Routes>
      </MemoryRouter>
    </CapabilityProvider>,
  );
}

describe("Pair route — fragment handling (GOV-13)", () => {
  it("strips the fragment from the address bar immediately on mount", () => {
    window.history.pushState(null, "", "/pair#req-1.s3cr3t-value");
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { expiresAt: "2026-01-01T08:00:00Z" }, error: undefined } as never);

    renderPair();

    expect(window.location.hash).toBe("");
  });

  it("posts the parsed id + secret to the redeem endpoint, never leaking the secret elsewhere", async () => {
    window.history.pushState(null, "", "/pair#req-1.s3cr3t-value");
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { expiresAt: "2026-01-01T08:00:00Z" }, error: undefined } as never);

    renderPair();

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/pairing-requests/{id}/redeem", {
        params: { path: { id: "req-1" } },
        body: { secret: "s3cr3t-value" },
      }),
    );
    // Never rendered, never in a query string, never in a path segment.
    expect(document.body.textContent).not.toContain("s3cr3t-value");
    expect(window.location.href).not.toContain("s3cr3t-value");
  });

  it("routes to the approval surface on a successful redeem", async () => {
    window.history.pushState(null, "", "/pair#req-1.s3cr3t-value");
    vi.mocked(apiClient.POST).mockResolvedValue({ data: { expiresAt: "2026-01-01T08:00:00Z" }, error: undefined } as never);

    renderPair();

    await waitFor(() => expect(screen.getByTestId("approval-surface")).toBeTruthy());
  });

  it("a malformed fragment (no secret half) is refused WITHOUT calling redeem — no oracle", async () => {
    window.history.pushState(null, "", "/pair#req-1-only");

    renderPair();

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("a missing fragment entirely is refused the same uniform way", async () => {
    window.history.pushState(null, "", "/pair");

    renderPair();

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("a rejected redeem (wrong/expired/consumed secret, all collapsed server-side) shows ONE uniform message", async () => {
    window.history.pushState(null, "", "/pair#req-1.wrong-secret");
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "not_found", message: "pairing request not found or expired" },
    } as never);

    renderPair();

    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    expect(screen.getByRole("alert").textContent).toMatch(/invalid or has expired/i);
    expect(document.body.textContent).not.toContain("wrong-secret");
  });
});
