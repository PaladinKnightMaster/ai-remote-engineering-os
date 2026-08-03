// Deployment-profile browser smoke (T-112b; OI-24, CON-4, VER-2, ADR-0068 §D2).
// Proves the live cross-boundary path: a real AIREOS_DEPLOYMENT_PROFILE env var
// (set to lan-only on the api service — docker-compose.e2e.yml, included verbatim
// by docker-compose.smoke.yml) → the real api process → GET /v1/auth/me → the
// real generated openapi.ts types → the real SPA bundle served by Caddy. jsdom
// unit tests (capability.test.tsx) cover the component contract; this is the
// browser-rendered evidence the project DoD requires for apps/web rendering
// changes.

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

test("dashboard renders the active deployment profile from a live env var", async ({ page }, testInfo) => {
  await login(page);

  // The dashboard is the post-login landing route; the header renders the
  // capability-edge identity line this ticket extends (Dashboard.tsx).
  await expect(page).toHaveURL(/\/(dashboard)?$/, { timeout: 15_000 });

  const header = page.locator("header");
  await expect(header).toContainText("lan-only", { timeout: 30_000 });

  await testInfo.attach("dashboard-deployment-profile", { body: await page.screenshot(), contentType: "image/png" });
});
