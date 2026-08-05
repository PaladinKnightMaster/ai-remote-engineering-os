// <Button> browser-rendered evidence (T-116; ADR-0068 §D3, FE-36/FE-42).
//
// jsdom cannot prove a hover or active state paints — the defect this
// component fixes (as-built finding 4: "no button with an actual button
// appearance has any hover or active state, anywhere in the product, since
// v0.1.0") is by definition invisible to jsdom, which never loads Tailwind's
// CSS at all. This spec drives the real, built apps/web bundle in a real
// Chromium and asserts `getComputedStyle` — not className presence, which
// proves nothing about what the browser actually painted.
//
// The proving ground is /pairing (T-115a/b), migrated onto <Button> by this
// same ticket: it happens to carry exactly one button of each family —
// Refresh (ghost), Create pairing code (primary), Done (default), Revoke
// (destructive) — so this single route exercises all four variants without
// this ticket touching any other route (T-117's job).
//
// Interaction notes:
//   - :active is read via a real mouse mousedown (not `locator.click()`,
//     which resolves too fast to observe the pseudo-class) followed by
//     mouseup on the same point, which also completes that button's real
//     action — Refresh (idempotent), Create (submits, which this spec needs
//     anyway to reach Done), Done (dismisses, after the pairing link is
//     already captured), Revoke (revokes, the spec's last step). The
//     mouse-click-focus-shows-no-ring assertion piggybacks on the same
//     mousedown read, since a mouse click focuses the element too.
//   - focus-visible-via-keyboard is proven separately with a real
//     `page.keyboard.press("Tab")` loop from a neutral (non-focusable)
//     starting point, since programmatic `.focus()` does not exercise the
//     same "was this keyboard modality" heuristic real users hit.
//   - Disabled is asserted once, on "Create pairing code" before a label is
//     typed (`disabled={creating || !label.trim()}` — a stable, non-racy
//     disabled condition, unlike the brief in-flight `revokingId`/`creating`
//     windows) — the `disabled:opacity-60 disabled:pointer-events-none`
//     fragment is the literal same constant shared by all four variant
//     cells in packages/ui/src/components/Button.tsx, so one real-browser
//     proof of it painting covers the mechanism for all four.
//   - Every cell carries `transition-colors`, which ANIMATES the changed
//     property over Tailwind's default duration rather than snapping it —
//     a real effect of this component, not a test artifact, but it means
//     reading `getComputedStyle` immediately after `hover()`/`mousedown`
//     races the transition and can catch a mid-animation frame (observed:
//     an early frame can read as "still resting", flaking the assertion).
//     SETTLE_MS below is a deliberate wait past the transition before each
//     read — proving the STEADY-STATE painted value, not the animation.

import { test, expect, request as pwRequest, type Page, type Locator } from "@playwright/test";

const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";
const BASE_URL = process.env.SMOKE_BASE_URL ?? "https://web";
// Comfortably past Tailwind's default 150ms transition-colors duration.
const SETTLE_MS = 300;

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

// Tabs forward from a neutral (non-interactive) starting point until the
// target locator's element is `document.activeElement`, proving real
// keyboard traversal reaches it (rather than asserting on a
// programmatically-`.focus()`ed element, which doesn't exercise the same
// browser focus-visible heuristic a real Tab key does).
async function tabToElement(page: Page, target: Locator, maxTabs = 40) {
  await page.getByRole("heading", { name: "Device pairing" }).click();
  // Chromium's focus-visible heuristic suppresses the ring on the very
  // first Tab immediately following a mouse click (observed: the real
  // target-seeking Tab below reliably shows the ring for every element
  // EXCEPT one reached on the very first press right after the reset
  // click). A throwaway Tab/Shift+Tab pair "warms up" keyboard modality
  // and returns focus to the same starting point before the real,
  // measured traversal begins.
  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  for (let i = 0; i < maxTabs; i++) {
    await page.keyboard.press("Tab");
    const isActive = await target.evaluate((el) => el === document.activeElement);
    if (isActive) return;
  }
  throw new Error("tabToElement: target was not reached within maxTabs Tab presses");
}

test("Button states are real: resting, hover, active and focus-visible paint on all four variants; disabled is non-interactive", async ({
  page,
}) => {
  await login(page);
  await page.goto("/pairing");

  // ===== ghost — Refresh =====
  const refresh = page.getByRole("button", { name: "Refresh" });
  await expect(refresh).toBeVisible();

  const ghostResting = await refresh.evaluate((el) => getComputedStyle(el).textDecorationLine);
  expect(ghostResting, "ghost resting: no underline").toBe("none");

  await refresh.hover();
  await page.waitForTimeout(SETTLE_MS);
  const ghostHover = await refresh.evaluate((el) => getComputedStyle(el).textDecorationLine);
  expect(ghostHover, "ghost hover: underline paints").toBe("underline");

  await tabToElement(page, refresh);
  const ghostTabRing = await refresh.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(ghostTabRing, "ghost keyboard focus: a ring paints").not.toBe("none");

  // Blur first: tabToElement left keyboard focus (and its focus-visible
  // ring) sitting on this exact element. A mousedown on an already-focused
  // element does not re-trigger a focus event, so without this reset the
  // "mouse click shows no ring" check below would just be reading the
  // leftover keyboard-focus ring, not a fresh mouse-triggered one.
  await page.getByRole("heading", { name: "Device pairing" }).click();
  const ghostRestingOpacity = await refresh.evaluate((el) => getComputedStyle(el).opacity);
  const box = (await refresh.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(SETTLE_MS);
  const ghostActive = await refresh.evaluate((el) => ({
    opacity: getComputedStyle(el).opacity,
    boxShadow: getComputedStyle(el).boxShadow,
  }));
  expect(ghostActive.opacity, "ghost active: opacity changes").not.toBe(ghostRestingOpacity);
  expect(ghostActive.boxShadow, "ghost mouse-click focus: no ring").toBe("none");
  await page.mouse.up(); // completes a harmless Refresh click

  // ===== primary — Create pairing code (also proves disabled) =====
  const create = page.getByRole("button", { name: /create pairing code/i });
  await expect(create).toBeVisible();

  const disabledStyle = await create.evaluate((el) => ({
    opacity: getComputedStyle(el).opacity,
    pointerEvents: getComputedStyle(el).pointerEvents,
  }));
  expect(disabledStyle.opacity, "disabled: dimmed").toBe("0.6");
  expect(disabledStyle.pointerEvents, "disabled: non-interactive").toBe("none");

  const label = `button-states-${Date.now()}`;
  await page.getByLabel("label").fill(label);

  const primaryResting = await create.evaluate((el) => getComputedStyle(el).backgroundColor);
  await create.hover();
  await page.waitForTimeout(SETTLE_MS);
  const primaryHover = await create.evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(primaryHover, "primary hover: background changes").not.toBe(primaryResting);

  await tabToElement(page, create);
  const primaryTabRing = await create.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(primaryTabRing, "primary keyboard focus: a ring paints").not.toBe("none");

  await page.getByRole("heading", { name: "Device pairing" }).click(); // blur (see the ghost block's comment)
  const createBox = (await create.boundingBox())!;
  await page.mouse.move(createBox.x + createBox.width / 2, createBox.y + createBox.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(SETTLE_MS);
  const primaryActive = await create.evaluate((el) => ({
    backgroundColor: getComputedStyle(el).backgroundColor,
    boxShadow: getComputedStyle(el).boxShadow,
  }));
  expect(primaryActive.backgroundColor, "primary active: background changes").not.toBe(primaryResting);
  expect(primaryActive.boxShadow, "primary mouse-click focus: no ring").toBe("none");
  await page.mouse.up(); // submits — creates the pairing request

  // ===== default — Done =====
  const qr = page.getByRole("img", { name: /pairing link/i });
  await expect(qr).toBeVisible({ timeout: 15_000 });
  const link = (await page.locator("code").innerText()).trim();
  const fragment = link.split("#")[1] ?? "";
  const [id, secret] = fragment.split(".");
  expect(id, "pairing request id parsed from the fragment").toBeTruthy();
  expect(secret, "one-time secret parsed from the fragment").toBeTruthy();

  const done = page.getByRole("button", { name: /^done$/i });
  await expect(done).toBeVisible();

  const defaultResting = await done.evaluate((el) => getComputedStyle(el).backgroundColor);
  await done.hover();
  await page.waitForTimeout(SETTLE_MS);
  const defaultHover = await done.evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(defaultHover, "default hover: background changes").not.toBe(defaultResting);

  await tabToElement(page, done);
  const defaultTabRing = await done.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(defaultTabRing, "default keyboard focus: a ring paints").not.toBe("none");

  await page.getByRole("heading", { name: "Device pairing" }).click(); // blur (see the ghost block's comment)
  const doneBox = (await done.boundingBox())!;
  await page.mouse.move(doneBox.x + doneBox.width / 2, doneBox.y + doneBox.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(SETTLE_MS);
  const defaultActive = await done.evaluate((el) => ({
    backgroundColor: getComputedStyle(el).backgroundColor,
    boxShadow: getComputedStyle(el).boxShadow,
  }));
  expect(defaultActive.backgroundColor, "default active: background changes").not.toBe(defaultResting);
  expect(defaultActive.boxShadow, "default mouse-click focus: no ring").toBe("none");
  await page.mouse.up(); // dismisses the secret panel

  // ===== destructive — Revoke =====
  // Redeem the captured fragment (same device-plays-itself pattern as
  // pairing.smoke.spec.ts) so a live, revocable row exists to carry the
  // fourth variant. A separate request context avoids the redeemed
  // session's Set-Cookie clobbering the operator's own desk cookie.
  const device = await pwRequest.newContext({ baseURL: BASE_URL, ignoreHTTPSErrors: true });
  const redeem = await device.post(`/v1/pairing-requests/${id}/redeem`, { data: { secret } });
  expect(redeem.ok(), "device redeem must succeed").toBeTruthy();
  await device.dispose();

  const row = page.locator("li").filter({ hasText: label });
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(row).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 30_000 });

  const revoke = row.getByRole("button", { name: /^revoke$/i });
  await expect(revoke).toBeVisible();

  const destructiveResting = await revoke.evaluate((el) => getComputedStyle(el).backgroundColor);
  await revoke.hover();
  await page.waitForTimeout(SETTLE_MS);
  const destructiveHover = await revoke.evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(destructiveHover, "destructive hover: background changes").not.toBe(destructiveResting);

  await tabToElement(page, revoke);
  const destructiveTabRing = await revoke.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(destructiveTabRing, "destructive keyboard focus: a ring paints").not.toBe("none");

  await page.getByRole("heading", { name: "Device pairing" }).click(); // blur (see the ghost block's comment)
  const revokeBox = (await revoke.boundingBox())!;
  await page.mouse.move(revokeBox.x + revokeBox.width / 2, revokeBox.y + revokeBox.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(SETTLE_MS);
  const destructiveActive = await revoke.evaluate((el) => ({
    backgroundColor: getComputedStyle(el).backgroundColor,
    boxShadow: getComputedStyle(el).boxShadow,
  }));
  expect(destructiveActive.backgroundColor, "destructive active: background changes").not.toBe(
    destructiveResting,
  );
  expect(destructiveActive.boxShadow, "destructive mouse-click focus: no ring").toBe("none");
  await page.mouse.up(); // revokes the session

  await expect(row.getByText("revoked")).toBeVisible({ timeout: 30_000 });
});

// T-117/FE-42: the mechanism-level proof above (all four variants) was taken on
// /pairing, the one route T-116 itself touched. This second test proves the
// SAME mechanism travels with the component to a route T-117 migrated but never
// built the component in — /audit's Refresh button (default/sm, a distinct
// variant+size cell from anything exercised above: sm here vs. xs for "Done").
// If this test is red while the suite above is green, the defect is in the
// MIGRATION (a call site not actually wired to <Button>), not in <Button> itself.
test("Button states travel with the component to a T-117-migrated route (/audit, outside Pairing.tsx)", async ({
  page,
}) => {
  await login(page);
  await page.goto("/audit");

  const refresh = page.getByRole("button", { name: "Refresh" });
  await expect(refresh).toBeVisible();

  const resting = await refresh.evaluate((el) => getComputedStyle(el).backgroundColor);

  await refresh.hover();
  await page.waitForTimeout(SETTLE_MS);
  const hover = await refresh.evaluate((el) => getComputedStyle(el).backgroundColor);
  expect(hover, "default/sm hover: background changes").not.toBe(resting);

  await page.keyboard.press("Tab");
  await page.keyboard.press("Shift+Tab");
  for (let i = 0; i < 40; i++) {
    await page.keyboard.press("Tab");
    if (await refresh.evaluate((el) => el === document.activeElement)) break;
  }
  const tabRing = await refresh.evaluate((el) => getComputedStyle(el).boxShadow);
  expect(tabRing, "default/sm keyboard focus: a ring paints").not.toBe("none");

  await page.getByRole("heading", { name: "Audit timeline" }).click(); // blur
  const box = (await refresh.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.waitForTimeout(SETTLE_MS);
  const active = await refresh.evaluate((el) => ({
    backgroundColor: getComputedStyle(el).backgroundColor,
    boxShadow: getComputedStyle(el).boxShadow,
  }));
  expect(active.backgroundColor, "default/sm active: background changes").not.toBe(resting);
  expect(active.boxShadow, "default/sm mouse-click focus: no ring").toBe("none");
  await page.mouse.up(); // completes a harmless Refresh click
});
