// Tests for <ResourceCredentialPanel> (T-176d; ADR-0079 D2/D5, FRX-4). Mocks the module-level apiClient.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

vi.mock("../api", () => ({
  apiClient: { GET: vi.fn(), POST: vi.fn() },
}));

import { ResourceCredentialPanel } from "./ResourceCredentialPanel";
import { apiClient } from "../api";

const OPTS = "restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty";
const cred = (n: string) => ({
  publicKey: `ssh-ed25519 AAAA${n}`,
  fingerprint: `SHA256:fp${n}`,
  authorizedKeysLine: `${OPTS} ssh-ed25519 AAAA${n}`,
});

const getKey = (n: string) => Promise.resolve({ data: cred(n), error: undefined, response: { status: 200 } });
const get404 = () => Promise.resolve({ data: undefined, error: { message: "no public key is recorded for this resource" }, response: { status: 404 } });

beforeEach(() => vi.clearAllMocks());
afterEach(() => cleanup());

function renderPanel() {
  return render(<ResourceCredentialPanel resourceId="r1" resourceName="web-1" />);
}

describe("ResourceCredentialPanel — states", () => {
  it("no credential: offers Generate a key, then shows the public key, fingerprint and the exact line", async () => {
    vi.mocked(apiClient.GET).mockImplementation((() => get404()) as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: cred("1"), error: undefined, response: { status: 200 } } as never);
    renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: "Generate a key" }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/resources/{id}/credential", {
        params: { path: { id: "r1" } },
        body: { rotate: false },
      }),
    );
    expect(await screen.findByText("ssh-ed25519 AAAA1")).toBeTruthy();
    expect(screen.getByText("SHA256:fp1")).toBeTruthy();
    expect(screen.getByLabelText("authorized_keys line").textContent).toBe(`${OPTS} ssh-ed25519 AAAA1`);
    expect(screen.getByText(/authorized_keys/, { selector: "code" })).toBeTruthy();
    expect(screen.getByText((_, el) => el?.tagName === "P" && /Add this line to the target account's/.test(el.textContent ?? ""))).toBeTruthy();
  });

  it("has a generated key: reads it with GET and shows it without generating anything", async () => {
    vi.mocked(apiClient.GET).mockImplementation((() => getKey("2")) as never);
    renderPanel();

    expect(await screen.findByText("SHA256:fp2")).toBeTruthy();
    expect(screen.getByLabelText("authorized_keys line").textContent).toContain("ssh-ed25519 AAAA2");
    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("imported key: Generate's 409 becomes a plain note and no key is invented", async () => {
    vi.mocked(apiClient.GET).mockImplementation((() => get404()) as never);
    vi.mocked(apiClient.POST).mockResolvedValue({ data: undefined, error: { message: "a credential already exists" }, response: { status: 409 } } as never);
    renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: "Generate a key" }));

    expect(await screen.findByText(/imported on the server/)).toBeTruthy();
    expect(screen.queryByLabelText("authorized_keys line")).toBeNull();
    expect(screen.queryByRole("button", { name: "Rotate key" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Generate a key" })).toBeNull();
  });

  it("any other read failure shows the server's message", async () => {
    vi.mocked(apiClient.GET).mockImplementation((() =>
      Promise.resolve({ data: undefined, error: { message: "connector-service is unavailable or answered unusably" }, response: { status: 502 } })) as never);
    renderPanel();

    expect((await screen.findByRole("alert")).textContent).toBe("connector-service is unavailable or answered unusably");
  });
});

describe("ResourceCredentialPanel — rotate needs a confirmation that names the consequence", () => {
  beforeEach(() => {
    vi.mocked(apiClient.GET).mockImplementation((() => getKey("1")) as never);
  });

  it("opens an alertdialog naming the consequence, with focus on Cancel; Cancel sends nothing and returns focus", async () => {
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Rotate key" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog.textContent).toContain("Rotating replaces this key. Until you install the new line on the target, the cockpit will not be able to connect to it.");
    expect(dialog.getAttribute("aria-labelledby")).toBeTruthy();
    expect(dialog.getAttribute("aria-describedby")).toBeTruthy();
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "Cancel" })));
    expect(apiClient.POST).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(apiClient.POST).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Rotate key" }));
  });

  it("Escape cancels without sending anything", async () => {
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Rotate key" }));
    fireEvent.keyDown(await screen.findByRole("alertdialog"), { key: "Escape" });

    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(apiClient.POST).not.toHaveBeenCalled();
  });

  it("Confirm sends {rotate:true}, shows the NEW line and says the old one stopped working", async () => {
    vi.mocked(apiClient.POST).mockResolvedValue({ data: cred("9"), error: undefined, response: { status: 200 } } as never);
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Rotate key" }));
    fireEvent.click(await screen.findByRole("button", { name: "Rotate and replace the key" }));

    await waitFor(() =>
      expect(apiClient.POST).toHaveBeenCalledWith("/v1/resources/{id}/credential", {
        params: { path: { id: "r1" } },
        body: { rotate: true },
      }),
    );
    await waitFor(() => expect(screen.getByLabelText("authorized_keys line").textContent).toContain("AAAA9"));
    expect(screen.queryByRole("alertdialog")).toBeNull();
    expect(screen.getByText(/old key no longer works/)).toBeTruthy();
  });

  it("a failed rotation says so and keeps showing the key that is still in force", async () => {
    vi.mocked(apiClient.POST).mockResolvedValue({ data: undefined, error: { message: "connector-service is unavailable or answered unusably" }, response: { status: 502 } } as never);
    renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "Rotate key" }));
    fireEvent.click(await screen.findByRole("button", { name: "Rotate and replace the key" }));

    expect(await screen.findByText("connector-service is unavailable or answered unusably")).toBeTruthy();
    expect(screen.getByLabelText("authorized_keys line").textContent).toContain("AAAA1");
  });
});

describe("ResourceCredentialPanel — copy and D5", () => {
  it("Copy line writes exactly the authorized_keys line to the clipboard", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.mocked(apiClient.GET).mockImplementation((() => getKey("1")) as never);
    renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: "Copy line" }));

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(`${OPTS} ssh-ed25519 AAAA1`));
    expect(await screen.findByText("Copied.")).toBeTruthy();
  });

  it("a clipboard failure says so instead of pretending", async () => {
    Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) }, configurable: true });
    vi.mocked(apiClient.GET).mockImplementation((() => getKey("1")) as never);
    renderPanel();

    fireEvent.click(await screen.findByRole("button", { name: "Copy line" }));

    expect(await screen.findByText(/Copy failed/)).toBeTruthy();
  });

  it("D5: in every state the panel has no input, textarea or file picker that could accept a private key", async () => {
    const seen: string[] = [];
    for (const state of ["key", "none", "imported"] as const) {
      vi.clearAllMocks();
      vi.mocked(apiClient.GET).mockImplementation((() => (state === "key" ? getKey("1") : get404())) as never);
      vi.mocked(apiClient.POST).mockResolvedValue({ data: undefined, error: {}, response: { status: 409 } } as never);
      const { container, unmount } = renderPanel();
      if (state === "key") await screen.findByLabelText("authorized_keys line");
      else await screen.findByRole("button", { name: "Generate a key" });
      if (state === "imported") {
        fireEvent.click(screen.getByRole("button", { name: "Generate a key" }));
        await screen.findByText(/imported on the server/);
      }
      if (state === "key") {
        fireEvent.click(screen.getByRole("button", { name: "Rotate key" }));
        await screen.findByRole("alertdialog");
      }
      seen.push(`${state}:${container.querySelectorAll("input, textarea, select, [contenteditable], [type=file]").length}`);
      expect(container.textContent ?? "").not.toMatch(/PRIVATE KEY/);
      unmount();
    }
    expect(seen).toEqual(["key:0", "none:0", "imported:0"]);
  });
});
