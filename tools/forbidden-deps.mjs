#!/usr/bin/env node
// forbidden-deps.mjs — fail loud if any apps/*/package.json declares a
// meta-framework dependency forbidden by ADR-0047 §Verification §1.
//
// Closes T-012's second deliverable; closes v0.1.0 release-criteria FE-1
// support clause ("no meta-framework deps in apps/*").
//
// Usage:
//   node tools/forbidden-deps.mjs                # checks ./apps/
//   node tools/forbidden-deps.mjs <base-dir>     # checks <base-dir>/apps/
//
// Exit code 0 = clean. Exit code 1 = at least one violation; offenders
// printed to stderr. The exit code is the load-bearing contract — CI
// reads it.

import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const FORBIDDEN = [
  "next",
  "@remix-run/", // glob — any @remix-run/* dep
  "@sveltejs/kit",
  "astro",
  "nuxt",
  "solid-js",
  "@tanstack/react-start",
];

function isForbidden(dep) {
  for (const f of FORBIDDEN) {
    if (f.endsWith("/")) {
      if (dep.startsWith(f)) return f;
    } else if (dep === f) {
      return f;
    }
  }
  return null;
}

function collectAppPackageJsons(baseDir) {
  const appsDir = join(baseDir, "apps");
  if (!existsSync(appsDir) || !statSync(appsDir).isDirectory()) return [];
  return readdirSync(appsDir)
    .map((name) => join(appsDir, name, "package.json"))
    .filter((p) => existsSync(p));
}

function checkPackageJson(file) {
  let pkg;
  try {
    pkg = JSON.parse(readFileSync(file, "utf8"));
  } catch (err) {
    // Surface malformed JSON with a clear file-scoped message instead of
    // a bare stack trace. Treat as a violation (release-block) — a broken
    // package.json is its own class of badness.
    return [{ file, block: "parse", dep: "<malformed JSON>", match: String(err) }];
  }
  if (pkg === null || typeof pkg !== "object" || Array.isArray(pkg)) {
    return [{ file, block: "parse", dep: "<not an object>", match: typeof pkg }];
  }
  const blocks = ["dependencies", "devDependencies", "peerDependencies", "optionalDependencies"];
  const violations = [];
  for (const block of blocks) {
    const deps = pkg[block];
    if (deps === undefined || deps === null) continue;
    if (typeof deps !== "object" || Array.isArray(deps)) {
      violations.push({ file, block, dep: "<wrong shape>", match: typeof deps });
      continue;
    }
    for (const dep of Object.keys(deps)) {
      const match = isForbidden(dep);
      if (match) violations.push({ file, block, dep, match });
    }
  }
  return violations;
}

function main(argv) {
  const baseDir = resolve(argv[2] ?? ".");
  const files = collectAppPackageJsons(baseDir);
  if (files.length === 0) {
    // No apps directory — nothing to check; consider clean.
    console.log(`forbidden-deps: no apps/*/package.json under ${baseDir}; OK.`);
    return 0;
  }
  let violations = [];
  for (const file of files) {
    violations = violations.concat(checkPackageJson(file));
  }
  if (violations.length === 0) {
    console.log(
      `forbidden-deps: ${files.length} apps/*/package.json checked; no forbidden meta-framework deps (ADR-0047 §Verification §1).`,
    );
    return 0;
  }
  for (const v of violations) {
    console.error(
      `forbidden-deps: ${v.file}: ${v.block} contains "${v.dep}" — forbidden by ADR-0047 (matched "${v.match}").`,
    );
  }
  console.error(`forbidden-deps: ${violations.length} violation(s); failing.`);
  return 1;
}

process.exit(main(process.argv));
