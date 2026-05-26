// Programmatic test of the shared ESLint config — T-012.
//
// Runs ESLint over the __fixtures__/ files with the shared config and
// asserts the SEC-7 rules fire (or don't) where expected. These tests
// ARE the proof that v0.1.0 release-criteria SEC-7 is enforced; if a
// future refactor breaks a rule, this test fails before the rule
// silently degrades.
//
// Implementation note: the shared config universally ignores
// `**/__fixtures__/**` so the *workspace* lint run does not pick up
// these intentionally-broken files. For the test, we load the shared
// config and strip *just that one* ignore entry, then pass the result
// as `baseConfig` to a fresh ESLint instance (with `overrideConfigFile:
// true` so the eslint.config.js auto-discovery does not re-add it).

import { describe, it, expect } from "vitest";
import { ESLint, type Linter } from "eslint";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import sharedConfig from "./eslint.config.js";

const here = dirname(fileURLToPath(import.meta.url));
const fixturesDir = resolve(here, "__fixtures__");

// Strip the `__fixtures__/` and `forbidden-deps.fixtures/` entries from
// the universal ignore block so the test can lint files that the
// workspace lint correctly skips.
const testConfig: Linter.Config[] = (sharedConfig as Linter.Config[]).map((block) => {
  if (Array.isArray(block?.ignores)) {
    return {
      ...block,
      ignores: block.ignores.filter(
        (g) => !g.includes("__fixtures__") && !g.includes("forbidden-deps.fixtures"),
      ),
    };
  }
  return block;
});

async function lintFixture(name: string) {
  const eslint = new ESLint({
    overrideConfigFile: true,
    baseConfig: testConfig,
  });
  const results = await eslint.lintFiles([resolve(fixturesDir, name)]);
  return results[0]!;
}

describe("@aireos/config/eslint.config.js — SEC-7 enforcement", () => {
  it("flags direct fetch() outside the @aireos/shared-types wrapper", async () => {
    const r = await lintFixture("bad-fetch.tsx");
    const messages = r.messages.filter((m) => m.ruleId === "no-restricted-syntax");
    expect(messages.length).toBeGreaterThanOrEqual(2);
    expect(messages.some((m) => /fetch/.test(m.message))).toBe(true);
    expect(messages.some((m) => /Request/.test(m.message))).toBe(true);
  });

  it("flags localStorage / sessionStorage in apps and UI code", async () => {
    const r = await lintFixture("bad-storage.tsx");
    const messages = r.messages.filter((m) => m.ruleId === "no-restricted-globals");
    expect(messages.some((m) => /localStorage/.test(m.message))).toBe(true);
    expect(messages.some((m) => /sessionStorage/.test(m.message))).toBe(true);
  });

  it("flags dangerouslySetInnerHTML (react/no-danger)", async () => {
    const r = await lintFixture("bad-danger.tsx");
    const messages = r.messages.filter((m) => m.ruleId === "react/no-danger");
    expect(messages.length).toBeGreaterThanOrEqual(1);
  });

  it("does not flag clean, idiomatic React code", async () => {
    const r = await lintFixture("good-app.tsx");
    const errors = r.messages.filter((m) => m.severity === 2);
    expect(errors).toEqual([]);
  });

  it("flags member-expression bypass forms — globalThis.fetch / window.fetch / window.Request", async () => {
    const r = await lintFixture("bad-fetch-bypass.tsx");
    const syntax = r.messages.filter((m) => m.ruleId === "no-restricted-syntax");
    // bypassA (globalThis.fetch), bypassB (window.fetch), bypassC (self.fetch),
    // bypassD (new window.Request) → at least 4 syntax violations.
    expect(syntax.length).toBeGreaterThanOrEqual(4);
    // bypassE (window.localStorage.setItem), bypassF (globalThis.sessionStorage.setItem)
    const props = r.messages.filter((m) => m.ruleId === "no-restricted-properties");
    expect(props.length).toBeGreaterThanOrEqual(2);
  });

  it("flags eval / new Function() / implied-eval / target=_blank without noopener", async () => {
    const r = await lintFixture("bad-eval.tsx");
    const ruleIds = new Set(r.messages.map((m) => m.ruleId));
    expect(ruleIds.has("no-eval")).toBe(true);
    expect(ruleIds.has("no-new-func")).toBe(true);
    expect(ruleIds.has("no-implied-eval")).toBe(true);
    expect(ruleIds.has("react/jsx-no-target-blank")).toBe(true);
  });
});
