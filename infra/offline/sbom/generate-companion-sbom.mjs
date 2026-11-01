// generate-companion-sbom.mjs — build a CycloneDX 1.6 SBOM for the postgres
// companion from the source-built tree's own build record,
// pgsql/share/aireos/COMPONENTS.json (ADR-0070 D5; ADR-0077 Consequences — the
// package-index parser this replaces died with the package closure).
//
// What the companion ships, and therefore what the SBOM lists:
//   - ONE `postgresql` component (the server and its contrib pgcrypto are one
//     upstream source tarball): version, pkg:generic purl carrying the
//     download_url, and the source tarball's SHA-256 as its hash.
//   - ONE component per BUNDLED library (a library the OS's minimal base lacks,
//     copied into pgsql/lib): its package's deb/rpm purl, package version, the
//     SHA-256 of the file as shipped, and the file's path in the archive
//     (evidence.occurrences[].location) so a gate can open exactly that file.
//   - NOT host-linked libraries: they are not shipped. They are recorded in
//     metadata.properties (aireos:host-lib) so a reader can see what the tree
//     expects the host to provide; they are not components.
//
// This generator reads the builder's record. It is NOT the coverage gate:
// check-coverage.mjs re-derives its expectations from the archive itself
// (the version compiled into bin/postgres, the files actually in pgsql/lib),
// so a wrong record here is caught there rather than echoed back.
//
// Deterministic: no timestamp, no randomness; the serial is a hash of the content.
//
// Usage: node generate-companion-sbom.mjs --components <COMPONENTS.json> --name <n> --output <out>
import { readFileSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";

function argVal(flag) {
  const i = process.argv.indexOf(flag);
  if (i === -1 || i === process.argv.length - 1) {
    console.error(`generate-companion-sbom: missing required ${flag}`);
    process.exit(2);
  }
  return process.argv[i + 1];
}
function fail(msg) {
  console.error(`generate-companion-sbom: ${msg}`);
  process.exit(1);
}

const componentsPath = argVal("--components");
const name = argVal("--name");
const outPath = argVal("--output");

let rec;
try {
  rec = JSON.parse(readFileSync(componentsPath, "utf8"));
} catch (e) {
  fail(`cannot read/parse ${componentsPath}: ${e.message}`);
}

const pg = rec.postgres || {};
if (!/^\d+\.\d+$/.test(pg.version || "")) fail(`postgres.version '${pg.version}' is not MAJOR.MINOR`);
if (!/^[0-9a-f]{64}$/.test(pg.sha256 || "")) fail("postgres.sha256 is not 64 lowercase hex");
if (!/^https:\/\//.test(pg.source_url || "")) fail("postgres.source_url is not an https URL");
const ostag = rec.ostag || "";
const m = ostag.match(/^(ubuntu|rocky)(\d+)$/);
if (!m) fail(`ostag '${ostag}' is not ubuntu<NNNN> or rocky<N> — no purl mapping for it`);
// Package-URL mapping per OS family; the distro qualifier is the OS version.
const pkgType = m[1] === "ubuntu" ? "deb" : "rpm";
const distro = m[1] === "ubuntu" ? `ubuntu-${m[2].slice(0, 2)}.${m[2].slice(2)}` : `rocky-${m[2]}`;

const enc = encodeURIComponent;
const components = [];

components.push({
  "bom-ref": `postgresql@${pg.version}`,
  type: "application",
  name: "postgresql",
  version: pg.version,
  description: "PostgreSQL server built from the upstream source tarball (includes the contrib pgcrypto module)",
  purl: `pkg:generic/postgresql@${pg.version}?download_url=${enc(pg.source_url)}`,
  hashes: [{ alg: "SHA-256", content: pg.sha256 }],
  externalReferences: [{ type: "distribution", url: pg.source_url }],
  properties: [{ name: "aireos:configure-flags", value: pg.configure_flags || "" }],
});

for (const b of Array.isArray(rec.bundled) ? rec.bundled : []) {
  for (const k of ["soname", "package", "package_version", "package_arch"]) {
    if (!b[k]) fail(`a bundled library record lacks '${k}': ${JSON.stringify(b)}`);
  }
  if (!/^[0-9a-f]{64}$/.test(b.sha256 || "")) fail(`bundled ${b.soname}: sha256 is not 64 lowercase hex`);
  const purl = `pkg:${pkgType}/${m[1]}/${enc(b.package)}@${enc(b.package_version)}?arch=${enc(b.package_arch)}&distro=${enc(distro)}`;
  components.push({
    "bom-ref": `${b.package}@${b.package_version}#${b.soname}`,
    type: "library",
    name: b.package,
    version: b.package_version,
    purl,
    hashes: [{ alg: "SHA-256", content: b.sha256 }],
    evidence: { occurrences: [{ location: `pgsql/lib/${b.soname}` }] },
  });
}

const properties = [
  { name: "aireos:ostag", value: ostag },
  { name: "aireos:build-base-image", value: rec.base_image || "" },
];
for (const h of Array.isArray(rec.host_libs) ? rec.host_libs : []) {
  const v = (rec.host_lib_versions || []).find((x) => x.soname === h);
  properties.push({ name: "aireos:host-lib", value: v ? `${h} (requires ${v.min_symbol_version})` : h });
}

const digest = createHash("sha256")
  .update(`${name}|${components.map((c) => c.purl + "|" + c.hashes[0].content).join(",")}`)
  .digest("hex");
const serial = `${digest.slice(0, 8)}-${digest.slice(8, 12)}-${digest.slice(12, 16)}-${digest.slice(16, 20)}-${digest.slice(20, 32)}`;

const bom = {
  bomFormat: "CycloneDX",
  specVersion: "1.6",
  serialNumber: `urn:uuid:${serial}`,
  version: 1,
  metadata: {
    timestamp: "1970-01-01T00:00:00Z", // deterministic; see merge-sbom.mjs for the rationale
    component: { type: "application", "bom-ref": `bundle:${name}`, name, version: "n/a" },
    properties,
  },
  components,
};

writeFileSync(outPath, JSON.stringify(bom, null, 2) + "\n");
console.log(
  `generate-companion-sbom: wrote ${outPath} — postgresql ${pg.version} + ${components.length - 1} bundled librar${components.length - 1 === 1 ? "y" : "ies"} (${pkgType}), ${properties.length - 2} host libraries recorded in metadata.properties`,
);
