// Playwright config for the AI ReOS browser smoke suite (T-034).
//
// One chromium project driving the §12 governed-egress walkthrough against the
// BUILT apps/web bundle served same-origin behind Caddy over HTTPS (the shipped
// posture; the api login cookies are Secure, so plain HTTP would drop them).
// ignoreHTTPSErrors trusts Caddy's `tls internal` local CA inside the compose
// network. Runs only in the pinned Playwright image — @playwright/test here MUST
// match that image tag (infra/docker-compose/docker-compose.smoke.yml).
//
// SMOKE_BASE_URL is injected by the compose `smoke` service (https://web). The
// go-run backend compiles on first request, so the suite is patient: a generous
// per-test timeout plus a login retry loop in the spec absorb the cold start.

import { defineConfig, devices } from "@playwright/test";

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
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
