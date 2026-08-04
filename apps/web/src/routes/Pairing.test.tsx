// Tests for the Pairing route (T-115a). Mocks the module-level apiClient (the
// Grants/Runs pattern) — jsdom-level only. The browser-rendered DoD evidence
// is tests/smoke/pairing.smoke.spec.ts (CLAUDE.md DoD: jsdom unit tests do not
// satisfy it), which is why coverage here is deliberately narrow: it targets
// the three things the smoke suite structurally cannot reach —
//   1. the SEC-41 "disabled" branch: task smoke always runs with
//      AIREOS_PAIRING_ENABLED=1, so the live-404 degrade path is never
//      exercised by any browser test;
//   2. a failed create (error paths in general are smoke's weak spot — it
//      proves the happy path in a real browser, which is stronger evidence
//      for THAT path, but can't easily drive a failing request); and
//   3. the one-shot secret property (never re-rendered after dismiss) that
//      the route's own file comment promises but nothing previously asserted.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { Pairing } from "./Pairing";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderPairing() {
  return render(
    <MemoryRouter initialEntries={["/pairing"]}>
      <Routes>
        <Route path="/pairing" element={<Pairing />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Pairing route", () => {
  it("SEC-41: a live 404 from GET /v1/paired-sessions degrades to 'not available' — no create affordance, by absence not CSS", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: undefined,
      error: { code: "not_found", message: "not found" },
      response: new Response(null, { status: 404 }),
    } as never);

    renderPairing();

    await waitFor(() =>
      expect(screen.getByText(/not enabled on this deployment/i)).toBeTruthy(),
    );
    expect(screen.queryByLabelText("label")).toBeNull();
    expect(screen.queryByRole("button", { name: /create pairing code/i })).toBeNull();
    expect(screen.queryByRole("img")).toBeNull();
  });

  it("surfaces an error banner on a failed create, and renders no QR or secret", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [], page: {} },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "internal", message: "boom" },
    } as never);

    renderPairing();
    await waitFor(() => expect(screen.getByLabelText("label")).toBeTruthy());

    fireEvent.change(screen.getByLabelText("label"), { target: { value: "my phone" } });
    fireEvent.click(screen.getByRole("button", { name: /create pairing code/i }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/could not create/i));
    expect(screen.queryByRole("img")).toBeNull();
    expect(document.querySelector("code")).toBeNull();
  });

  it("shows the secret exactly once — gone after the operator dismisses it (Done)", async () => {
    vi.mocked(apiClient.GET).mockResolvedValue({
      data: { items: [], page: {} },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as never);
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { id: "pr-1", expiresAt: "2026-01-01T00:02:00Z", secret: "s3cr3t" },
      error: undefined,
    } as never);

    renderPairing();
    await waitFor(() => expect(screen.getByLabelText("label")).toBeTruthy());

    fireEvent.change(screen.getByLabelText("label"), { target: { value: "my phone" } });
    fireEvent.click(screen.getByRole("button", { name: /create pairing code/i }));

    await waitFor(() => expect(screen.getByRole("img")).toBeTruthy());
    expect(document.querySelector("code")?.textContent).toContain("s3cr3t");

    fireEvent.click(screen.getByRole("button", { name: /^done$/i }));

    expect(screen.queryByRole("img")).toBeNull();
    expect(document.querySelector("code")).toBeNull();
  });
});
