// Audit read-surface browser smoke (T-107; AUD-26, FE-31..34, ADR-0067). Converts
// the v1.0-BLOCKING finding fix into a CI gate against the BUILT apps/web bundle on
// the live e2e backend: /audit opens newest-first on the governed-actions@1 preset,
// the disclosure banner names the preset and the whole chain's size, "Show all
// events" clears it in one interaction, and a server-side filter both changes what
// renders AND is echoed back in the banner (proving the round trip, not a client
// re-filter of the already-loaded page).
//
// The smoke backend has no production-shaped lifecycle noise, so this does NOT
// assert on any particular reduction ratio — only the mechanism. "agent-repo" is
// used as the deterministic zero-match probe: release-criteria PERF-7 records it as
// a reserved edge that currently matches zero rows in any environment, so filtering
// to it must empty a list that was, a moment ago, non-empty — proving reset (not
// append) without depending on seeded data volume.
//
// Commander-review follow-up (post-02b37be): the actor/outcome text filters
// commit on blur/Enter, not per keystroke (a request storm on the evidence
// surface, and a source of stale-response races). The actor-filter leg below
// exercises that REAL commit path (fill() then Enter) — filling alone must
// leave the view untouched, and Enter is what issues the request — rather than
// relying on fill() happening to trigger a fetch on its own.

import { test, expect, type Page } from "@playwright/test";

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

test("audit view opens newest-first on the preset, discloses it, and filters server-side", async ({ page }, testInfo) => {
  // The login itself chains an `auth.login.success` event (actor=operator:admin,
  // edge=human-agent — services/api's recordAuth, SEC-9) — a known, freshly-minted
  // event this spec can look for at the top of the newest-first view.
  await login(page);

  const banner = page.getByRole("region", { name: "filtered view" });
  const rows = page.locator("tbody tr");

  // FE-31: opens newest-first on the preset — the just-completed login is the
  // freshest governed action and is on the first screen with no paging.
  await expect(async () => {
    await page.goto("/audit");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(banner).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  await expect(rows.first()).toContainText("auth.login.success", { timeout: 30_000 });
  await expect(rows.first()).toContainText("operator:admin");

  // FE-32/EVD-8: the banner names the preset and discloses the whole chain's size
  // (chainHead — a position, not a row count) — never silent, never implied.
  await expect(banner).toContainText("governed-actions@1");
  await expect(banner).toContainText(/full chain has \d+ positions/i);

  await testInfo.attach("audit-newest-first-preset", { body: await page.screenshot(), contentType: "image/png" });

  // "Show all events" is ONE interaction: the banner disappears because nothing is
  // applied anymore — the canonical contiguous timeline.
  await page.getByRole("button", { name: /show all events/i }).click();
  await expect(banner).toHaveCount(0);

  // FE-33/AUD-26: setting a filter issues a fresh server-side request over the
  // WHOLE chain and RESETS the list (never appends, never re-filters in the
  // browser). "agent-repo" matches zero events anywhere (PERF-7), so a list that
  // was just non-empty (the login row above) must now render empty — and the
  // banner must echo the edge value back from the RESPONSE, proving a round trip
  // occurred rather than a client-side re-filter of stale data.
  await page.getByLabel("edge").selectOption("agent-repo");
  await expect(banner).toBeVisible({ timeout: 30_000 });
  await expect(banner).toContainText("agent-repo");
  await expect(page.getByText(/no audit events/i)).toBeVisible({ timeout: 30_000 });

  await testInfo.attach("audit-filtered-to-zero-match-edge", { body: await page.screenshot(), contentType: "image/png" });

  // Clearing the edge filter is itself a control change: a fresh request resets
  // the list again, and the login row reappears — this is a live read each time,
  // not a one-way client filter over a fixed page.
  await page.getByLabel("edge").selectOption("");
  await expect(rows.first()).toContainText("auth.login.success", { timeout: 30_000 });
  await expect(banner).toHaveCount(0);

  await testInfo.attach("audit-filter-cleared", { body: await page.screenshot(), contentType: "image/png" });

  // FE-33 (the real commit path): the actor text filter commits on blur/Enter,
  // never on keystroke. fill() alone changes only the draft — the view (no
  // banner right now) must stay exactly as it is until the operator commits.
  const actorInput = page.getByLabel("actor");
  await actorInput.fill("operator:admin");
  await expect(banner).toHaveCount(0); // uncommitted draft — nothing has changed yet

  // Enter (the wrapping form's onSubmit) is the commit: a fresh request is
  // issued and the banner + rows update together, from the response.
  await actorInput.press("Enter");
  await expect(banner).toBeVisible({ timeout: 30_000 });
  await expect(banner).toContainText("operator:admin");
  await expect(rows.first()).toContainText("auth.login.success", { timeout: 30_000 });

  await testInfo.attach("audit-actor-filter-committed", { body: await page.screenshot(), contentType: "image/png" });
});
