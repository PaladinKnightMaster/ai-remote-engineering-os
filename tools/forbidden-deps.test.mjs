// Tests for forbidden-deps.mjs — uses node:test (no extra devDep).
// Run via: `node --test tools/forbidden-deps.test.mjs`.
//
// Asserts the script's exit-code contract (the load-bearing one):
//   0 on clean fixture, non-zero on violating fixture, 0 on missing apps/.

import { strict as assert } from "node:assert";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import test from "node:test";

const here = dirname(fileURLToPath(import.meta.url));
const script = resolve(here, "forbidden-deps.mjs");
const fixturesRoot = resolve(here, "forbidden-deps.fixtures");

function run(baseDir) {
  return spawnSync("node", [script, baseDir], { encoding: "utf8" });
}

test("clean fixture: apps/* without forbidden deps → exit 0", () => {
  const r = run(resolve(fixturesRoot, "clean"));
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstdout:\n${r.stdout}\nstderr:\n${r.stderr}`);
});

test("violating fixture: apps/* with `next` and `@remix-run/react` → exit 1", () => {
  const r = run(resolve(fixturesRoot, "violating"));
  assert.notEqual(r.status, 0, `expected non-zero exit, got ${r.status}\nstdout:\n${r.stdout}`);
  assert.match(r.stderr, /next/, "expected 'next' in stderr");
  assert.match(r.stderr, /@remix-run\//, "expected '@remix-run/' in stderr");
});

test("missing apps/ dir → exit 0 with informative message", () => {
  const r = run(resolve(fixturesRoot, "empty"));
  assert.equal(r.status, 0, `expected exit 0, got ${r.status}\nstdout:\n${r.stdout}`);
  assert.match(r.stdout, /no apps/, "expected 'no apps' message in stdout");
});
