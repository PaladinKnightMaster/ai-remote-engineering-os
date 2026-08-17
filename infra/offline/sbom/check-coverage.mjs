// check-coverage.mjs — the "not just presence" half of the provenance gate's
// SBOM check (ADR-0070 D9, VER-11, T-129b). Given one shipped tar.gz and its
// sibling SBOM, proves the SBOM actually COVERS what shipped, rather than
// merely existing (a presence-only check is the S-25 shape this ticket exists
// to avoid):
//
//   - core bundle artifact     -> every bin/<file> entry inside the archive
//                                  appears as a named SBOM component. Catches
//                                  a binary silently dropped from the SBOM
//                                  while still being shipped.
//   - postgres companion       -> the SBOM's component count equals the
//     artifact                    package-stanza count in the artifact's OWN
//                                  repo/Packages index.
//
// Usage: node check-coverage.mjs <artifact.tar.gz> <sbom.json>
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

const [, , artifact, sbomPath] = process.argv;
if (!artifact || !sbomPath) {
  console.error("usage: check-coverage.mjs <artifact.tar.gz> <sbom.json>");
  process.exit(2);
}

let sbom;
try {
  sbom = JSON.parse(readFileSync(sbomPath, "utf8"));
} catch (e) {
  console.error(`check-coverage FAIL — cannot read/parse SBOM ${sbomPath}: ${e.message}`);
  process.exit(1);
}

function collectNames(components, out) {
  for (const c of Array.isArray(components) ? components : []) {
    if (c && typeof c.name === "string") out.add(c.name);
    if (c && Array.isArray(c.components)) collectNames(c.components, out);
  }
}
const names = new Set();
collectNames(sbom.components, names);
if (sbom.metadata && sbom.metadata.component && sbom.metadata.component.name) {
  names.add(sbom.metadata.component.name);
}

function tarList(file) {
  // --force-local is REQUIRED, not cosmetic. GNU tar parses "host:path" as a
  // REMOTE archive spec, so a Windows path like D:/repo/dist/x.tar.gz is read
  // as host "D" — tar then tries to reach a machine called D and fails with
  // "Cannot connect to D: resolve failed". Invisible on Linux/CI (no drive
  // letters), fatal for anyone running the gate on a Windows host, which is
  // this project's own primary dev environment.
  return execFileSync("tar", ["--force-local", "-tzf", file], { encoding: "utf8" })
    .split(/\r?\n/)
    .filter(Boolean);
}

const base = artifact.split(/[\\/]/).pop();

if (base.startsWith("aireos-offline-")) {
  const entries = tarList(artifact);
  const binNames = new Set(
    entries.filter((e) => /\/bin\/[^/]+$/.test(e)).map((e) => e.split("/").pop()),
  );
  if (binNames.size === 0) {
    console.error(
      `check-coverage FAIL — ${base}: found NO bin/ entries inside the archive itself (cannot assert coverage over nothing)`,
    );
    process.exit(1);
  }
  const missing = [...binNames].filter((n) => !names.has(n)).sort();
  if (missing.length) {
    console.error(
      `check-coverage FAIL — ${base}: bin/ executable(s) missing from SBOM components: ${missing.join(", ")}`,
    );
    process.exit(1);
  }
  console.log(
    `check-coverage OK — ${base}: all ${binNames.size} bin/ executables present as SBOM components (${[...binNames].sort().join(", ")})`,
  );
  process.exit(0);
} else if (base.startsWith("aireos-postgres-")) {
  const entries = tarList(artifact);
  const pkgEntry = entries.find((e) => /\/repo\/Packages$/.test(e));
  if (!pkgEntry) {
    console.error(`check-coverage FAIL — ${base}: no repo/Packages found inside the archive`);
    process.exit(1);
  }
  // --force-local for the same reason as tarList above: without it a Windows
  // path is parsed as a remote host:path spec.
  const packagesText = execFileSync("tar", ["--force-local", "-xzf", artifact, "-O", pkgEntry], {
    encoding: "utf8",
    maxBuffer: 1024 * 1024 * 64,
  });
  const pkgCount = (packagesText.match(/^Package:/gm) || []).length;
  const sbomCount = (sbom.components || []).length;
  if (pkgCount === 0) {
    console.error(`check-coverage FAIL — ${base}: repo/Packages inside the archive has 0 entries`);
    process.exit(1);
  }
  if (sbomCount !== pkgCount) {
    console.error(
      `check-coverage FAIL — ${base}: SBOM has ${sbomCount} component(s) but repo/Packages inside the archive has ${pkgCount} entries — coverage mismatch`,
    );
    process.exit(1);
  }
  console.log(
    `check-coverage OK — ${base}: SBOM component count (${sbomCount}) matches repo/Packages entry count (${pkgCount})`,
  );
  process.exit(0);
} else {
  console.error(
    `check-coverage FAIL — ${base}: unrecognized artifact naming — cannot determine which coverage rule applies`,
  );
  process.exit(1);
}
