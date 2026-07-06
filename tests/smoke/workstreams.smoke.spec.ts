// Chartered-Workstreams browser smoke (T-075; FE-20, ADR-0063) — the operator charters a
// standing Workstream (objective + scope + an immutable template + a schedule), sees it
// listed active with its next fire, pauses it, resumes it, and retires it, in a real
// browser against the BUILT apps/web bundle on the live backend. It converts FE-20 into a
// CI gate: the /workstreams surface RENDERS the charter form (scope picker + template
// steps + bounds + the GOV-8 consent copy), the charter list, and the lifecycle controls.
// The LIVE schedule fire is WD-7 (T-076) at the low e2e floor — smoke is lifecycle-only
// (the smoke stack keeps the 60s production floor, so nothing here waits a minute).

import { test, expect, type Page } from "@playwright/test";

const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";
const RESOURCE = "e2e-target (ssh)"; // seed …e2

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("operator charters a workstream, sees it active, and pauses → resumes → retires it", async ({ page }, testInfo) => {
  await login(page);
  await page.goto("/workstreams");

  // The GOV-8 consent copy renders on the charter form.
  await expect(page.getByText(/unattended, scheduled execution/i)).toBeVisible();

  // A unique objective so the charter's row is unambiguous across re-runs.
  const objective = `smoke-health-${Date.now()}`;
  await page.getByLabel("objective").fill(objective);

  // Scope: pick the seed resource (exact so it doesn't clash with the 'charter scope' list
  // label once added — the T-070 capture-spec lesson).
  await page.getByLabel("scope resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByRole("button", { name: /add to scope/i }).click();

  // Template: one step on the scoped resource.
  await page.getByLabel("step resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByLabel("step command", { exact: true }).fill("uptime");
  await page.getByRole("button", { name: /add step/i }).click();

  // A short interval is fine to enter (>= the 60s floor); nothing waits for it in smoke.
  await page.getByLabel("Interval seconds").fill("60");

  await page.getByRole("button", { name: /charter workstream/i }).click();

  // The new charter appears active, with its schedule. Scope to its row (unique objective).
  const row = page.locator("li").filter({ hasText: objective });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("active")).toBeVisible();
  await expect(row.getByText(/every 60s/)).toBeVisible();

  await testInfo.attach("workstream-chartered", { body: await page.screenshot(), contentType: "image/png" });

  // Pause → the row's Resume control appears.
  await row.getByRole("button", { name: /^pause$/i }).click();
  await expect(row.getByText("paused")).toBeVisible({ timeout: 30_000 });
  await expect(row.getByRole("button", { name: /^resume$/i })).toBeVisible();

  // Resume → back to active.
  await row.getByRole("button", { name: /^resume$/i }).click();
  await expect(row.getByText("active")).toBeVisible({ timeout: 30_000 });

  // Retire → terminal; the Retire control disappears.
  await row.getByRole("button", { name: /^retire$/i }).click();
  await expect(row.getByText("retired")).toBeVisible({ timeout: 30_000 });
  await expect(row.getByRole("button", { name: /^retire$/i })).toHaveCount(0);

  await testInfo.attach("workstream-retired", { body: await page.screenshot(), contentType: "image/png" });
});
