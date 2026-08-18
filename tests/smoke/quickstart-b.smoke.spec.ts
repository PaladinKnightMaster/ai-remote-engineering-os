// Quickstart browser gate — spec B: steps 5 (launch a one-step Run), 6 (the
// audit surface), and step 7's cockpit half (Remove the demo target) of
// docs/devops/quickstart-first-governed-action.md (T-133b, completing OI-36
// alongside T-133a's shell half).
//
// Runs standalone via infra/offline/wd1/quickstart-browser-gate.sh, in a
// SEPARATE Playwright run from spec A, because quickstart step 4 (bind the
// credential) happens on the host shell in between — the quickstart
// alternates planes (browser -> host shell -> browser) and cannot be one
// Playwright run (Commander finding B). QUICKSTART_RESOURCE_ID is the id
// spec A created and the orchestrator's shell step 4 bound.

import { test, expect, type Page } from "@playwright/test";
import fs from "node:fs";

const OPERATOR_USER = process.env.QUICKSTART_OPERATOR_USER ?? "admin";
const OPERATOR_PASS = process.env.QUICKSTART_OPERATOR_PASS ?? "demo-operator-pw";
const RESOURCE_ID = process.env.QUICKSTART_RESOURCE_ID;
const DONE_FILE = process.env.QUICKSTART_DONE_FILE ?? "/smoke/.quickstart-b-done";
const RESOURCE_LABEL = process.env.QUICKSTART_RESOURCE_LABEL ?? "loopback-demo (ssh)"; // "{name} ({type})", Runs.tsx
const COMMAND = "whoami";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("quickstart steps 5/6/7-cockpit: launch a governed Run, see its events in the audit surface, remove the demo target", async ({
  page,
}, testInfo) => {
  test.skip(!RESOURCE_ID, "QUICKSTART_RESOURCE_ID env var is required (spec A's created + step 4's bound resource id)");

  await login(page);

  // Step 5: compose and launch a one-step Run. The resource was created
  // WITHOUT requires_approval (OnboardingChecklist.tsx), so this runs
  // unattended to completed with no approval to park on (ONB-3).
  await page.goto("/runs");
  await page.getByLabel("resource").selectOption({ label: RESOURCE_LABEL });
  await page.getByLabel("command").fill(COMMAND);
  await page.getByRole("button", { name: "Add step" }).click();
  await page.getByRole("button", { name: /review & launch/i }).click();
  const review = page.getByRole("region", { name: "launch review" });
  await expect(review).toBeVisible();
  // "confirm the single step reads back exactly the command you typed" (doc step 5).
  await expect(review.getByLabel("command")).toContainText(COMMAND);

  const launched = page.waitForResponse((r) => r.url().endsWith("/v1/runs") && r.request().method() === "POST");
  await page.getByRole("button", { name: /confirm launch/i }).click();
  const runResp = await launched;
  expect(runResp.ok(), "POST /v1/runs").toBeTruthy();
  const run = (await runResp.json()) as { id: string };

  // This is the assertion VER-16's negative leg breaks: without step 4's
  // bind, the connector has no credential and the step fails instead of
  // completing.
  const steps = page.getByRole("list", { name: "run steps" });
  await expect(steps).toContainText("completed", { timeout: 180_000 });

  await testInfo.attach("quickstart-step5-run-completed", { body: await page.screenshot(), contentType: "image/png" });

  // Step 6: the audit surface shows the exact events the doc names, each
  // targeting the id the as-built code actually targets (services/mcp-gateway
  // Finish -> resource:<id>; services/api run/store.go -> run:<id>).
  const banner = page.getByRole("region", { name: "filtered view" });
  const rows = page.locator("tbody tr");

  async function assertAuditRow(outcome: string, targetSubstring: string) {
    await expect(async () => {
      await page.goto("/audit");
      await page.getByLabel("outcome").fill(outcome);
      await page.getByRole("button", { name: "Apply" }).click();
      await expect(banner).toContainText(outcome, { timeout: 5_000 });
      await expect(rows.filter({ hasText: targetSubstring })).toHaveCount(1, { timeout: 5_000 });
    }).toPass({ timeout: 60_000 });
  }

  await assertAuditRow("run.completed", `run:${run.id}`);
  await assertAuditRow("tool.result", `resource:${RESOURCE_ID}`);
  await assertAuditRow("run.launched", `run:${run.id}`);

  await testInfo.attach("quickstart-step6-audit-events", { body: await page.screenshot(), contentType: "image/png" });

  // Step 7 (cockpit half): remove the demo target through the checklist's own
  // action — the same governed DELETE /v1/resources/{id} route any resource
  // removal uses (RemoveLoopbackDemoTarget, T-118c).
  await page.goto("/");
  const panel = page.getByRole("region", { name: "first-run checklist" });
  await expect(panel).toBeVisible({ timeout: 15_000 });
  const removeButton = panel.getByRole("button", { name: "Remove the demo target" });
  await expect(removeButton).toBeVisible({ timeout: 15_000 });

  const removed = page.waitForResponse((r) => r.url().includes("/v1/resources/") && r.request().method() === "DELETE");
  await removeButton.click();
  const delResp = await removed;
  expect(delResp.ok(), "DELETE /v1/resources/{id}").toBeTruthy();

  // ONB-1's derived read flips item 1 back — the removal actually took.
  const registerItem = panel.locator("li", { hasText: "Register a resource" });
  await expect(registerItem.locator('span[aria-hidden="true"]')).toHaveText("○", { timeout: 15_000 });

  await testInfo.attach("quickstart-step7-cockpit-removed", { body: await page.screenshot(), contentType: "image/png" });

  // Completion marker, written as the LAST act of the test body (Commander
  // review, T-133b). A SKIPPED Playwright test still exits 0, so the
  // orchestrator's exit-code check alone cannot tell "spec B passed" from
  // "spec B never ran" -- e.g. if QUICKSTART_RESOURCE_ID were ever empty, the
  // test.skip() above would fire, `npx playwright test` would exit 0, step 7's
  // --undo would succeed regardless, and the gate would print PASS having
  // proven nothing. That is the S-25 silent-guardrail shape inside the gate
  // itself. Spec A is already covered by the state file it writes; this is the
  // symmetric marker for spec B, and the orchestrator asserts it.
  fs.writeFileSync(DONE_FILE, "ok", "utf8");
});
