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

// R1(a) — the adaptive charter leg (T-091; FE-24, ADR-0065 §4/§5). The operator
// charters an ADAPTIVE Workstream (kind + a named composer, no template steps) and
// sees the kind badge + composer on its detail; the charter's first tick fires with
// NOBODY PRESENT (the schedule, not the composer) and opens a durable plan request —
// the detail renders it OPEN, with its TTL. This smoke stack keeps the 60s production
// schedule floor (unlike e2e's lowered one), so the wait is real — the "goto + toPass"
// pacing pattern (memories.smoke.spec.ts) is reused rather than a fixed sleep: each
// retry fully reloads the route (a fresh mount re-fetches the detail; the app's
// selected-workstream effect only re-fetches on a NEW selection, not on Refresh alone).
// The composer answering the request (plan.submit, an MCP client) is explicitly OUT of
// scope here — T-092's WD-9 capstone owns that leg; this proves only the inbound
// channel opens with nobody present.
test("operator charters an adaptive workstream; its first tick opens a plan request with nobody present", async ({ page }, testInfo) => {
  await login(page);
  await page.goto("/workstreams");

  const objective = `smoke-adaptive-${Date.now()}`;
  await page.getByLabel("objective").fill(objective);

  await page.getByLabel("scope resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByRole("button", { name: /add to scope/i }).click();

  await page.getByLabel("workstream kind").selectOption("adaptive");
  const composer = `agent:smoke-composer-${Date.now()}`;
  await page.getByLabel("composer agent").fill(composer);
  await expect(page.getByText(/always parks for your review/i)).toBeVisible();
  await expect(page.getByText(/accrued memory/i)).toBeVisible();

  // The production floor (60s) — the wait below is real, not lowered.
  await page.getByLabel("Interval seconds").fill("60");

  await page.getByRole("button", { name: /charter workstream/i }).click();

  const row = page.locator("li").filter({ hasText: objective });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("active")).toBeVisible();

  await row.getByText(objective).click();
  // Scoped to the row: the bare word "adaptive" also matches the kind <select>'s
  // option and the consent copy elsewhere on the page (the T-091 smoke lesson).
  await expect(row.getByText("adaptive", { exact: true })).toBeVisible();
  await expect(row.getByText(composer)).toBeVisible();
  await expect(row.getByText(/no plan requests yet/i)).toBeVisible();

  await testInfo.attach("adaptive-charter-chartered", { body: await page.screenshot(), contentType: "image/png" });

  // The tick fires ~60s after chartering, with nobody present, and opens a plan
  // request. Poll by fully reloading the route (the T-084-lineage pacing pattern:
  // retry the whole assertion via a fresh mount, not a fixed sleep or a stale effect).
  await expect(async () => {
    await page.goto("/workstreams");
    const freshRow = page.locator("li").filter({ hasText: objective });
    await expect(freshRow).toBeVisible({ timeout: 10_000 });
    await freshRow.getByText(objective).click();
    await expect(freshRow.getByText(/plan requests/i)).toBeVisible({ timeout: 5_000 });
    await expect(freshRow.getByText(/open · expires/i)).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 100_000 });

  await testInfo.attach("adaptive-plan-request-opened", { body: await page.screenshot(), contentType: "image/png" });
});
