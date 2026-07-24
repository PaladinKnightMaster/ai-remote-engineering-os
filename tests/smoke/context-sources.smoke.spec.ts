// Layer 1 Live/Context Index browser smoke (T-100b; FE-26, GOV-11, ADR-0066 §1) — the
// operator charters a workstream, opens its detail, registers a charter-bound context
// source (seeing the GOV-11 consent copy on the surface AND in the confirm dialog before
// it commits), sees it listed with its staleness signal ("never indexed / pending" —
// nothing has swept it yet in this smoke run), then removes it (the GOV-11 removal
// confirm copy), in a real browser against the BUILT apps/web bundle on the live backend.
// It converts FE-26 into a CI gate: the /workstreams charter-detail surface RENDERS the
// context-sources section, the register form + consent copy, the staleness badge, and
// the remove verb. The indexer sweep itself (an up-to-date staleness signal, incremental
// re-index on edit) is WD-10's live e2e capstone (T-101), not this smoke spec.

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

test("operator registers a charter-bound context source (GOV-11 copy + staleness signal), then removes it", async ({
  page,
}, testInfo) => {
  await login(page);
  await page.goto("/workstreams");

  // Charter a minimal template workstream so the detail view (the context-sources
  // home, FE-26) has something to expand.
  const objective = `smoke-ctx-${Date.now()}`;
  await page.getByLabel("objective").fill(objective);
  await page.getByLabel("scope resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByRole("button", { name: /add to scope/i }).click();
  await page.getByLabel("step resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByLabel("step command", { exact: true }).fill("uptime");
  await page.getByRole("button", { name: /add step/i }).click();
  await page.getByRole("button", { name: /charter workstream/i }).click();

  const row = page.locator("li").filter({ hasText: objective });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await row.getByText(objective).click();

  // FE-26/GOV-11: the context-sources section renders on the charter detail, with its
  // consent copy stated at the surface (not only inside the confirm dialog).
  await expect(row.getByText("Context sources", { exact: true })).toBeVisible();
  await expect(row.getByText(/readable by this charter's composer agent/i)).toBeVisible();
  await expect(row.getByText(/no context sources registered/i)).toBeVisible();

  await testInfo.attach("context-sources-empty", { body: await page.screenshot(), contentType: "image/png" });

  // Register: the GOV-11 copy is visible in the confirm dialog BEFORE accepting (the
  // memories share-copy precedent — the prompt-injection risk named honestly).
  const rootPath = `/docs/runbooks/smoke-${Date.now()}`;
  let confirmMessage = "";
  page.once("dialog", (dialog) => {
    confirmMessage = dialog.message();
    void dialog.accept();
  });
  await row.getByLabel("root path").fill(rootPath);
  await row.getByRole("button", { name: /^register$/i }).click();

  const sourceRow = row.locator("li").filter({ hasText: rootPath });
  await expect(sourceRow).toBeVisible({ timeout: 30_000 });
  expect(confirmMessage).toMatch(/readable by this charter's composer agent/i);
  expect(confirmMessage).toMatch(/prompt-injection/i);

  // The staleness signal (FE-26): a freshly registered source has never been swept.
  await expect(sourceRow.getByText(/never indexed \/ pending/i)).toBeVisible();
  await expect(sourceRow.getByText("active")).toBeVisible();

  await testInfo.attach("context-source-registered", { body: await page.screenshot(), contentType: "image/png" });

  // Remove: the GOV-11 removal copy names the recall limit plainly.
  let removeConfirmMessage = "";
  page.once("dialog", (dialog) => {
    removeConfirmMessage = dialog.message();
    void dialog.accept();
  });
  await sourceRow.getByRole("button", { name: /^remove$/i }).click();
  await expect(sourceRow).toHaveCount(0, { timeout: 30_000 });
  expect(removeConfirmMessage).toMatch(/stops future agent reads/i);
  expect(removeConfirmMessage).toMatch(/cannot recall/i);

  await testInfo.attach("context-source-removed", { body: await page.screenshot(), contentType: "image/png" });
});
