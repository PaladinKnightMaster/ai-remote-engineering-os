// Device-pairing browser smoke (T-115a; FE-39/FE-42, GOV-12/GOV-13/GOV-14,
// ADR-0068 §D6) — the desk half of QR pairing, proven in a real browser
// against the BUILT apps/web bundle on the live e2e backend (which runs with
// AIREOS_PAIRING_ENABLED=1 as a test-stack-only override; see
// docker-compose.e2e.yml). It converts FE-39/GOV-14 into a CI gate: the
// /pairing surface renders a real QR (vendored encoder), the operator-chosen
// label appears once the request is redeemed, and revoking a session sticks.
//
// Redeem itself (device-side /pair UI) is T-115b's scope, not this ticket's —
// so here the "device" leg is played by a direct POST to the real, already-
// implemented redeem endpoint (T-114b), reading the id+secret straight out of
// the pairing link the page renders as selectable text (the same fallback the
// ADR requires for a device that cannot scan). This proves GOV-14's "session
// appears in the list, and revoking removes it" without building UI this
// ticket doesn't own.

import { test, expect, request as pwRequest, type Page } from "@playwright/test";

const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";
const BASE_URL = process.env.SMOKE_BASE_URL ?? "https://web";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("operator creates a pairing request, the QR renders, the redeemed session lists, and revoke sticks", async ({
  page,
}, testInfo) => {
  await login(page);
  await page.goto("/pairing");

  // A unique label so the row is unambiguous across re-runs (mirrors the
  // unique-agent-subject pattern in grants.smoke.spec.ts — the T-073
  // dirty-DB lesson).
  const label = `smoke-phone-${Date.now()}`;
  await page.getByLabel("label").fill(label);
  await page.getByRole("button", { name: /create pairing code/i }).click();

  // FE-39: the QR is rendered from the vendored encoder as inline SVG, with a
  // text alternative (role=img + aria-label) — this is the browser-rendered
  // evidence a jsdom unit test cannot provide.
  const qr = page.getByRole("img", { name: /pairing link/i });
  await expect(qr).toBeVisible({ timeout: 15_000 });
  expect(await qr.locator("rect").count()).toBeGreaterThan(1);

  // The same payload is ALSO available as selectable text (accessibility +
  // "a device that cannot scan"), and GOV-13: the secret rides the fragment,
  // never a query param — assert the shape here too.
  const link = (await page.locator("code").innerText()).trim();
  expect(link).toMatch(/^https:\/\/[^/]+\/pair#[0-9a-f-]+\.[A-Za-z0-9_-]+$/);

  await testInfo.attach("pairing-qr", { body: await page.screenshot(), contentType: "image/png" });

  const fragment = link.split("#")[1] ?? "";
  const [id, secret] = fragment.split(".");
  expect(id, "pairing request id parsed from the fragment").toBeTruthy();
  expect(secret, "one-time secret parsed from the fragment").toBeTruthy();

  // Play the device's half directly against the real, already-shipped
  // redeem endpoint (T-114b) — public/unauthenticated, exactly as a phone
  // that has never logged in would call it. A SEPARATE request context is
  // required here (mirrors the gateway-agent pattern in
  // invocation-approval.smoke.spec.ts): `page.request` shares the browser
  // context's cookie jar with `page`, so posting redeem through it would
  // have the paired session's Set-Cookie silently clobber the operator's own
  // desk session cookie mid-test — the redeem would "succeed" but then log
  // the desk OUT of its own session (caught in review: the list-refresh step
  // that follows started 403'ing because the browser was suddenly holding
  // the narrow approvals@1 cookie instead of the operator's).
  const device = await pwRequest.newContext({ baseURL: BASE_URL, ignoreHTTPSErrors: true });
  const redeem = await device.post(`/v1/pairing-requests/${id}/redeem`, {
    data: { secret },
  });
  expect(redeem.ok(), "the device's redeem must succeed with the fragment-carried secret").toBeTruthy();
  await device.dispose();

  // GOV-14: the desk's list picks up the new paired session (live, labeled
  // at request time — the redeeming device never chose this label).
  const row = page.locator("li").filter({ hasText: label });
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(row).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 30_000 });
  await expect(row.getByText("live")).toBeVisible();

  await testInfo.attach("paired-session-listed", { body: await page.screenshot(), contentType: "image/png" });

  // Revoke it: the row flips to revoked and the Revoke control disappears
  // (SEC-34's liveness check now refuses this session on every request).
  await row.getByRole("button", { name: /^revoke$/i }).click();
  await expect(row.getByText("revoked")).toBeVisible({ timeout: 30_000 });
  await expect(row.getByRole("button", { name: /^revoke$/i })).toHaveCount(0);
});
