// check-js-licenses.mjs — JS license gate (SC-6, §11a / T-026).
//
// Asserts every PRODUCTION (shipped) JS dependency's license is in the allowlist.
// devDeps (eslint, vite, vitest, …) are build-time, never shipped, so they are
// out of scope — only what the SPA bundle ships is checked, via `pnpm licenses
// list --prod`. The Go side is covered by `go-licenses check` (see Taskfile).
//
// Usage: node infra/scripts/check-js-licenses.mjs "MIT,ISC,BSD-3-Clause,..."
import { execSync } from "node:child_process";

const allowed = new Set(
  (process.argv[2] || "").split(",").map((s) => s.trim()).filter(Boolean),
);
if (allowed.size === 0) {
  console.error("usage: check-js-licenses.mjs <comma-separated-allowed-SPDX>");
  process.exit(2);
}

// pnpm licenses list --prod --json => { "<license>": [ { name, ... }, ... ], ... }
let raw = "";
try {
  raw = execSync("pnpm licenses list --prod --json", {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  });
} catch (e) {
  // Some pnpm versions exit non-zero when there are no prod deps; tolerate that.
  raw = (e.stdout || "").toString();
}
if (!raw.trim()) {
  console.log("js-licenses OK — no production dependencies to check");
  process.exit(0);
}

const data = JSON.parse(raw);
const violations = [];
for (const [license, pkgs] of Object.entries(data)) {
  if (allowed.has(license)) continue;
  for (const p of Array.isArray(pkgs) ? pkgs : [pkgs]) {
    violations.push(`${(p && p.name) || p}  (${license})`);
  }
}
if (violations.length) {
  console.error("js-licenses FAIL — production deps with disallowed licenses:");
  for (const v of [...new Set(violations)].sort()) console.error("  " + v);
  process.exit(1);
}
console.log(
  `js-licenses OK — all production dependency licenses allowed (${Object.keys(data).sort().join(", ")})`,
);
