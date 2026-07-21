// Persistent Scoped Memory browser smoke (T-082; FE-22/FE-23, GOV-9, ADR-0064) — the
// operator authors free-text memories on their own operator scope, sees them listed with
// content, redacts one (governed forgetting — content erased, the tombstone renders),
// and pins another, in a real browser against the BUILT apps/web bundle on the live
// backend. It converts FE-22/FE-23 into a CI gate: the /memories surface RENDERS the
// scope picker, the GOV-9 consent copy, the authoring form, and the tombstone/pin
// verbs. No expiry leg here (the reaper's ~2-minute sweep cadence has no place in a
// smoke spec — that lives at the WD-8 e2e capstone, T-083).

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

test("operator authors, redacts, and pins memories on their own operator scope", async ({ page }, testInfo) => {
  await login(page);

  // These routes fetch once on mount / Refresh (no auto-poll) — retry the navigation
  // until the default operator scope has resolved and the authoring form is present
  // (the runs.smoke.spec.ts goto+Refresh+toPass lesson).
  await expect(async () => {
    await page.goto("/memories");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByRole("button", { name: /author memory/i })).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  // GOV-9: the consent copy renders on the authoring form (sealed/operator-only, the R2
  // redaction boundary, content-free accrual).
  await expect(page.getByText(/sealed and operator-only/i)).toBeVisible();
  await expect(page.getByText(/does not and cannot rewrite the audit chain/i)).toBeVisible();

  // Author two notes, keyed uniquely so their rows stay identifiable after redaction
  // erases content (the key survives a tombstone; the content does not).
  const keyA = `smoke-key-a-${Date.now()}`;
  const keyB = `smoke-key-b-${Date.now()}`;

  await page.getByLabel("key", { exact: true }).fill(keyA);
  await page.getByLabel("content", { exact: true }).fill("note A body — should vanish on redact");
  await page.getByRole("button", { name: /author memory/i }).click();
  const rowA = page.locator("li").filter({ hasText: keyA });
  await expect(rowA).toBeVisible({ timeout: 30_000 });
  await expect(rowA.getByText(/note A body/)).toBeVisible();

  await page.getByLabel("key", { exact: true }).fill(keyB);
  await page.getByLabel("content", { exact: true }).fill("note B body");
  await page.getByRole("button", { name: /author memory/i }).click();
  const rowB = page.locator("li").filter({ hasText: keyB });
  await expect(rowB).toBeVisible({ timeout: 30_000 });

  await testInfo.attach("memories-authored", { body: await page.screenshot(), contentType: "image/png" });

  // Redact A: accept the confirm dialog; content is erased and the row renders the
  // Redacted tombstone (never blank-as-if-empty) — the row stays identifiable by key.
  page.once("dialog", (dialog) => void dialog.accept());
  await rowA.getByRole("button", { name: /^redact$/i }).click();
  await expect(rowA.getByText(/redacted/i)).toBeVisible({ timeout: 30_000 });
  await expect(rowA.getByText(/note A body/)).toHaveCount(0);
  await expect(rowA.getByRole("button", { name: /^redact$/i })).toHaveCount(0);

  // Pin B: the Pinned badge appears.
  await rowB.getByRole("button", { name: /^pin$/i }).click();
  await expect(rowB.getByText("Pinned")).toBeVisible({ timeout: 30_000 });

  await testInfo.attach("memories-redacted-and-pinned", { body: await page.screenshot(), contentType: "image/png" });
});

// R1(b) — the share flip leg (T-091; FE-25, MEM-6, GOV-10, ADR-0065 §3). The operator
// authors a memory, shares it (the GOV-10 consent copy is visible in the confirm
// dialog before accepting), sees the "visible to agent" badge appear, then unshares it
// and the badge disappears. Converts FE-25 into a CI gate: the /memories surface
// RENDERS the share/unshare verbs + badge on an authored row.
test("operator shares an authored memory (GOV-10 confirm copy visible) then unshares it", async ({ page }, testInfo) => {
  await login(page);

  await expect(async () => {
    await page.goto("/memories");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(page.getByRole("button", { name: /author memory/i })).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  const key = `smoke-share-${Date.now()}`;
  await page.getByLabel("key", { exact: true }).fill(key);
  await page.getByLabel("content", { exact: true }).fill("shareable note body");
  await page.getByRole("button", { name: /author memory/i }).click();
  const row = page.locator("li").filter({ hasText: key });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("visible to agent")).toHaveCount(0); // unshared is the default (R2)

  await testInfo.attach("memory-authored-for-share", { body: await page.screenshot(), contentType: "image/png" });

  // Share: the GOV-10 copy is visible in the confirm dialog BEFORE accepting.
  let confirmMessage = "";
  page.once("dialog", (dialog) => {
    confirmMessage = dialog.message();
    void dialog.accept();
  });
  await row.getByRole("button", { name: /^share$/i }).click();
  await expect(row.getByText("visible to agent")).toBeVisible({ timeout: 30_000 });
  expect(confirmMessage).toMatch(/composer agent's context/i);
  expect(confirmMessage).toMatch(/cannot recall past ones/i);

  await testInfo.attach("memory-shared", { body: await page.screenshot(), contentType: "image/png" });

  // Unshare: the badge disappears.
  page.once("dialog", (dialog) => void dialog.accept());
  await row.getByRole("button", { name: /^unshare$/i }).click();
  await expect(row.getByText("visible to agent")).toHaveCount(0);

  await testInfo.attach("memory-unshared", { body: await page.screenshot(), contentType: "image/png" });
});
