// Quickstart browser gate — spec A: steps 1 (log in) and 3 (create the loopback demo target) of
// docs/devops/quickstart-first-governed-action.md, in the v0.17.0 form (T-176d; ADR-0079 D4, FRX-5): ONE click, no paste, no
// server command. The stack was installed by setup.sh with AIREOS_LOOPBACK_DEMO=yes (bringup-demo.sh --demo), which prepared the
// demo account and a pending key; the button asks the API to create the resource from that and bind the key.
//
// ONB-4 ("no demo-only code path"): after the click, a `resource.created` audit event targeting the EXACT new resource:<id> must
// exist (the ordinary governed create chains it in the same transaction), and `credential.bound` must follow from the connector.
//
// The new resource's id goes to QUICKSTART_STATE_FILE for the orchestrator and the later specs. Screenshots go to
// QUICKSTART_EVIDENCE_DIR when it is set.

import fs from "node:fs";
import { test, expect, type Page } from "@playwright/test";

const OPERATOR_USER = process.env.QUICKSTART_OPERATOR_USER ?? "admin";
const OPERATOR_PASS = process.env.QUICKSTART_OPERATOR_PASS ?? "demo-operator-pw";
const STATE_FILE = process.env.QUICKSTART_STATE_FILE ?? "/smoke/.quickstart-resource-id";
const EVIDENCE = process.env.QUICKSTART_EVIDENCE_DIR;

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("quickstart steps 1+3: log in and create the loopback demo target with ONE click (FRX-5, ONB-4)", async ({ page }, testInfo) => {
  // Only the quickstart gate (quickstart-browser-gate.sh) has a stack with a PREPARED demo and no resource; `task smoke`'s
  // seeded stack has neither, so this must not run there. The gate always passes the operator password.
  test.skip(!process.env.QUICKSTART_OPERATOR_PASS, "QUICKSTART_OPERATOR_PASS is required (set by quickstart-browser-gate.sh only)");
  await login(page);
  const panel = page.getByRole("region", { name: "first-run checklist" });
  await expect(panel).toBeVisible({ timeout: 15_000 });
  await expect(panel).toContainText("Register a resource");
  await expect(panel).toContainText("Complete a governed action");

  // No paste, no server command anywhere on the default path.
  const button = panel.getByRole("button", { name: "Create loopback demo target" });
  await expect(button).toBeVisible();
  await expect(panel).not.toContainText("setup-loopback-demo.sh");
  await expect(page.getByLabel("username")).toHaveCount(0);
  await expect(page.getByLabel("ssh host key")).toHaveCount(0);
  if (EVIDENCE) await page.screenshot({ path: `${EVIDENCE}/01-demo-button-before.png`, fullPage: true });

  const created = page.waitForResponse((r) => r.url().endsWith("/v1/demo-target") && r.request().method() === "POST");
  await button.click();
  const resp = await created;
  expect(resp.status(), "POST /v1/demo-target").toBe(201);
  const resource = (await resp.json()) as { id: string; name: string };
  expect(resource.id, "created resource id").toMatch(/^[0-9a-f-]{8,}$/);
  expect(resource.name).toBe("loopback-demo");

  // The checklist's own re-read flips item 1; nothing offers a bind command (the server bound the key).
  const registerItem = panel.locator("li", { hasText: "Register a resource" });
  await expect(registerItem.locator('span[aria-hidden="true"]')).toHaveText("✓", { timeout: 15_000 });
  await expect(panel.getByRole("group", { name: "bind the demo credential" })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Remove the demo target" })).toBeVisible();

  fs.writeFileSync(STATE_FILE, resource.id, "utf8");
  if (EVIDENCE) await page.screenshot({ path: `${EVIDENCE}/02-demo-target-created.png`, fullPage: true });
  await testInfo.attach("quickstart-step3-resource-created", { body: await page.screenshot(), contentType: "image/png" });

  // The audit chain (the outbox relay is asynchronous, so retry the read).
  const banner = page.getByRole("region", { name: "filtered view" });
  const rows = page.locator("tbody tr");
  for (const outcome of ["resource.created", "credential.bound"]) {
    await expect(async () => {
      await page.goto("/audit");
      await page.getByLabel("outcome").fill(outcome);
      await page.getByRole("button", { name: "Apply" }).click();
      await expect(banner).toContainText(outcome, { timeout: 5_000 });
      await expect(rows.filter({ hasText: `resource:${resource.id}` })).toHaveCount(1, { timeout: 5_000 });
    }).toPass({ timeout: 60_000 });
  }
  await testInfo.attach("quickstart-onb4-credential-bound-audit-event", { body: await page.screenshot(), contentType: "image/png" });
});
