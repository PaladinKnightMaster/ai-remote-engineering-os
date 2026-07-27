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

// FE-27..30 (T-108; ADR-0067 §D6) — the live detail-refresh capstone. FE-30 is the
// interesting assertion: an open, POLLING detail panel must add ZERO audit events
// over its lifetime (operator read routes are bare SELECTs; the chained
// memory.read/context.read events are agent-plane only — a regression here would
// quietly feed the exact noise E-01 fixed). This does NOT assert on the global
// chainHead: the adaptive-charter test above leaves its charter ACTIVE for the rest
// of the suite, and its ticker chains plan.requested/plan.expired on its own 60s
// schedule — a whole-chain assertion would be flaky for reasons that have nothing
// to do with polling. Instead it scopes the assertion with the server-side actor
// filter (dogfooding T-105/106/107): the events matching this operator, and the
// events matching THIS charter's own actor. This charter's intervalSeconds is a
// long 3600 — active (so FE-27/28 polling runs) but it will not tick during the
// test window, isolating the assertion from its OWN ticker too.
test("an open, polling detail panel adds ZERO audit events over its lifetime (FE-30)", async ({ page }, testInfo) => {
  await login(page);
  await page.goto("/workstreams");

  const objective = `smoke-fe30-${Date.now()}`;
  await page.getByLabel("objective").fill(objective);
  await page.getByLabel("scope resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByRole("button", { name: /add to scope/i }).click();
  await page.getByLabel("step resource", { exact: true }).selectOption({ label: RESOURCE });
  await page.getByLabel("step command", { exact: true }).fill("uptime");
  await page.getByRole("button", { name: /add step/i }).click();
  // Well above the 60s floor and long enough it will not tick in this test's window.
  await page.getByLabel("Interval seconds").fill("3600");
  await page.getByRole("button", { name: /charter workstream/i }).click();

  const row = page.locator("li").filter({ hasText: objective });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByText("active")).toBeVisible();

  // The charter's id is not rendered in the DOM — resolve it via the same read the
  // SPA itself uses, so the actor filters below can target this charter exactly.
  const listResp = await page.request.get("/v1/workstreams");
  expect(listResp.ok()).toBeTruthy();
  const items = ((await listResp.json()).items ?? []) as { id: string; objective: string }[];
  const charter = items.find((w) => w.objective === objective);
  expect(charter, "charter resolvable via GET /v1/workstreams").toBeTruthy();
  const charterId = charter!.id;

  await row.getByText(objective).click();
  const freshness = row.getByText(/^updated /i);
  await expect(freshness).toBeVisible({ timeout: 15_000 }); // FE-29: the initial fetch lands

  async function auditEventsFor(actor: string) {
    const resp = await page.request.get(`/v1/audit/events?limit=200&actor=${encodeURIComponent(actor)}`);
    expect(resp.ok()).toBeTruthy();
    return ((await resp.json()).items ?? []) as unknown[];
  }
  async function freshnessSeconds(): Promise<number> {
    const text = (await freshness.textContent()) ?? "";
    if (/just now/i.test(text)) return 0;
    const m = text.match(/(\d+)s ago/i);
    return m ? Number(m[1]) : Number.POSITIVE_INFINITY;
  }

  // Baseline AFTER one poll cycle has passed — absorbs any immediate first tick, so
  // the baseline reflects the panel's steady, already-polling state.
  await page.waitForTimeout(13_000);
  expect(await freshnessSeconds(), "a poll landed recently at the baseline point").toBeLessThan(9);
  const operatorBaseline = await auditEventsFor("operator:admin");
  const charterBaseline = await auditEventsFor(`workstream:${charterId}`);

  // The positive control: ANOTHER full cycle resets the freshness affordance back
  // down rather than growing to ~26s — this proves polling actually WORKS (a
  // broken poller would otherwise pass the silence-only assertion below for the
  // wrong reason: nothing refreshing is also "zero new audit events").
  await page.waitForTimeout(13_000);
  expect(await freshnessSeconds(), "the freshness affordance reset across a cycle, proving polling works").toBeLessThan(9);

  await testInfo.attach("fe30-panel-polling-live", { body: await page.screenshot(), contentType: "image/png" });

  // At least 3 MORE poll cycles, panel open, no operator interaction.
  await page.waitForTimeout(39_000);

  expect(await auditEventsFor("operator:admin")).toEqual(operatorBaseline);
  expect(await auditEventsFor(`workstream:${charterId}`)).toEqual(charterBaseline);

  await testInfo.attach("fe30-zero-audit-events", { body: await page.screenshot(), contentType: "image/png" });
});
