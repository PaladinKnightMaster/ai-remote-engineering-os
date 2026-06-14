// Governed-egress browser smoke (T-034) — the headless, machine version of the
// §12 human WD-2 walkthrough that gated v0.2.0. It drives the operator cockpit in
// a real browser against the BUILT apps/web bundle served behind Caddy, on the
// same live backend the Go e2e harness uses (api → policy → connector → broker →
// sshd → audit), and converts the human gate into a CI gate:
//
//   login → start a session against the requires_approval-flagged resource →
//   <ApprovalPrompt> → DENY (assert the session is blocked) → start again →
//   APPROVE → assert the RENDERED xterm terminal (type a command, read its output
//   back from the painted rows) → end → assert the audit timeline renders the
//   governed-egress outcomes.
//
// Division of labour: the Go e2e (tests/e2e/approval_test.go) remains the
// authoritative proof of the LIVE AUDIT CHAIN on real wires (cast-SHA anchored,
// byte-exact). This suite owns what only a real browser can prove — that the
// shipped bundle actually RENDERS: the login form, the approval prompt, and the
// xterm terminal. The xterm-stylesheet guard below would have failed on the
// latent v0.2.0 defect (<TerminalView> never importing xterm.css), which every
// jsdom unit test passed straight through.

import { test, expect, type Page } from "@playwright/test";

// Must match tests/e2e/cmd/seed/main.go + the e2e compose bootstrap operator.
const FLAGGED_RESOURCE_ID = "01900000-0000-7000-8000-0000000000a2"; // requires_approval=true
const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";

// Log in through the rendered form. Retries the whole submit because the go-run
// backend compiles on first request — mirrors the Go harness's 120s login retry.
async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    // Success leaves /login (RequireAuth lets an authenticated operator through).
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

// Start a session against the flagged resource and land on its page, where api
// has parked it pending_approval (ADR-0054) and the SPA renders <ApprovalPrompt>.
async function startFlaggedSession(page: Page) {
  await page.goto("/sessions");
  await page.getByPlaceholder("resource id (UUID)").fill(FLAGGED_RESOURCE_ID);
  await page.getByRole("button", { name: "Start session" }).click();
  await expect(page).toHaveURL(/\/sessions\/[^/]+$/);
  await expect(page.getByRole("region", { name: "approval required" })).toBeVisible();
}

test("governed-egress cockpit walkthrough renders end-to-end", async ({ page }, testInfo) => {
  await test.step("login", async () => {
    await login(page);
  });

  await test.step("deny leg — the approval gate blocks the session", async () => {
    await startFlaggedSession(page);
    await page.getByRole("button", { name: "Deny" }).click();
    // The blocked session must surface as not-approved, never a terminal.
    await expect(page.getByText(/not approved/i)).toBeVisible();
    await expect(page.getByRole("group", { name: "remote terminal" })).toHaveCount(0);
  });

  await test.step("approve leg — the terminal mounts and renders live output", async () => {
    await startFlaggedSession(page);
    await page.getByRole("button", { name: "Approve" }).click();

    // The broker WS attaches once api flips the session to 'pending'. The header
    // status reads '· open' when the channel is live; sending before then is lost.
    await expect(page.getByText("· open")).toBeVisible({ timeout: 60_000 });

    const terminal = page.getByRole("group", { name: "remote terminal" });
    await expect(terminal).toBeVisible();

    // Type into xterm (focus its hidden textarea via the container) and read the
    // command's output back from the PAINTED rows — the proof the round-trip and
    // the render both work. $((6*7)) exercises the remote shell, like the
    // v0.2.0 browser sweep (echo SWEEP-$((6*7))-OK → SWEEP-42-OK).
    await terminal.click();
    await page.keyboard.type("echo SMOKE-$((6*7))-OK");
    await page.keyboard.press("Enter");
    await expect(page.locator(".xterm-rows")).toContainText("SMOKE-42-OK", { timeout: 30_000 });

    // Regression guard for the latent v0.2.0 defect: <TerminalView> must import
    // @xterm/xterm/css/xterm.css, so the built bundle ships a stylesheet whose
    // rules target .xterm. Without it the rows still carry text (this DOM text
    // assertion would pass) but paint as unreadable overlapping layers — exactly
    // what the human WD-2 caught and jsdom could not. Assert the stylesheet is
    // actually present in the rendered document.
    const xtermStyled = await page.evaluate(() => {
      for (const sheet of Array.from(document.styleSheets)) {
        let rules: CSSRuleList | null = null;
        try {
          rules = sheet.cssRules;
        } catch {
          continue; // cross-origin sheet; skip
        }
        if (!rules) continue;
        for (const rule of Array.from(rules)) {
          const sel = (rule as CSSStyleRule).selectorText;
          if (sel && sel.includes(".xterm")) return true;
        }
      }
      return false;
    });
    expect(
      xtermStyled,
      "xterm.css must be loaded in the shipped bundle (regression guard for the v0.2.0 latent defect)",
    ).toBe(true);

    // Browser-rendered evidence for the report (satisfies the DoD addendum).
    await testInfo.attach("approved-terminal", {
      body: await page.screenshot(),
      contentType: "image/png",
    });

    await page.getByRole("button", { name: "End session" }).click();
    await expect(page).toHaveURL(/\/sessions$/);
  });

  await test.step("audit timeline renders the governed-egress outcomes", async () => {
    await page.goto("/audit");
    const table = page.getByRole("table");
    // The operator can see the governed wedge in the chain: the approval decision,
    // the audited credential egress, and the operator-driven close. Exact outcome
    // strings mirror tests/e2e/approval_test.go. The producer outboxes relay to
    // audit-service ASYNCHRONOUSLY (the Audit page only fetches on mount / Refresh,
    // it does not poll), so re-click Refresh until the outcomes land — same patience
    // the Go e2e harness applies when polling the chain.
    await expect(async () => {
      await page.getByRole("button", { name: "Refresh" }).click();
      await expect(table).toContainText("approval.granted", { timeout: 5_000 });
      await expect(table).toContainText("approval.denied:operator", { timeout: 5_000 });
      await expect(table).toContainText("credential.leased", { timeout: 5_000 });
      await expect(table).toContainText("closed:operator", { timeout: 5_000 });
    }).toPass({ timeout: 60_000 });
  });
});
