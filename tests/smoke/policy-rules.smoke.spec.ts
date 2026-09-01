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
// Must match tests/e2e/cmd/seed/main.go: the UNFLAGGED resource (no
// requires_approval label) — the exact case ADR-0074 diagnoses ("an unflagged
// host runs remote.exec ungoverned"). Distinct from governed-egress.smoke.spec.ts's
// FLAGGED_RESOURCE_ID: this leg proves a RULE governs what the label never did.
const RESOURCE_ID = "01900000-0000-7000-8000-0000000000e2";

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

  // FE-27 (T-152b): reach the page via the Dashboard card, not a direct
  // goto — this is the actual proof of discoverability VER-32's §human pass
  // depends on (the CEO would otherwise have to be told the URL).
  await page.goto("/");
  await page.getByRole("link", { name: /policy rules/i }).click();
  await expect(page).toHaveURL(/\/policy-rules$/);

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

// VER-31 — task smoke extended with a RULE-EVALUATION leg, not just CRUD. The test
// above proves a rule can be authored/toggled/removed; it never causes a rule to be
// EVALUATED (its target is a synthetic string no invocation can ever match). This one
// authors a rule against a REAL resource and watches the operator's own action change
// in a real browser: starting a session against an unflagged (no requires_approval)
// resource normally succeeds directly (no rule table, v0.14.0 semantics) — with a
// wildcard-subject deny rule live for session.start on it, the SAME action is refused.
test("an authored rule actually governs a real action, not just CRUD in the list", async ({ page }) => {
  await login(page);

  await page.goto("/policy-rules");
  const label = `smoke-eval-${Date.now()}`;
  await page.getByLabel("label").fill(label);
  await page.getByLabel("Subject").fill("*");
  await page.getByLabel("Edge").fill("agent-remote-session");
  await page.getByLabel("Action").fill("session.start");
  await page.getByLabel("Target").fill(`resource:${RESOURCE_ID}`);
  await page.getByLabel("outcome").selectOption("deny");
  await page.getByRole("button", { name: /create rule/i }).click();
  const row = page.locator("li").filter({ hasText: label });
  await expect(row).toBeVisible({ timeout: 30_000 });

  try {
    // Before this rule, starting a session against the UNFLAGGED resource succeeds
    // directly and navigates to /sessions/{id} — no approval prompt, no rule to
    // consult (CON-4's fallback). With the rule live, the SAME action is now denied.
    await page.goto("/sessions");
    const picker = page.getByLabel("resource");
    await expect(picker.locator(`option[value="${RESOURCE_ID}"]`)).toBeAttached({ timeout: 15_000 });
    await picker.selectOption(RESOURCE_ID);
    await page.getByRole("button", { name: "Start session" }).click();
    await expect(page.getByRole("alert")).toBeVisible({ timeout: 30_000 });
    await expect(page).not.toHaveURL(/\/sessions\/[0-9a-f-]{36}$/);
  } finally {
    // Cleanup even on a failed assertion above — SEC-54: deleting a rule leaves
    // decisions it already produced on the chain, but must not leave the rule live
    // for governed-egress.smoke.spec.ts or any other spec sharing this resource.
    await page.goto("/policy-rules");
    const cleanupRow = page.locator("li").filter({ hasText: label });
    await cleanupRow.getByRole("button", { name: /^delete$/i }).click();
    await expect(cleanupRow).toHaveCount(0, { timeout: 30_000 });
  }
});
