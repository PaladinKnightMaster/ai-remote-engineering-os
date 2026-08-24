// Invocation-approval browser smoke (T-040c) — the operator-facing leg of MCP
// tool-invocation approval (FE-11; ADR-0057 §4 / ADR-0059), proven in a real browser
// against the BUILT apps/web bundle on the same live backend the Go e2e uses (api →
// gateway → policy). It converts FE-11 into a CI gate: the operator pending-invocation
// surface RENDERS a parked invocation (with its command — ADR-0059 informed consent)
// and resolving it works.
//
// Seeding (the "C" strategy): the smoke creates a REAL parked invocation through the
// real path — the operator mints a scoped agent-tool token (POST /v1/agent-tokens),
// then a background agent tools/call parks pending approval (block-and-wait). No
// cross-service DB poking. The browser then DENIES it (deny needs no broker/sshd, so
// this stays fast + deterministic). The full approve→exec chain is the T-041 Go e2e
// capstone; here we own what only a real browser proves: the surface renders + resolves.

import { test, expect, request as pwRequest, type Page } from "@playwright/test";

const FLAGGED_RESOURCE_ID = "01900000-0000-7000-8000-0000000000a2"; // requires_approval=true (seed)
const OPERATOR_USER = "admin";
const OPERATOR_PASS = "e2e-operator-pw";
const GATEWAY_URL = process.env.SMOKE_GATEWAY_URL ?? "http://mcp-gateway:8085";
const COMMAND = "echo INVOKE-SMOKE-OK";

async function login(page: Page) {
  await expect(async () => {
    await page.goto("/login");
    await page.getByLabel("Username").fill(OPERATOR_USER);
    await page.getByLabel("Password").fill(OPERATOR_PASS);
    await page.getByRole("button", { name: /sign in/i }).click();
    await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });
  }).toPass({ timeout: 180_000 });
}

test("operator resolves a parked tool invocation in the browser", async ({ page }, testInfo) => {
  await login(page);

  // Mint a scoped agent-tool token through the operator's authenticated session
  // (human-agent + double-submit CSRF: the SPA echoes the aireos_csrf cookie).
  const csrf = (await page.context().cookies()).find((c) => c.name === "aireos_csrf")?.value ?? "";
  const mint = await page.request.post("/v1/agent-tokens", {
    headers: { "X-CSRF-Token": csrf },
    data: { agentId: "smoke", resourceId: FLAGGED_RESOURCE_ID, operations: ["exec"] },
  });
  expect(mint.ok(), "operator must be able to mint an agent token").toBeTruthy();
  const { token } = await mint.json();

  // Fire a real agent tools/call that PARKS pending approval (block-and-wait). Do not
  // await — it returns only once the operator resolves it. A separate request context
  // (direct Bearer to the gateway, off Caddy) keeps the held call off the page.
  const agent = await pwRequest.newContext({ baseURL: GATEWAY_URL, ignoreHTTPSErrors: true });
  const held = agent
    .post("/mcp", {
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      data: {
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: { name: "remote.exec", arguments: { resourceId: FLAGGED_RESOURCE_ID, command: COMMAND } },
      },
      timeout: 180_000,
    })
    .catch(() => null);

  // The operator surface lists the parked invocation; its command renders verbatim
  // (ADR-0059) — the browser-rendered DoD evidence. The list fetches on mount / Refresh
  // (no poll), so Refresh until the parked row lands (same patience as the audit leg).
  const prompt = page.getByRole("region", { name: "approval required" });
  await expect(async () => {
    await page.goto("/invocations");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(prompt).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });
  await expect(page.getByLabel("command")).toContainText("INVOKE-SMOKE-OK");

  await testInfo.attach("pending-invocation", { body: await page.screenshot(), contentType: "image/png" });

  // Deny → the invocation resolves and drops out of the pending list (refetch).
  await page.getByRole("button", { name: "Deny" }).click();
  await expect(prompt).toHaveCount(0, { timeout: 30_000 });

  // The held agent call unblocks with a tool-error (denied), never an exec result.
  const res = await held;
  if (res) {
    expect(res.ok()).toBeTruthy(); // JSON-RPC tool errors are HTTP 200 with isError
    expect(JSON.stringify(await res.json())).toContain("denied");
  }
  await agent.dispose();
});

// T-143 (FE-43/44/45, ADR-0071 §D3): the file-family leg. Proves, against the
// BUILT bundle in a real browser (jsdom does not satisfy this — CLAUDE.md
// §Definition of done), that a remote.file.write invocation renders with the
// FIXED per-tool copy (not the old hardcoded "run a command" sentence), the
// mutating affordance, and its verbatim §D3 descriptor (path/size/digest/mode)
// under an accurate "file descriptor" label — never a "command" label, and
// never the raw file content (which the gateway never persists — T-141).
test("operator sees a remote.file.write invocation described and marked as mutating", async ({
  page,
}, testInfo) => {
  await login(page);

  const csrf = (await page.context().cookies()).find((c) => c.name === "aireos_csrf")?.value ?? "";
  // D7: file operations require the verb explicitly in scope — "exec" alone
  // (as the first test mints) would be rejected for remote.file.write.
  const mint = await page.request.post("/v1/agent-tokens", {
    headers: { "X-CSRF-Token": csrf },
    data: { agentId: "smoke-file", resourceId: FLAGGED_RESOURCE_ID, operations: ["file.write"] },
  });
  expect(mint.ok(), "operator must be able to mint a file.write-scoped agent token").toBeTruthy();
  const { token } = await mint.json();

  const path = "/tmp/t143-smoke.txt";
  const content = Buffer.from("T-143 SMOKE CONTENT — must never render in the approval prompt").toString(
    "base64",
  );

  const agent = await pwRequest.newContext({ baseURL: GATEWAY_URL, ignoreHTTPSErrors: true });
  const held = agent
    .post("/mcp", {
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      data: {
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: {
          name: "remote.file.write",
          arguments: { resourceId: FLAGGED_RESOURCE_ID, path, content },
        },
      },
      timeout: 180_000,
    })
    .catch(() => null);

  const prompt = page.getByRole("region", { name: "approval required" });
  await expect(async () => {
    await page.goto("/invocations");
    await page.getByRole("button", { name: "Refresh" }).click();
    await expect(prompt).toBeVisible({ timeout: 5_000 });
  }).toPass({ timeout: 60_000 });

  // The fixed description: accurate for a write, not the exec sentence, and
  // plain language — no shouting caps (T-143 review).
  await expect(prompt).toContainText(/requesting to write a file/i);
  await expect(prompt).not.toContainText(/requesting to run a command/i);
  await expect(prompt).not.toContainText(/MUTATES/);
  // The host-changing affordance (FE-43: visually distinguishable) — plain
  // badge text, the emphasis lives in role="status" + styling, not shouting copy.
  await expect(prompt.getByRole("status")).toHaveText("Changes the host");
  // The write consequence (T-143 review): the operator approves a LIMITED
  // review — path/size/digest/mode — never the raw bytes.
  await expect(prompt).toContainText(/approving the path, size, digest and mode/i);
  // The verbatim §D3 descriptor, under the CORRECT label — path + mode, and a
  // sha256 digest, never the raw base64 content.
  const descriptor = page.getByLabel("file descriptor");
  await expect(descriptor).toContainText(path);
  await expect(descriptor).toContainText(/sha256:[0-9a-f]{64}/);
  await expect(descriptor).toContainText(/mode 0[0-7]{3}/);
  await expect(descriptor).not.toContainText("T-143 SMOKE CONTENT");
  await expect(page.getByLabel("command")).toHaveCount(0); // wrong label must not linger

  await testInfo.attach("pending-file-write-invocation", {
    body: await page.screenshot(),
    contentType: "image/png",
  });

  await page.getByRole("button", { name: "Deny" }).click();
  await expect(prompt).toHaveCount(0, { timeout: 30_000 });

  const res = await held;
  if (res) {
    expect(res.ok()).toBeTruthy();
    expect(JSON.stringify(await res.json())).toContain("denied");
  }
  await agent.dispose();
});
