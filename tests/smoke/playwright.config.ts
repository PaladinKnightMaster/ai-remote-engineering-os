// Playwright config for the AI ReOS browser smoke suite (T-034).
//
// Two projects driving the suite against the BUILT apps/web bundle served
// same-origin behind Caddy over HTTPS (the shipped posture; the api login
// cookies are Secure, so plain HTTP would drop them). ignoreHTTPSErrors trusts
// Caddy's `tls internal` local CA inside the compose network. Runs only in the
// pinned Playwright image — @playwright/test here MUST match that image tag
// (infra/docker-compose/docker-compose.smoke.yml).
//
// SMOKE_BASE_URL is injected by the compose `smoke` service (https://web). The
// go-run backend compiles on first request, so the suite is patient: a generous
// per-test timeout plus a login retry loop in each spec absorb the cold start.
//
// `chromium` — the original desktop project (T-034), unchanged: it matches
// every `*.smoke.spec.ts` file EXCEPT the mobile-only ones (testIgnore below),
// so desk-only specs are never additionally driven at a 375px viewport where
// they were never meant to work.
//
// `mobile` — FE-38's evidence (T-115b, ADR-0068 §D5): a SECOND project, scoped
// by `testMatch` to ONLY `*.mobile.smoke.spec.ts` files, so adding it does not
// double the suite's runtime by re-running the whole desk suite at a phone
// viewport. Base device is devices["iPhone SE"] (a real WebKit mobile
// profile — UA, touch, deviceScaleFactor) with its viewport WIDTH overridden
// to exactly 375, the number FE-38 names; its own default (320) is narrower
// than what the criterion asks to prove.
import { defineConfig, devices } from "@playwright/test";

const MOBILE_SPEC = "**/*.mobile.smoke.spec.ts";
const MOBILE_DEVICE = { ...devices["iPhone SE"], viewport: { width: 375, height: 667 } };

export default defineConfig({
  testDir: ".",
  testMatch: "**/*.smoke.spec.ts",
  // The cold go-run stack can take a couple of minutes to compile + boot.
  timeout: 240_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  use: {
    baseURL: process.env.SMOKE_BASE_URL ?? "https://web",
    ignoreHTTPSErrors: true,
    headless: true,
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"] }, testIgnore: MOBILE_SPEC },
    { name: "mobile", use: { ...MOBILE_DEVICE }, testMatch: MOBILE_SPEC },
  ],
});
