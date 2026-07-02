// Delegation-grants browser smoke (T-067a; FE-17, ADR-0062) — the operator issues a scoped
// delegation grant, sees it listed active, and revokes it, in a real browser against the BUILT
// apps/web bundle on the live backend. It converts FE-17 into a CI gate: the /grants surface
// RENDERS the issue form (resource picker + bounds), the grant list, and the revoke control.

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

test("operator issues a delegation grant, sees it listed active, and revokes it", async ({ page }, testInfo) => {
  await login(page);
  await page.goto("/grants");

  // A unique agent subject so the grant's row is unambiguous across re-runs.
  const agent = `agent:smoke-${Date.now()}`;
  await page.getByLabel("agent").fill(agent);
  await page.getByLabel("resource").selectOption({ label: RESOURCE });
  await page.getByRole("button", { name: /add resource/i }).click();
  await page.getByRole("button", { name: /issue grant/i }).click();

  // The new grant appears in the list, active + revocable. Scope to its row (unique agent).
  const row = page.locator("li").filter({ hasText: agent });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("active")).toBeVisible();

  await testInfo.attach("grant-issued", { body: await page.screenshot(), contentType: "image/png" });

  // Revoke it (DUR-7): the row flips to revoked and the Revoke control disappears.
  await row.getByRole("button", { name: /^revoke$/i }).click();
  await expect(row.getByText("revoked")).toBeVisible({ timeout: 30_000 });
  await expect(row.getByRole("button", { name: /^revoke$/i })).toHaveCount(0);
});
