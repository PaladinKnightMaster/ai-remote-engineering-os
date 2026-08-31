// Policy-rules browser smoke (T-152; FE-24, FE-25) — the operator authors a policy rule
// with a wildcarded field, sees the blast-radius warning rendered BEFORE the rule is
// saved, then creates, disables, and deletes it, in a real browser against the BUILT
// apps/web bundle on the live backend. Converts FE-24 (list/create/enable/disable/
// delete) and FE-25's `block` criterion (blast radius visible before save, not a
// confirm dialog — FE-25a) into a CI gate, the same way grants.smoke.spec.ts does for
// FE-17.

import { test, expect, type Page } from "@playwright/test";

const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("operator sees the blast radius before saving a wildcard rule, then creates/disables/deletes it", async ({
  page,
}, testInfo) => {
  await login(page);
  await page.goto("/policy-rules");

  const label = `smoke-rule-${Date.now()}`;
  await page.getByLabel("label").fill(label);
  await page.getByLabel("Subject").fill("*");
  await page.getByLabel("Edge").fill("agent-remote-session");
  await page.getByLabel("Action").fill("remote.exec");
  await page.getByLabel("Target").fill(`smoke-target-${Date.now()}`);
  await page.getByLabel("outcome").selectOption("deny");

  // FE-25 (block): the blast radius renders inline, BEFORE the create button is ever
  // clicked — not behind a window.confirm at submit time (FE-25a).
  const blastRadius = page.getByRole("note");
  await expect(blastRadius).toBeVisible();
  await expect(blastRadius).toContainText(/every subject/i);
  await expect(blastRadius).toContainText(/severity beats specificity/i);

  await testInfo.attach("blast-radius-before-save", { body: await page.screenshot(), contentType: "image/png" });

  await page.getByRole("button", { name: /create rule/i }).click();

  const row = page.locator("li").filter({ hasText: label });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("enabled")).toBeVisible();

  await testInfo.attach("rule-created", { body: await page.screenshot(), contentType: "image/png" });

  await row.getByRole("button", { name: /^disable$/i }).click();
  await expect(row.getByText("disabled")).toBeVisible({ timeout: 30_000 });

  await row.getByRole("button", { name: /^delete$/i }).click();
  await expect(row).toHaveCount(0, { timeout: 30_000 });
});
