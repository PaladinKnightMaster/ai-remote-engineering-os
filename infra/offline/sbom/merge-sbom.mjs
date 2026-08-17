// merge-sbom.mjs — merge per-binary Go CycloneDX fragments + the node
// fragment + a hand-described Caddy component into ONE CycloneDX 1.6 SBOM
// for the offline core bundle (ADR-0070 D5, T-129b).
//
// Each Go fragment (produced by generate-sbom.sh via `cyclonedx-gomod app`)
// describes exactly ONE shipped binary as its metadata.component — but that
// component's name is the Go MODULE path (e.g. "aireos.local/packages/go/auth"
// for authctl, since authctl's main lives at packages/go/auth/cmd/authctl),
// not the bin/<name> filename the coverage gate checks against. This script
// pulls that component out, renames it to the real bin/ filename, and folds
// each fragment's own dependency libraries into one de-duplicated pool (keyed
// by purl) so a transitive dependency shared by two binaries isn't repeated.
//
// Usage:
//   node merge-sbom.mjs --name <bundle-name> --version <version> \
//     --go-dir <dir of go-<binary>.json fragments> \
//     [--node <node fragment json>] \
//     [--caddy-version <v> --caddy-sha512 <hash>] \
//     --output <path>
import { readFileSync, writeFileSync, readdirSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";

function argVal(flag, required = false) {
  const i = process.argv.indexOf(flag);
  if (i === -1 || i === process.argv.length - 1) {
    if (required) {
      console.error(`merge-sbom: missing required ${flag}`);
      process.exit(2);
    }
    return undefined;
  }
  return process.argv[i + 1];
}

const name = argVal("--name", true);
const version = argVal("--version", true);
const goDir = argVal("--go-dir", true);
const nodeFragPath = argVal("--node");
const caddyVersion = argVal("--caddy-version");
const caddySha512 = argVal("--caddy-sha512");
const outPath = argVal("--output", true);

const componentsByKey = new Map(); // dedup pool for library components
const appComponents = []; // one entry per shipped binary/application

function keyOf(c) {
  return c.purl || `${c.group || ""}/${c.name}@${c.version || ""}`;
}
function addLibrary(c) {
  const k = keyOf(c);
  if (!componentsByKey.has(k)) componentsByKey.set(k, c);
}

// --- Go fragments: one per shipped binary ---------------------------------
const goFiles = readdirSync(goDir)
  .filter((f) => f.startsWith("go-") && f.endsWith(".json"))
  .sort();
if (goFiles.length === 0) {
  console.error(`merge-sbom: no go-*.json fragments found in ${goDir}`);
  process.exit(1);
}
for (const f of goFiles) {
  const binName = f.slice("go-".length, -".json".length);
  const frag = JSON.parse(readFileSync(path.join(goDir, f), "utf8"));
  const mc = frag.metadata && frag.metadata.component;
  if (!mc) {
    console.error(`merge-sbom: ${f} has no metadata.component — malformed fragment`);
    process.exit(1);
  }
  appComponents.push({
    "bom-ref": `binary:${binName}`,
    type: "application",
    name: binName,
    version: mc.version,
    purl: mc.purl,
    properties: [{ name: "aireos:sbom:source-module", value: mc.name }],
  });
  for (const c of frag.components || []) addLibrary(c);
}

// --- Node fragment: apps/web ------------------------------------------------
if (nodeFragPath) {
  const frag = JSON.parse(readFileSync(nodeFragPath, "utf8"));
  const mc = frag.metadata && frag.metadata.component;
  if (mc) {
    appComponents.push({
      "bom-ref": "binary:web",
      type: "application",
      name: "web",
      version: mc.version,
      purl: mc.purl,
      properties: [
        {
          name: "aireos:sbom:source-package",
          value: `${mc.group ? mc.group + "/" : ""}${mc.name}`,
        },
      ],
    });
  }
  for (const c of frag.components || []) addLibrary(c);
}

// --- Caddy: pinned version + the SHA-512 already verified at build time ----
if (caddyVersion) {
  const comp = {
    "bom-ref": "binary:caddy",
    type: "application",
    name: "caddy",
    version: caddyVersion,
    purl: `pkg:golang/github.com/caddyserver/caddy@v${caddyVersion}`,
  };
  if (caddySha512) comp.hashes = [{ alg: "SHA-512", content: caddySha512 }];
  appComponents.push(comp);
}

// --- Deterministic serialNumber -------------------------------------------
// A hash of the component set, not a random UUID: this document ships INSIDE
// a build artifact that ADR-0070 D6 already normalizes (--mtime/--sort/gzip
// -n) so two builds of identical content produce identical tar bytes. A
// random serialNumber here would reintroduce non-determinism at the SBOM
// layer for no benefit — T-130 measures reproducibility, and this keeps that
// question meaningful rather than pre-answering it "no" on a technicality.
// Not a real UUID version/variant nibble, just the CycloneDX-required
// hex-with-dashes shape.
const allKeys = [
  ...appComponents.map((c) => c.purl || c.name),
  ...componentsByKey.keys(),
].sort();
const h = createHash("sha256")
  .update(`${name}|${version}|${allKeys.join(",")}`)
  .digest("hex");
const serial = `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20, 32)}`;

const bom = {
  bomFormat: "CycloneDX",
  specVersion: "1.6",
  serialNumber: `urn:uuid:${serial}`,
  version: 1,
  metadata: {
    // Fixed, not "now" — same determinism rationale as the serialNumber above.
    timestamp: "1970-01-01T00:00:00Z",
    component: { type: "application", "bom-ref": `bundle:${name}`, name, version },
  },
  components: [...appComponents, ...componentsByKey.values()],
};

writeFileSync(outPath, JSON.stringify(bom, null, 2) + "\n");
console.log(
  `merge-sbom: wrote ${outPath} — ${appComponents.length} shipped binaries, ${componentsByKey.size} library components`,
);
