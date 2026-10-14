// Quickstart browser gate — spec A: steps 1 (log in) and 3 (create the
// loopback demo target through the cockpit) of
// docs/devops/quickstart-first-governed-action.md (T-133b, completing OI-36
// alongside T-133a's shell half; ONB-4).
//
// Runs standalone via infra/offline/wd1/quickstart-browser-gate.sh — NOT
// through docker-compose.smoke.yml (that `include`s the whole e2e backend, a
// second unrelated stack, per Commander finding D) — against the freshly
// brought-up aireos-demo-host stack. That stack is started with
// bringup-demo.sh --no-seed-resource (Commander finding A): every earlier
// walkthrough ran against a pre-seeded resource, which makes
// OnboardingChecklist's "Create loopback demo target" button permanently
// invisible (it renders only while !resourceExists) — so this is the first
// automated run that can ever click it.
//
// ONB-4 ("no demo-only code path"): clicking the button only proves the
// button works, not which code path ran underneath it. This spec goes
// further — after the click, it asserts a `resource.created` audit event
// targeting the EXACT new resource:<id> exists in the chain (AUD-40:
// CreateResource chains this event in the same transaction as the row
// insert). That is positive, evidence-backed proof the ordinary
// POST /v1/resources handler executed, not a UI-only assertion.
//
// The new resource's id is written to QUICKSTART_STATE_FILE — bind-mounted
// tests/smoke (gitignored) — for the orchestrator to read: quickstart step 4
// (bind the credential, host shell) needs it, and it crosses back into spec B
// (steps 5/6/7-cockpit) as an env var, because the quickstart alternates
// planes (browser -> host shell -> browser) and cannot be one Playwright run
// (Commander finding B).

import fs from "node:fs";
import { test, expect, type Page } from "@playwright/test";

const OPERATOR_USER = process.env.QUICKSTART_OPERATOR_USER ?? "admin";
const OPERATOR_PASS = process.env.QUICKSTART_OPERATOR_PASS ?? "demo-operator-pw";
const DEMO_ACCOUNT = process.env.QUICKSTART_DEMO_ACCOUNT ?? "aireos-demo";
// Required: the full "ssh-ed25519 AAAA... aireos-demo-host" line step 2's
// `generate` printed — no fallback, a missing key would silently create an
// unconnectable resource (see OnboardingChecklist.tsx's ssh_host_key note).
const DEMO_HOST_KEY = process.env.QUICKSTART_HOST_KEY;
const STATE_FILE = process.env.QUICKSTART_STATE_FILE ?? "/smoke/.quickstart-resource-id";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("quickstart steps 1+3: log in and create the loopback demo target through the real cockpit path (ONB-4)", async ({ page }, testInfo) => {
  test.skip(!DEMO_HOST_KEY, "QUICKSTART_HOST_KEY env var is required (the ssh-ed25519 line quickstart step 2's generate printed)");

  // Step 1.
  await login(page);
  const panel = page.getByRole("region", { name: "first-run checklist" });
  await expect(panel).toBeVisible({ timeout: 15_000 });
  await expect(panel).toContainText("Register a resource");
  await expect(panel).toContainText("Complete a governed action");

  // Step 3: the create button only renders while no resource exists yet
  // (Commander finding A) — this stack was brought up with
  // --no-seed-resource for exactly this reason.
  // PUB-6 P10: the cockpit names the host script whose output this form asks for.
  await expect(panel).toContainText("sudo ./setup-loopback-demo.sh generate --account aireos-demo");
  await panel.getByRole("button", { name: "Create loopback demo target" }).click();
  await page.getByLabel("username").fill(DEMO_ACCOUNT);
  await page.getByLabel("ssh host key").fill(DEMO_HOST_KEY!);

  const created = page.waitForResponse((r) => r.url().endsWith("/v1/resources") && r.request().method() === "POST");
  await page.getByRole("button", { name: "Create" }).click();
  const resourceResp = await created;
  expect(resourceResp.ok(), "POST /v1/resources").toBeTruthy();
  const resource = (await resourceResp.json()) as { id: string };
  expect(resource.id, "created resource id").toMatch(/^[0-9a-f-]{8,}$/);

  // The checklist's own live re-read (ONB-1) flips item 1 — proves the click
  // registered on the derived-state read the doc describes, not just that
  // the POST returned 2xx.
  const registerItem = panel.locator("li", { hasText: "Register a resource" });
  await expect(registerItem.locator('span[aria-hidden="true"]')).toHaveText("✓", { timeout: 15_000 });

  // PUB-6 P12: the new resource's id and its bind command are rendered, so step 4 needs no psql.
  const bind = panel.getByRole("group", { name: "bind the demo credential" });
  await expect(bind).toContainText(resource.id, { timeout: 15_000 });
  await expect(bind).toContainText(`sudo ./setup-loopback-demo.sh bind --resource-id ${resource.id}`);

  fs.writeFileSync(STATE_FILE, resource.id, "utf8");

  await testInfo.attach("quickstart-step3-resource-created", { body: await page.screenshot(), contentType: "image/png" });

  // ONB-4's real proof: a resource.created event targeting THIS resource is
  // in the audit chain (AUD-40 chains it in-tx with the row insert) — only
  // the real handler fires it, so this is stronger evidence than any UI
  // assertion that the click "worked". The outbox relay is asynchronous
  // (Postgres LISTEN/NOTIFY + periodic-sweep fallback, ADR-0045), so retry
  // the whole read until it lands rather than asserting on the first page.
  const banner = page.getByRole("region", { name: "filtered view" });
  const rows = page.locator("tbody tr");
  await expect(async () => {
    await page.goto("/audit");
    await page.getByLabel("outcome").fill("resource.created");
    await page.getByRole("button", { name: "Apply" }).click();
    await expect(banner).toContainText("resource.created", { timeout: 5_000 });
    await expect(rows.filter({ hasText: `resource:${resource.id}` })).toHaveCount(1, { timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  await testInfo.attach("quickstart-onb4-resource-created-audit-event", { body: await page.screenshot(), contentType: "image/png" });
});
