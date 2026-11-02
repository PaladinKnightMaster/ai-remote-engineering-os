// First-run setup in a REAL browser — T-175d, ADR-0078 (FRX-1/FRX-2). jsdom cannot show this: the page, the cookie and the
// redirects only exist in a rendered browser against a real stack.
//
// Runs standalone via infra/offline/wd1/setup-browser-gate.sh against a stack brought up by `bringup-demo.sh --no-operator`
// (setup.sh, NO operator anywhere). SETUP_CODE is the code setup.sh printed on the host. The screenshots land in
// SETUP_EVIDENCE_DIR; SETUP_DONE_FILE is the positive marker (a skipped Playwright test still exits 0).

import fs from "node:fs";
import { test, expect } from "@playwright/test";

const CODE = process.env.SETUP_CODE;
const EVIDENCE = process.env.SETUP_EVIDENCE_DIR ?? "/evidence";
const DONE_FILE = process.env.SETUP_DONE_FILE ?? "/smoke/.setup-done";
const USER = "founder-op";
const PASS = process.env.SETUP_OPERATOR_PASS ?? "a-long-browser-chosen-passphrase";

test("first-run setup: setup page -> code -> dashboard; afterwards the page is gone", async ({ page, browser }) => {
  test.skip(!CODE, "SETUP_CODE is required (the code setup.sh printed on the host)");
  const shot = (name: string) => page.screenshot({ path: `${EVIDENCE}/${name}.png`, fullPage: true });
  const setupForm = page.getByRole("form", { name: "first-run setup" });

  // Setup is open, and the server says so.
  const open = await page.request.get("/v1/setup");
  expect(open.status()).toBe(200);
  expect(await open.json()).toEqual({ setupRequired: true });

  // Every route is the setup page, and /login goes there too.
  await page.goto("/");
  await expect(setupForm).toBeVisible();
  await page.goto("/login");
  await expect(setupForm).toBeVisible();
  await expect(page.getByRole("button", { name: /sign in/i })).toHaveCount(0);
  await page.goto("/");
  await expect(page.getByLabel("Setup code")).toBeVisible();
  await shot("01-setup-page");

  // A wrong code is refused in the server's own words; the form stays.
  await page.getByLabel("Setup code").fill("AAAAA-AAAAA-AAAAA-AAAAA");
  await page.getByLabel("Username").fill(USER);
  await page.getByLabel("Password", { exact: true }).fill(PASS);
  await page.getByLabel("Confirm password").fill(PASS);
  await page.getByRole("button", { name: /create operator account/i }).click();
  await expect(page.getByRole("alert")).toContainText("setup.sh --new-setup-code");
  await shot("02-wrong-code-refused");

  // The real code, typed the way a person might (lower case, no dashes), creates the operator and signs in.
  await page.getByLabel("Setup code").fill(CODE!.toLowerCase().replaceAll("-", ""));
  await page.getByRole("button", { name: /create operator account/i }).click();
  await expect(page.getByRole("heading", { name: "Operator actions" })).toBeVisible();
  await expect(setupForm).toHaveCount(0);
  await shot("03-dashboard-after-setup");

  // Afterwards the page is unreachable: /setup is not a route, and neither is the API's half of it.
  await page.goto("/setup");
  await expect(page.getByRole("heading", { name: "Operator actions" })).toBeVisible();
  await expect(setupForm).toHaveCount(0);
  await page.reload();
  await expect(setupForm).toHaveCount(0);
  expect((await page.request.get("/v1/setup")).status()).toBe(404);
  expect((await page.request.post("/v1/setup/complete", { data: { code: CODE, username: "x-op", password: PASS } })).status()).toBe(404);

  // A browser with no session: /setup lands on the login page, and the operator chosen in setup signs in.
  const ctx = await browser.newContext({ baseURL: process.env.SMOKE_BASE_URL, ignoreHTTPSErrors: true });
  const fresh = await ctx.newPage();
  await fresh.goto("/setup");
  await expect(fresh).toHaveURL(/\/login$/);
  await expect(fresh.getByRole("form", { name: "first-run setup" })).toHaveCount(0);
  await fresh.screenshot({ path: `${EVIDENCE}/04-setup-route-lands-on-login.png`, fullPage: true });
  await fresh.getByLabel("Username").fill(USER);
  await fresh.getByLabel("Password").fill(PASS);
  await fresh.getByRole("button", { name: /sign in/i }).click();
  await expect(fresh.getByRole("heading", { name: "Operator actions" })).toBeVisible();
  await ctx.close();

  fs.writeFileSync(DONE_FILE, "ok");
});
