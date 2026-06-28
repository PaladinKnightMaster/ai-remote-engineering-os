// Governed-Run browser smoke (T-059b) — the operator-facing leg of v0.5.0 Runs
// (FE-14/15; ADR-0061), proven in a real browser against the BUILT apps/web bundle on
// the same live backend the Go e2e uses (api sequencer → gateway → policy → broker →
// sshd). It converts FE-14/15 into a CI gate: the operator composes a plan, reviews it
// (informed launch, GOV-1), launches, watches per-step status, and cancels a run.
//
// Seeding (the "C" strategy): no DB poking — every Run is launched through the REAL
// operator path (POST /v1/runs via the SPA), driven server-side by the api sequencer.
// Unlike the invocation smoke, the browser holds NO gateway call: Runs are driven by
// the backend, so the surface is simply launch → poll → render. The full byte-exact
// audit chain stays the job of `task e2e`; here we own what only a real browser proves:
// the /runs surface RENDERS the informed-launch review, per-step status, and cancel.

import { test, expect, type Page } from "@playwright/test";

const UNGATED_RESOURCE = "e2e-target (ssh)"; // requires_approval=false (seed: …e2)
const GATED_RESOURCE = "e2e-target-gated (ssh)"; // requires_approval=true  (seed: …a2)
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

// Compose a single-step plan on the /runs surface, review it (informed-launch gate),
// and confirm the launch. Returns once POST /v1/runs has been issued.
async function composeAndLaunch(page: Page, resourceLabel: string, command: string) {
  await page.goto("/runs");
  await page.getByLabel("resource").selectOption({ label: resourceLabel });
  await page.getByLabel("command").fill(command);
  await page.getByRole("button", { name: "Add step" }).click();

  // Informed launch (GOV-1): the full plan, command verbatim, is shown before the POST.
  await page.getByRole("button", { name: /review & launch/i }).click();
  const review = page.getByRole("region", { name: "launch review" });
  await expect(review).toBeVisible();
  await expect(review.getByLabel("command")).toContainText(command);
  await page.getByRole("button", { name: /confirm launch/i }).click();
}

test("operator launches a governed run and watches it reach completed", async ({ page }, testInfo) => {
  await login(page);
  await composeAndLaunch(page, UNGATED_RESOURCE, "echo RUN-SMOKE-OK");

  // The launched Run is auto-selected; the api sequencer drives the ungated step through
  // the gateway to terminal. Poll patiently — the cold go-run stack compiles on first
  // request, and the sequencer adds a scan + an SSH exec round-trip.
  const steps = page.getByRole("list", { name: "run steps" });
  await expect(steps).toContainText("completed", { timeout: 180_000 });

  await testInfo.attach("run-completed", { body: await page.screenshot(), contentType: "image/png" });
});

test("operator cancels a running governed run", async ({ page }) => {
  await login(page);
  // A gated step parks for approval server-side, so the Run stays running long enough to
  // cancel deterministically (the held step is torn down with the stack; DUR-5 cancel is
  // cooperative stop-future-steps, and FinishRun's running-guard keeps it canceled).
  await composeAndLaunch(page, GATED_RESOURCE, "echo CANCEL-SMOKE");

  // The Run row exposes a Cancel control while it is pending/running (DUR-5).
  const cancel = page.getByRole("button", { name: /^cancel$/i });
  await expect(cancel).toBeVisible({ timeout: 60_000 });
  await cancel.click();

  // The Run resolves to canceled and stops being cancelable.
  await expect(page.getByText("canceled")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: /^cancel$/i })).toHaveCount(0, { timeout: 30_000 });
});
