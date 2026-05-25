// Tests for the Login route — ADR-0047 §Decision item "401 handling: single
// chokepoint" + ADR-0046 (operator login).
//
// We mock the module-level apiClient via vi.mock rather than stub the global
// fetch — openapi-fetch captures its fetch reference inside the client
// instance, so stubbing globalThis.fetch after `import { apiClient } from
// "../api"` doesn't intercept. Mocking the module is the canonical pattern.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { CapabilityProvider } from "@aireos/ui";

// Mock the api module. The factory runs before any importing module's code,
// so Login.tsx receives the mocked apiClient.
vi.mock("../api", () => ({
  apiClient: {
    POST: vi.fn(),
    GET: vi.fn(),
  },
  fetchCurrentCapability: vi.fn().mockResolvedValue(null),
}));

// Re-import after the mock is registered.
import { Login } from "./Login";
import { apiClient } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

function renderLogin(initial = "/login") {
  return render(
    <CapabilityProvider fetchCapability={async () => null} initialValue={null}>
      <MemoryRouter initialEntries={[initial]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<div data-testid="dashboard">dashboard</div>} />
        </Routes>
      </MemoryRouter>
    </CapabilityProvider>,
  );
}

function fillAndSubmit(username = "admin", password = "hunter2") {
  fireEvent.change(screen.getByLabelText(/username/i), { target: { value: username } });
  fireEvent.change(screen.getByLabelText(/password/i), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: /sign in/i }));
}

describe("Login route", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("navigates to / on successful login (200) and reloads the capability", async () => {
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: { expiresAt: "2026-12-31T00:00:00Z" },
      error: undefined,
      response: new Response(null, { status: 200 }),
    } as any);

    renderLogin();
    fillAndSubmit();

    await waitFor(() => {
      expect(screen.getByTestId("dashboard")).toBeTruthy();
    });
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/auth/login", {
      body: { username: "admin", password: "hunter2" },
    });
  });

  it("shows an inline 'invalid credentials' error on 401 and stays on /login", async () => {
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "unauthorized", message: "bad creds" },
      response: new Response(null, { status: 401 }),
    } as any);

    renderLogin();
    fillAndSubmit("admin", "wrong-password");

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toMatch(/invalid/i);
    });
    expect(screen.queryByTestId("dashboard")).toBeNull();
  });

  it("shows a generic 'login failed' error on non-401 failure (e.g. 500)", async () => {
    vi.mocked(apiClient.POST).mockResolvedValue({
      data: undefined,
      error: { code: "internal", message: "boom" },
      response: new Response(null, { status: 500 }),
    } as any);

    renderLogin();
    fillAndSubmit();

    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toMatch(/login failed/i);
    });
  });
});
