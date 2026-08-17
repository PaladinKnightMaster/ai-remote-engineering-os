// VER-13 (T-132) — do the config scan roots this repo declares actually
// cover what they claim to, on the REAL tree? Three roots, three failure
// modes a config-shaped file can hide:
//
//   1. tsconfig `include` roots (apps/web, packages/ui, packages/shared-types)
//      — each package's tsconfig.json names directories/files it claims to
//      type-check. If a root stops resolving (moved dir, typo) `tsc` just
//      quietly type-checks less than advertised; nothing fails loud.
//   2. The Tailwind v4 `@source` root in apps/web/src/styles.css — T-116
//      found this exact class of gap once already (packages/ui/src wasn't
//      scanned at all; see that file's own comment). This test is the
//      regression guard T-116 didn't get.
//   3. packages/config/tailwind.config.ts's `content:` array — deleted at
//      T-132 rather than bound to a test; see that file's own comment for
//      why binding it would have asserted a false equivalence (v3 `content`
//      globs and v4's `@source`/auto-detection aren't the same mechanism).
//      Nothing to assert here as a result — noted so a reader doesn't go
//      looking for a third root-check and wonder why it's missing.

import { describe, expect, it } from "vitest";
import { existsSync, globSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "../..");

type TsconfigIncludeCase = {
  pkg: string;
  tsconfigPath: string;
};

const TSCONFIG_CASES: TsconfigIncludeCase[] = [
  { pkg: "apps/web", tsconfigPath: "apps/web/tsconfig.json" },
  { pkg: "packages/ui", tsconfigPath: "packages/ui/tsconfig.json" },
  { pkg: "packages/shared-types", tsconfigPath: "packages/shared-types/tsconfig.json" },
];

describe("tsconfig `include` roots resolve and cover real files (VER-13)", () => {
  for (const { pkg, tsconfigPath } of TSCONFIG_CASES) {
    const absTsconfigPath = resolve(repoRoot, tsconfigPath);
    const pkgDir = dirname(absTsconfigPath);

    it(`${tsconfigPath} exists and declares a non-empty "include"`, () => {
      expect(existsSync(absTsconfigPath)).toBe(true);
      const parsed = JSON.parse(readFileSync(absTsconfigPath, "utf-8")) as {
        include?: string[];
      };
      expect(Array.isArray(parsed.include)).toBe(true);
      expect(parsed.include!.length).toBeGreaterThan(0);
    });

    it(`${tsconfigPath}'s every "include" entry resolves to at least one real file under ${pkg}`, () => {
      const parsed = JSON.parse(readFileSync(absTsconfigPath, "utf-8")) as {
        include: string[];
      };
      for (const entry of parsed.include) {
        // Two include shapes appear in this repo: a glob ("src/**/*") and a
        // literal path to one file ("vite.config.ts"). Both are checked the
        // same way in the end — does the pattern resolve to a real file? —
        // fs.globSync handles a literal path exactly like a single-match glob.
        const matches = globSync(entry, { cwd: pkgDir });
        expect(
          matches.length,
          `"${entry}" in ${tsconfigPath} matched zero real files under ${pkg}`,
        ).toBeGreaterThan(0);
      }
    });
  }
});

describe("apps/web/src/styles.css's Tailwind @source root resolves to real .tsx files (VER-13)", () => {
  const cssPath = resolve(repoRoot, "apps/web/src/styles.css");
  const cssDir = dirname(cssPath);
  const css = readFileSync(cssPath, "utf-8");
  // Matches: @source "../../../packages/ui/src";
  const sourceMatch = css.match(/@source\s+"([^"]+)"/);

  it("declares exactly one @source directive", () => {
    expect(sourceMatch).not.toBeNull();
  });

  it("the @source root resolves to a real directory containing .tsx files", () => {
    const root = sourceMatch![1];
    const absRoot = resolve(cssDir, root);
    expect(existsSync(absRoot), `@source root "${root}" -> ${absRoot} does not exist`).toBe(true);
    const tsxFiles = globSync("**/*.tsx", { cwd: absRoot }).filter(
      (p) => !/(^|[\\/])(node_modules|dist|build|\.vite)([\\/]|$)/.test(p),
    );
    expect(
      tsxFiles.length,
      `@source root "${root}" -> ${absRoot} contains no .tsx files`,
    ).toBeGreaterThan(0);
  });
});
