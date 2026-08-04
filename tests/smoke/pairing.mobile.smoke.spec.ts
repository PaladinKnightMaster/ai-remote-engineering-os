// Mobile approval-surface browser smoke (T-115b; FE-38, FE-42, GOV-13,
// SEC-34/SEC-35, ADR-0068 §D5) — the DEVICE half of QR pairing, proven in a
// real 375px mobile browser (the `mobile` Playwright project,
// devices["iPhone SE"]) against the BUILT apps/web bundle on the live e2e
// backend. Matched ONLY by the `mobile` project's testMatch
// (playwright.config.ts) — the `.mobile.smoke.spec.ts` suffix keeps this out
// of the `chromium` project, so the desktop-only suite doesn't double-run at
// a phone viewport, and this file doesn't run twice.
//
// Flow: an authenticated desk operator (a plain API request context — never
// `page`, so its Set-Cookie never touches the device's own cookie jar; the
// T-115a lesson pairing.smoke.spec.ts already documents) creates a pairing
// request and parks a real held tool invocation. The device (`page`, the
// mobile-emulated fixture) redeems the QR link via /pair#<id>.<secret>,
// lands on the reduced-nav Dashboard, opens the approval surface, reads the
// held command and approves it — all asserted to fit in 375px with NO
// horizontal page scroll — and finally is refused an out-of-scope action
// (launching a Run) from that SAME paired session, proving SEC-35's denial
// live in the browser rather than only server-side.

import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";

const FLAGGED_RESOURCE_ID = "01900000-0000-7000-8000-0000000000a2"; // requires_approval=true (seed)
const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";
const BASE_URL = process.env.SMOKE_BASE_URL ?? "https://web";
const GATEWAY_URL = process.env.SMOKE_GATEWAY_URL ?? "http://mcp-gateway:8085";
// Deliberately long — this is the one thing on the approval surface that can
// force horizontal overflow if it isn't contained (FE-38's own warning).
const LONG_COMMAND =
  "echo PAIR-MOBILE-SMOKE-OK-this-is-a-deliberately-long-remote-exec-command-string-used-to-exercise-the-375px-approval-surface-without-any-horizontal-page-scroll";

async function loginDesk(): Promise<APIRequestContext> {
  const desk = await pwRequest.newContext({ baseURL: BASE_URL, ignoreHTTPSErrors: true });
  // The go-run backend compiles on first request — patient retry, same posture
  // as every other spec's login loop.
  await expect(async () => {
    const res = await desk.post("/v1/auth/login", { data: { username: OPERATOR_USER, password: OPERATOR_PASS } });
    expect(res.ok(), "operator login").toBeTruthy();
  }).toPass({ timeout: 180_000 });
  return desk;
}

async function csrfHeaderFor(ctx: APIRequestContext): Promise<Record<string, string>> {
  const state = await ctx.storageState();
  const csrf = state.cookies.find((c) => c.name === "aireos_csrf")?.value ?? "";
  return { "X-CSRF-Token": csrf };
}

test("a paired device redeems, approves a held invocation at 375px with no horizontal overflow, and is refused an out-of-scope action", async ({
  page,
}, testInfo) => {
  // --- Desk half (plain API calls; never `page`) ---------------------------
  const desk = await loginDesk();
  const deskCsrf = await csrfHeaderFor(desk);

  const label = `smoke-mobile-${Date.now()}`;
  const createdReq = await desk.post("/v1/pairing-requests", { headers: deskCsrf, data: { label } });
  expect(createdReq.ok(), "desk creates a pairing request").toBeTruthy();
  const { id, secret } = (await createdReq.json()) as { id: string; secret: string };

  // Mint a scoped agent-tool token and fire a real held invocation
  // (block-and-wait) — mirrors invocation-approval.smoke.spec.ts, seeding a
  // REAL parked invocation through the real path, no cross-service DB poking.
  const mint = await desk.post("/v1/agent-tokens", {
    headers: deskCsrf,
    data: { agentId: "smoke-mobile", resourceId: FLAGGED_RESOURCE_ID, operations: ["exec"] },
  });
  expect(mint.ok(), "operator mints an agent token").toBeTruthy();
  const { token } = (await mint.json()) as { token: string };

  const agent = await pwRequest.newContext({ baseURL: GATEWAY_URL, ignoreHTTPSErrors: true });
  const held = agent
    .post("/mcp", {
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      data: {
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: { name: "remote.exec", arguments: { resourceId: FLAGGED_RESOURCE_ID, command: LONG_COMMAND } },
      },
      timeout: 180_000,
    })
    .catch(() => null);

  // --- Device half: `page` is the mobile project's OWN 375px browser
  // context — a separate cookie jar from `desk` by construction, so
  // redeem's Set-Cookie lands ONLY here (the T-115a trap this spec drives
  // for real, harder than T-115a did). -------------------------------------
  await page.goto(`/pair#${id}.${secret}`);
  await expect(page).toHaveURL(/\/invocations$/, { timeout: 30_000 });

  // §3/§1: the phone's shell offers only what approvals@1 permits — asserted
  // LIVE, not only jsdom. Dashboard hides every operator-only action and
  // keeps Invocations + Audit.
  await page.goto("/");
  await expect(page.getByText(/paired device session/i)).toBeVisible();
  await expect(page.getByRole("link", { name: /pending tool invocations/i })).toBeVisible();
  await expect(page.getByRole("link", { name: /audit timeline/i })).toBeVisible();
  for (const name of [/^sessions/i, /^runs/i, /delegation grants/i, /workstreams/i, /^memory/i, /device pairing/i]) {
    await expect(page.getByRole("link", { name })).toHaveCount(0);
  }
  await page.getByRole("link", { name: /pending tool invocations/i }).click();
  await expect(page).toHaveURL(/\/invocations$/);

  // FE-38: the held invocation's command reads at 375px; approve/deny reachable.
  const prompt = page.getByRole("region", { name: "approval required" });
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(prompt).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await expect(page.getByLabel("command")).toContainText("PAIR-MOBILE-SMOKE-OK");

  // The page body must never scroll horizontally at a 375px viewport — the
  // command box may scroll WITHIN its own container (ApprovalPrompt's
  // overflow-x-auto <pre>), but document-level overflow is a hard fail.
  const documentOverflows = await page.evaluate(
    () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
  );
  expect(documentOverflows, "the page body must never scroll horizontally at 375px (FE-38)").toBe(false);

  await testInfo.attach("mobile-approval-surface", { body: await page.screenshot(), contentType: "image/png" });

  await page.getByRole("button", { name: "Approve" }).click();
  await expect(prompt).toHaveCount(0, { timeout: 30_000 });

  const res = await held;
  if (res) {
    expect(res.ok(), "the approved tool call must not error").toBeTruthy();
    const body = JSON.stringify(await res.json());
    expect(body).not.toContain("denied");
  }
  await agent.dispose();

  // --- SEC-35's live UI-level confirmation: this SAME paired session (the
  // cookie jar `page` now holds) is refused an out-of-scope action —
  // launching a Run. Reusing `page`'s cookies here is deliberately correct
  // (unlike the redeem step above): we WANT to probe THIS session's own
  // permission boundary, not a different role's. -----------------------
  const csrf = (await page.context().cookies()).find((c) => c.name === "aireos_csrf")?.value ?? "";
  const denied = await page.request.post("/v1/runs", {
    headers: { "X-CSRF-Token": csrf },
    data: { steps: [] },
  });
  expect(denied.status(), "a live paired session must be refused launching a Run (SEC-35)").toBe(403);
  const deniedBody = JSON.stringify(await denied.json());
  expect(deniedBody).toContain("not permitted for a paired session");
});
