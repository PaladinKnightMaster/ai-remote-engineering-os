// Quickstart browser gate — the credential panel on the demo target, for real (T-176d; ADR-0079 D2/D5, FRX-4).
//
// Runs between spec A (the demo target exists, bound) and spec B, against the same stack:
//   1. a one-step governed Run on the demo target completes (the generated key works);
//   2. the resource's credential panel shows the PUBLIC key, fingerprint and the exact restricted authorized_keys line;
//   3. Rotate asks for a confirmation that names the consequence; Cancel changes nothing; Confirm shows a NEW line;
//   4. THE CONSEQUENCE, FOR REAL: the next Run FAILS, because the target still trusts only the old key. The orchestrator then
//      installs the new line on the target as the operator would, and spec B shows the same Run succeeding again.
// The old and new lines go to QUICKSTART_LINES_FILE (JSON) for the orchestrator. Screenshots go to QUICKSTART_EVIDENCE_DIR.

import fs from "node:fs";
import { test, expect, type Page } from "@playwright/test";

const OPERATOR_USER = process.env.QUICKSTART_OPERATOR_USER ?? "admin";
const OPERATOR_PASS = process.env.QUICKSTART_OPERATOR_PASS ?? "demo-operator-pw";
const RESOURCE_ID = process.env.QUICKSTART_RESOURCE_ID;
const LINES_FILE = process.env.QUICKSTART_LINES_FILE ?? "/smoke/.quickstart-lines.json";
const EVIDENCE = process.env.QUICKSTART_EVIDENCE_DIR;
const RESOURCE_LABEL = "loopback-demo (ssh)"; // "{name} ({type})", Runs.tsx
const RESOURCE_NAME = "loopback-demo";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

// Launch a one-step Run (whoami) on the demo target from the cockpit and return the steps list.
async function launchRun(page: Page) {
  await page.goto("/runs");
  await page.getByLabel("resource").selectOption({ label: RESOURCE_LABEL });
  await page.getByLabel("command").fill("whoami");
  await page.getByRole("button", { name: "Add step" }).click();
  await page.getByRole("button", { name: /review & launch/i }).click();
  await expect(page.getByRole("region", { name: "launch review" })).toBeVisible();
  const launched = page.waitForResponse((r) => r.url().endsWith("/v1/runs") && r.request().method() === "POST");
  await page.getByRole("button", { name: /confirm launch/i }).click();
  expect((await launched).ok(), "POST /v1/runs").toBeTruthy();
  return page.getByRole("list", { name: "run steps" });
}

test("credential panel: view, rotate with a confirmation, and the next Run fails until the new line is installed", async ({ page }, testInfo) => {
  test.skip(!RESOURCE_ID, "QUICKSTART_RESOURCE_ID is required (spec A's created resource)");
  const shot = async (name: string) => {
    if (EVIDENCE) await page.screenshot({ path: `${EVIDENCE}/${name}.png`, fullPage: true });
    await testInfo.attach(name, { body: await page.screenshot(), contentType: "image/png" });
  };
  await login(page);

  // 1. The generated key works: a governed Run completes.
  await expect(await launchRun(page)).toContainText("completed", { timeout: 180_000 });
  await shot("03-run-completes-before-rotation");

  // 2. The panel: public material only.
  await page.goto("/sessions");
  await page.getByRole("button", { name: `Key for ${RESOURCE_NAME}` }).click();
  const panel = page.getByRole("region", { name: `credential for ${RESOURCE_NAME}` });
  const line = panel.getByLabel("authorized_keys line");
  await expect(line).toBeVisible();
  const oldLine = ((await line.textContent()) ?? "").trim();
  expect(oldLine).toMatch(/^restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 [A-Za-z0-9+/=]+$/);
  await expect(panel).toContainText("SHA256:");
  await expect(panel).toContainText("Add this line to the target account's ~/.ssh/authorized_keys.");
  // D5: nothing on this page accepts a private key, and nothing on it carries one.
  await expect(page.locator("input[type=file], textarea")).toHaveCount(0);
  expect(await page.content()).not.toMatch(/PRIVATE KEY/);
  await shot("04-credential-panel");

  // 3. Rotate: the confirmation names the consequence; Cancel changes nothing.
  await panel.getByRole("button", { name: "Rotate key" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("Until you install the new line on the target, the cockpit will not be able to connect to it.");
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await shot("05-rotate-confirmation");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  expect(((await line.textContent()) ?? "").trim()).toBe(oldLine);

  await panel.getByRole("button", { name: "Rotate key" }).click();
  const rotated = page.waitForResponse((r) => r.url().endsWith(`/v1/resources/${RESOURCE_ID}/credential`) && r.request().method() === "POST");
  await dialog.getByRole("button", { name: "Rotate and replace the key" }).click();
  const rotResp = await rotated;
  expect(rotResp.status(), "POST /credential {rotate:true}").toBe(200);
  expect(JSON.parse(rotResp.request().postData() ?? "{}")).toEqual({ rotate: true });
  await expect(panel).toContainText("old key no longer works");
  const newLine = ((await line.textContent()) ?? "").trim();
  expect(newLine).not.toBe(oldLine);
  expect(newLine).toMatch(/^restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 /);
  await shot("06-credential-rotated-new-line");
  fs.writeFileSync(LINES_FILE, JSON.stringify({ oldLine, newLine }), "utf8");

  // 4. The consequence, for real: the target still trusts only the old key, so the next Run fails.
  await expect(await launchRun(page)).toContainText("failed", { timeout: 180_000 });
  await shot("07-run-fails-after-rotation");
});
