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
const GATED_RESOURCE_ID = "01900000-0000-7000-8000-0000000000a2"; // the flagged resource id (seed …a2)
const GATEWAY_URL = process.env.SMOKE_GATEWAY_URL ?? "http://mcp-gateway:8085"; // agent /mcp surface (FE-18 leg)
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

test("a parked Run step shows its Run context on the invocations console", async ({ page }, testInfo) => {
  await login(page);
  await composeAndLaunch(page, GATED_RESOURCE, "echo CONTEXT-SMOKE");

  // The gated step parks for approval; the operator console (FE-16) shows it is part
  // of a Run, at which step — derived by api from the step idempotency key — alongside
  // the verbatim command. The list fetches on mount / Refresh, so Refresh until it lands.
  const prompt = page.getByRole("region", { name: "approval required" });
  await expect(async () => {
    await page.goto("/invocations");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(prompt).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await expect(page.getByText(/part of run/i)).toBeVisible();
  await expect(page.getByText(/step 1/i)).toBeVisible();
  await expect(page.getByLabel("command")).toContainText("CONTEXT-SMOKE");

  await testInfo.attach("parked-run-step-context", { body: await page.screenshot(), contentType: "image/png" });

  // Deny to release the held step (cleanup) so it does not linger into later specs.
  await page.getByRole("button", { name: "Deny" }).click();
  await expect(prompt).toHaveCount(0, { timeout: 30_000 });
});

test("operator cancels a running governed run", async ({ page }) => {
  await login(page);
  // A gated step parks for approval server-side, so the Run stays running long enough to
  // cancel deterministically (the held step is torn down with the stack; DUR-5 cancel is
  // cooperative stop-future-steps, and FinishRun's running-guard keeps it canceled).
  await composeAndLaunch(page, GATED_RESOURCE, "echo CANCEL-SMOKE");

  // The Run row exposes a Cancel control while it is pending/running (DUR-5). Target the
  // NEWEST run's control (last in the id-ASC list) — an earlier spec's gated run may still be
  // finishing (its deny→fail is async), so the page can briefly show more than one.
  const cancel = page.getByRole("button", { name: /^cancel$/i }).last();
  await expect(cancel).toBeVisible({ timeout: 60_000 });
  await cancel.click();

  // The Run resolves to canceled (DUR-5 cooperative stop-future-steps).
  await expect(page.getByText("canceled")).toBeVisible({ timeout: 30_000 });
});

test("a parked delegated Run step shows the agent-on-behalf-of context (FE-18)", async ({ page }, testInfo) => {
  await login(page);

  // The operator issues a delegation grant + mints an agent token via api HTTP (the browser
  // session's cookies flow through page.request; CSRF via the aireos_csrf cookie).
  const csrf = (await page.context().cookies()).find((c) => c.name === "aireos_csrf")?.value ?? "";
  const hdr = { "X-CSRF-Token": csrf, "Content-Type": "application/json" };

  const grantResp = await page.request.post("/v1/grants", {
    headers: hdr,
    data: { agent: "agent:smoke", label: "fe18", resources: [GATED_RESOURCE_ID], verbs: ["exec"], ttlSeconds: 3600, maxStepsPerRun: 4, maxConcurrentRuns: 4, stepBudget: 10 },
  });
  expect(grantResp.status(), "issue grant").toBe(201);
  const grantId = (await grantResp.json()).id as string;

  const tokResp = await page.request.post("/v1/agent-tokens", {
    headers: hdr,
    data: { agentId: "smoke", resourceId: GATED_RESOURCE_ID, operations: ["exec"] },
  });
  expect(tokResp.ok(), "mint agent token").toBeTruthy();
  const agentToken = (await tokResp.json()).token as string;

  // The agent launches a grant-bounded Run on the FLAGGED resource via run.launch → it parks.
  const launchResp = await page.request.post(`${GATEWAY_URL}/mcp`, {
    headers: { Authorization: `Bearer ${agentToken}`, "Content-Type": "application/json" },
    data: {
      jsonrpc: "2.0", id: 1, method: "tools/call",
      params: { name: "run.launch", arguments: { grantId, steps: [{ resourceId: GATED_RESOURCE_ID, command: "echo FE18-SMOKE" }] } },
    },
  });
  expect(launchResp.ok(), "agent run.launch").toBeTruthy();

  // The operator console shows the parked step is AGENT-driven under the operator's grant:
  // the FE-16 run context + FE-18 "on behalf of operator:admin" + the GOV-6 bounding grant
  // id (T-068b). Scope to MY step's row (its command is unique) — other parked steps may
  // linger from earlier specs.
  const row = page.locator("li").filter({ hasText: "FE18-SMOKE" });
  await expect(async () => {
    await page.goto("/invocations");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(row).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 90_000 });
  await expect(row.getByText(/part of run/i)).toBeVisible();
  await expect(row.getByText(/on behalf of/i)).toBeVisible();
  await expect(row).toContainText("operator:admin"); // the delegating principal
  await expect(row.getByText(/· grant/i)).toBeVisible();
  await expect(row).toContainText(grantId); // GOV-6: the full delegated context beside the command

  await testInfo.attach("delegated-parked-step-context", { body: await page.screenshot(), contentType: "image/png" });

  // FE-19: the operator's /runs view labels the same Run as agent-driven under the grant.
  await page.goto("/runs");
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.getByText(/driven by/i)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("agent:smoke")).toBeVisible();
  await testInfo.attach("agent-driven-run-label", { body: await page.screenshot(), contentType: "image/png" });

  // Back to the console to deny the parked step (cleanup) so it does not linger into later specs.
  await page.goto("/invocations");
  await page.getByRole("button", { name: "Refresh" }).click();
  const parked = page.locator("li").filter({ hasText: "FE18-SMOKE" });
  await expect(parked).toBeVisible({ timeout: 10_000 });
  await parked.getByRole("button", { name: "Deny" }).click();
  await expect(parked).toHaveCount(0, { timeout: 30_000 });
});
