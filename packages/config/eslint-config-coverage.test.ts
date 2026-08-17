// VER-12 (T-132) — does every ESLint override block's `files:` glob actually
// match a REAL file under a REAL `eslint .` invocation, or is it silently
// dead the way the SEC-7 block (eslint.config.js, formerly ~line 196) looked
// for its entire life?
//
// This is a DIFFERENT question than eslint-rules.test.ts (T-012) answers.
// That file lints __fixtures__/ files and asserts the SEC-7 *rules* fire —
// it never asks whether a block's `files:` glob resolves against anything
// real, because it always points ESLint straight at the fixture path. The
// dead SEC-7 override passed that fixture test for its entire dead life; it
// is not extended here on purpose (see the T-132 ticket brief) — a fixture
// test and a real-tree-coverage test are different kinds of check, and
// conflating them is exactly how the SEC-7 gap stayed invisible.
//
// "Under each invocation path" (release-criteria VER-12) means: for each of
// the cwds a real `eslint .` actually runs from. Established at the T-132
// consult (verified via each package.json): only THREE workspace packages
// declare a `lint` script — apps/web, packages/ui, packages/shared-types.
// There is no root-level eslint run and packages/config itself has no
// `lint` script (CEO decision, out of scope for this ticket — see the T-132
// report). So the three cwds below are the complete, real set; this test
// does not build a general path-discovery framework for invocations that
// don't exist.
//
// Globbing: uses Node's built-in `fs.globSync` (stable-enough since Node 22,
// this repo's floor per `engines`) — zero new dependency, per the ticket's
// explicit "do NOT add a dependency." No third-party glob library is a
// direct dependency of any workspace package under `strict-peer-dependencies`
// + `node-linker=isolated` (.npmrc), so this is also the only glob engine
// actually reachable from this file without adding one.

import { describe, expect, it } from "vitest";
import { globSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import type { Linter } from "eslint";
import sharedConfig from "./eslint.config.js";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "../..");

// The three real `eslint .` invocation cwds (established fact, see comment
// above) — resolved to absolute paths so glob matching is unambiguous
// regardless of this test file's own cwd.
const LINT_CWDS = ["apps/web", "packages/ui", "packages/shared-types"].map((p) =>
  resolve(repoRoot, p),
);

// "non-fixture, non-node_modules" per the release-criteria wording, plus the
// other build-output directories the shared config itself universally
// ignores (dist/build/.vite) — a match inside any of these wouldn't count as
// a real source file being covered either.
const EXCLUDE_RE =
  /(^|[\\/])(node_modules|dist|build|\.vite|__fixtures__|forbidden-deps\.fixtures)([\\/]|$)/;

function realFileCount(pattern: string, cwd: string): number {
  return globSync(pattern, { cwd }).filter((p) => !EXCLUDE_RE.test(p)).length;
}

// Enumerate the override blocks programmatically from the imported config
// object — do not hardcode the block list or its line numbers (VER-12: "Use
// whatever... iterate it; do not hardcode the block list"). Every block that
// declares a non-empty `files:` array is a candidate; the universal-ignores-
// only entry (index 0) doesn't declare `files:` at all and drops out of the
// filter automatically.
//
// `...tseslint.configs.recommended` (line 40) ALSO spreads in a `files:`-
// bearing block of its own (typescript-eslint's "eslint-recommended" preset,
// scoped to `**/*.ts`/`.tsx`/`.mts`/`.cts`) — spreading flattens it into this
// same top-level array, so a naive `files:`-presence filter can't tell it
// apart from the blocks THIS file hand-authors. It carries a flat-config
// `name` field (`"typescript-eslint/eslint-recommended"`); none of our own
// hand-authored blocks below set `.name`. That's the real, principled
// distinction (not a line-number or index guess) between "an override this
// file wrote" and "a block an imported preset happened to contribute" — so
// filter on it rather than hardcode a count.
const overrideBlocks = (sharedConfig as Linter.Config[]).filter(
  (block) => Array.isArray(block.files) && block.files.length > 0 && !block.name,
);

describe("@aireos/config/eslint.config.js — override files: globs match real files (VER-12)", () => {
  it("enumerates this file's own files:-bearing override blocks (currently five, post-T-132 SEC-7 removal)", () => {
    // Sanity check on the enumeration itself, not a hardcoded assertion
    // about which blocks exist — if a block is added or removed, this
    // number should move and the loop below still names the blocks
    // faithfully (via their own `files:` in the failure message). Five, not
    // six: T-132 deleted the dead-then-proven-unnecessary SEC-7 override
    // (see the comment where it used to live in eslint.config.js).
    expect(overrideBlocks.length).toBe(5);
  });

  overrideBlocks.forEach((block, i) => {
    const patterns = block.files as string[];

    it(`block #${i} (files: ${JSON.stringify(patterns)}) matches a real, non-fixture, non-node_modules file under at least one real lint cwd`, () => {
      const totalMatches = LINT_CWDS.reduce(
        (sum, cwd) => sum + patterns.reduce((s, pattern) => s + realFileCount(pattern, cwd), 0),
        0,
      );
      expect(totalMatches).toBeGreaterThan(0);
    });
  });
});
