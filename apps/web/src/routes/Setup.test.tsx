// Tests for first-run setup — ADR-0078 (FRX-1/2). Mocks the module-level apiClient like Login.test.tsx.

import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Navigate, Route, Routes } from "react-router-dom";
import { CapabilityProvider } from "@aireos/ui";

vi.mock("../api", () => ({
  apiClient: { POST: vi.fn(), GET: vi.fn() },
  fetchSetupRequired: vi.fn(),
  fetchCurrentCapability: vi.fn().mockResolvedValue(null),
}));

import { SetupGate } from "./Setup";
import { apiClient, fetchSetupRequired } from "../api";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

// The same shape as App.tsx: the gate around the router, "/login" and "*" as real routes.
function renderApp(initial: string) {
  return render(
    <CapabilityProvider fetchCapability={async () => null} initialValue={null}>
      <MemoryRouter initialEntries={[initial]}>
        <SetupGate>
          <Routes>
            <Route path="/login" element={<div data-testid="login">login</div>} />
            <Route path="/" element={<div data-testid="dashboard">dashboard</div>} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </SetupGate>
      </MemoryRouter>
    </CapabilityProvider>,
  );
}

const form = () => screen.findByRole("form", { name: "first-run setup" });

function fill(over: Partial<Record<"code" | "username" | "password" | "confirm", string>> = {}) {
  const v = { code: "abcde-fghjk-mnpqr-stvwx", username: "admin", password: "correct horse battery", confirm: "correct horse battery", ...over };
  fireEvent.change(screen.getByLabelText("Setup code"), { target: { value: v.code } });
  fireEvent.change(screen.getByLabelText("Username"), { target: { value: v.username } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: v.password } });
  fireEvent.change(screen.getByLabelText("Confirm password"), { target: { value: v.confirm } });
  fireEvent.click(screen.getByRole("button", { name: /create operator account/i }));
}

function answer(status: number, error?: { code: string; message: string }) {
  vi.mocked(apiClient.POST).mockResolvedValue(
    (status === 200
      ? { data: { expiresAt: "2026-12-31T00:00:00Z" }, error: undefined, response: new Response(null, { status }) }
      : { data: undefined, error, response: new Response(null, { status }) }) as any,
  );
}

describe("SetupGate (the routing switch)", () => {
  it("setup open: every route is the setup page, and the dashboard is not rendered", async () => {
    vi.mocked(fetchSetupRequired).mockResolvedValue(true);
    renderApp("/");
    expect(await form()).toBeTruthy();
    expect(screen.queryByTestId("dashboard")).toBeNull();
  });

  it("setup open: /login redirects to the setup page", async () => {
    vi.mocked(fetchSetupRequired).mockResolvedValue(true);
    renderApp("/login");
    expect(await form()).toBeTruthy();
    expect(screen.queryByTestId("login")).toBeNull();
  });

  it("setup closed: the normal router runs, and /setup is no route — it falls through to /", async () => {
    vi.mocked(fetchSetupRequired).mockResolvedValue(false);
    renderApp("/setup");
    expect(await screen.findByTestId("dashboard")).toBeTruthy();
    expect(screen.queryByRole("form", { name: "first-run setup" })).toBeNull();
  });

  it("renders nothing until the server has answered (no flash of the login page before setup)", () => {
    vi.mocked(fetchSetupRequired).mockReturnValue(new Promise(() => {}));
    renderApp("/");
    expect(screen.queryByTestId("dashboard")).toBeNull();
    expect(screen.queryByRole("form")).toBeNull();
  });
});

describe("Setup form", () => {
  async function open() {
    vi.mocked(fetchSetupRequired).mockResolvedValue(true);
    renderApp("/");
    await form();
  }

  it("has labelled fields, states the 12-character minimum and the fingerprint check", async () => {
    await open();
    for (const l of ["Setup code", "Username", "Password", "Confirm password"]) expect(screen.getByLabelText(l)).toBeTruthy();
    expect(screen.getByText(/at least 12 characters/i)).toBeTruthy();
    expect(screen.getByText(/certificate\s+fingerprint/i)).toBeTruthy();
  });

  it("posts code, username and password in the body and lands on the dashboard", async () => {
    await open();
    answer(200);
    fill();
    expect(await screen.findByTestId("dashboard")).toBeTruthy();
    expect(apiClient.POST).toHaveBeenCalledWith("/v1/setup/complete", {
      body: { code: "abcde-fghjk-mnpqr-stvwx", username: "admin", password: "correct horse battery" },
    });
    expect(screen.queryByRole("form", { name: "first-run setup" })).toBeNull();
  });

  it("refuses mismatched passwords and short passwords without calling the server", async () => {
    await open();
    fill({ confirm: "something else entirely" });
    expect((await screen.findByRole("alert")).textContent).toMatch(/don't match/i);
    fill({ password: "short", confirm: "short" });
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/at least 12 characters/i));
    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("shows the server's own words for an invalid or expired code, and stays on the form", async () => {
    await open();
    answer(403, { code: "invalid_setup_code", message: "the setup code is invalid or has expired; run `setup.sh --new-setup-code` on the server for a new one" });
    fill();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/setup\.sh --new-setup-code/));
    expect(screen.queryByTestId("dashboard")).toBeNull();
  });

  it("shows the reason the server gives for a refused username or password", async () => {
    await open();
    answer(400, { code: "bad_request", message: "password is too common" });
    fill();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("password is too common"));
  });

  it("says plainly that setup is already complete on a 404", async () => {
    await open();
    answer(404);
    fill();
    await waitFor(() => expect(screen.getByRole("alert").textContent).toMatch(/already complete/i));
  });
});
