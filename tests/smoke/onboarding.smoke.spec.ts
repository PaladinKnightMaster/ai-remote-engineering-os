// First-run checklist browser smoke (T-118a; ADR-0068 §D8, ONB-1/ONB-2).
//
// task smoke's backend is the SEEDED e2e stack (docker-compose.e2e.yml,
// included by docker-compose.smoke.yml) — a resource always exists from the
// seed, so this suite can only ever prove the "both conditions met" state,
// plus dismissal in a real browser. It is NOT the un-seeded first-run
// walkthrough: that is jsdom's job for the derived-condition permutations
// (apps/web/src/components/OnboardingChecklist.test.tsx) and GATE-4's §human
// leg on a genuinely fresh `wd1` install for the real un-seeded experience
// (ADR-0068 §D9) — this spec must not be read as covering either.
//
// "Has a governed action completed" needs an ACTUAL completed action in the
// audit chain, not just a seeded resource — and with `fullyParallel: false`/
// `workers: 1` (playwright.config.ts), file execution order is alphabetical
// and nothing earlier in the suite is guaranteed to have completed one by the
// time this file runs. So this spec drives one itself, through the real
// operator /runs path (the runs.smoke.spec.ts "C" seeding strategy) — a
// single ungated step that reaches `completed`, the same `tool.result` +
// `run.completed` outcomes OnboardingChecklist derives its second condition
// from.

import { test, expect, type Page } from "@playwright/test";

const UNGATED_RESOURCE = "e2e-target (ssh)"; // requires_approval=false (seed …e2)
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

test("the checklist reflects a completed governed action and dismissal survives a reload", async ({
  page,
}, testInfo) => {
  await login(page);

  // Drive one real governed action to completion (see file header) so the
  // second derived condition is true regardless of what ran before this file.
  await page.goto("/runs");
  await page.getByLabel("resource").selectOption({ label: UNGATED_RESOURCE });
  await page.getByLabel("command").fill("echo ONBOARDING-SMOKE-OK");
  await page.getByRole("button", { name: "Add step" }).click();
  await page.getByRole("button", { name: /review & launch/i }).click();
  await page.getByRole("region", { name: "launch review" }).waitFor({ state: "visible" });
  await page.getByRole("button", { name: /confirm launch/i }).click();
  await expect(page.getByRole("list", { name: "run steps" })).toContainText("completed", { timeout: 180_000 });

  // Both conditions are now true (a resource has existed since seed; a
  // governed action just completed): the dashboard checklist shows both
  // items met and the "all set" note.
  await page.goto("/");
  const panel = page.getByRole("region", { name: "first-run checklist" });
  await expect(panel).toBeVisible({ timeout: 15_000 });
  await expect(panel).toContainText("Register a resource");
  await expect(panel).toContainText("Complete a governed action");
  await expect(panel).toContainText(/all set/i, { timeout: 15_000 });

  await testInfo.attach("onboarding-checklist-complete", { body: await page.screenshot(), contentType: "image/png" });

  // ONB-2: Dismiss hides the panel, and the dismissal is a per-browser
  // localStorage flag — it must survive a real page reload, not just a
  // client-side unmount.
  await page.getByRole("button", { name: "Dismiss" }).click();
  await expect(panel).toHaveCount(0);

  await page.reload();
  await expect(panel).toHaveCount(0);

  await testInfo.attach("onboarding-checklist-dismissed-after-reload", {
    body: await page.screenshot(),
    contentType: "image/png",
  });
});

// PUB-6 P10/P12 — the two checklist states the SEEDED stack can never reach on its own (a
// resource always exists from the seed, and an earlier spec has usually completed an action).
// The built bundle runs in a real Chromium; only the two READ responses the checklist derives
// its state from are replaced, so the rendered DOM under test is the shipped one. The REAL
// un-seeded walkthrough (a genuine create -> real id) is quickstart-a.smoke.spec.ts.
const FAKE_DEMO_ID = "0190c0de-0000-7000-8000-00000000d3e0";

async function stubChecklistReads(page: Page, resources: unknown[]) {
  await page.route(
    (url) => url.pathname === "/v1/resources",
    (route) =>
      route.request().method() === "GET"
        ? route.fulfill({ json: { items: resources, page: {} } })
        : route.fallback(),
  );
  await page.route(
    (url) => url.pathname === "/v1/audit/events" && url.searchParams.get("outcome") === "tool.result",
    (route) => route.fulfill({ json: { items: [], page: {} } }),
  );
}

test("before the demo resource exists, the checklist says which host script prints the values the form asks for (P10)", async ({ page }, testInfo) => {
  await login(page);
  await stubChecklistReads(page, []);
  await page.goto("/");

  const panel = page.getByRole("region", { name: "first-run checklist" });
  await expect(panel.getByRole("button", { name: "Create loopback demo target" })).toBeVisible({ timeout: 15_000 });
  await expect(panel).toContainText("sudo ./setup-loopback-demo.sh generate --account aireos-demo");
  await expect(panel).toContainText("directory the bundle was unpacked into");
  await expect(panel).not.toContainText("infra/offline");

  await testInfo.attach("onboarding-p10-generate-instruction", { body: await page.screenshot(), contentType: "image/png" });
});

test("once the demo resource exists, the checklist shows its id and the exact bind command (P12)", async ({ page }, testInfo) => {
  await login(page);
  await stubChecklistReads(page, [{ id: FAKE_DEMO_ID, name: "loopback-demo", type: "ssh" }]);
  await page.goto("/");

  const bind = page.getByRole("group", { name: "bind the demo credential" });
  await expect(bind).toBeVisible({ timeout: 15_000 });
  await expect(bind).toContainText(FAKE_DEMO_ID);
  await expect(bind).toContainText(`sudo ./setup-loopback-demo.sh bind --resource-id ${FAKE_DEMO_ID}`);
  // It sits above the existing next-step hint.
  const panelText = (await page.getByRole("region", { name: "first-run checklist" }).innerText()).replace(/\s+/g, " ");
  expect(panelText.indexOf("--resource-id")).toBeGreaterThan(-1);
  expect(panelText.indexOf("--resource-id")).toBeLessThan(panelText.indexOf("Next: launch a one-step command"));

  await testInfo.attach("onboarding-p12-resource-id-and-bind", { body: await page.screenshot(), contentType: "image/png" });
});
