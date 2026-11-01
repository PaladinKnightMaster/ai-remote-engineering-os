// check-coverage.mjs — the "not just presence" half of the provenance gate's
// SBOM check (ADR-0070 D9, VER-11, T-129b; INS-7, T-172b). Given one shipped
// tar.gz and its sibling SBOM, proves the SBOM actually COVERS what shipped,
// rather than merely existing (a presence-only check is the S-25 shape):
//
//   - core bundle artifact     -> every bin/<file> entry inside the archive
//                                  appears as a named SBOM component. Catches
//                                  a binary silently dropped from the SBOM
//                                  while still being shipped.
//   - postgres companion       -> INS-7 (ADR-0077). Every expectation is read
//     artifact                    from the ARCHIVE'S OWN CONTENTS, never from the
//                                  generator's input: a gate that compares a
//                                  generator with its own input proves
//                                  consistency, not correctness.
//                                  (a) the PostgreSQL version COMPILED INTO the
//                                      shipped pgsql/bin/postgres equals the
//                                      SBOM's `postgresql` component version;
//                                  (b) every file/symlink under pgsql/lib/ that
//                                      is not a PostgreSQL-built object (the
//                                      committed allowlist below) is an SBOM
//                                      library component — an unknown file
//                                      FAILS CLOSED;
//                                  (c) every SBOM library component names a
//                                      regular file that is in the archive and
//                                      whose bytes hash to the component's hash
//                                      (no phantom components);
//                                  (d) an archive without pgsql/bin/postgres is
//                                      refused.
//
// Usage: node check-coverage.mjs <artifact.tar.gz> <sbom.json>
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";

// ---- INS-7(b): what counts as "PostgreSQL-built" under pgsql/lib/ -----------
// Derived ONCE from real trees [ran: 2026-11-01, find lib -type f -o -type l over
// dist/offline/aireos-pgsql-{ubuntu2404,ubuntu2604,rocky9}/ — the three sets are
// identical, 32 entries]. PostgreSQL 16.15's own build installs, directly in lib/:
//   libpq.so, libpq.so.5, libpq.so.5.<minor>   (the client library + its soname links)
//   22 encoding-conversion modules             (*_and_*.so, listed by name)
//   dict_snowball.so libpqwalreceiver.so pgcrypto.so pgoutput.so plpgsql.so
// Anything else in lib/ (a bundled OpenSSL, a planted .so) is NOT PostgreSQL's and must
// be an SBOM component. EXACT names, not globs: a pattern like `utf8_and_*.so` would also
// admit `utf8_and_evil.so`. A new PostgreSQL release that adds a module fails here until
// the module is added below ON PURPOSE — failing closed is the point. A nested path never
// matches (every pattern is anchored and slash-free).
const PG_BUILT_LIB = [
  /^libpq\.so(\.5(\.\d+)?)?$/,
  new RegExp(
    "^(" +
      [
        "cyrillic_and_mic", "dict_snowball", "euc2004_sjis2004", "euc_cn_and_mic", "euc_jp_and_sjis",
        "euc_kr_and_mic", "euc_tw_and_big5", "latin2_and_win1250", "latin_and_mic", "libpqwalreceiver",
        "pgcrypto", "pgoutput", "plpgsql", "utf8_and_big5", "utf8_and_cyrillic", "utf8_and_euc2004",
        "utf8_and_euc_cn", "utf8_and_euc_jp", "utf8_and_euc_kr", "utf8_and_euc_tw", "utf8_and_gb18030",
        "utf8_and_gbk", "utf8_and_iso8859", "utf8_and_iso8859_1", "utf8_and_johab", "utf8_and_sjis",
        "utf8_and_sjis2004", "utf8_and_uhc", "utf8_and_win",
      ].join("|") +
      ")\\.so$",
  ),
];
const isPgBuilt = (relToLib) => PG_BUILT_LIB.some((re) => re.test(relToLib));

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
  return execFileSync("tar", ["--force-local", "-tzf", file], { encoding: "utf8", maxBuffer: 1024 * 1024 * 64 })
    .split(/\r?\n/)
    .filter(Boolean);
}

// Typed listing: [{type, path, target}]. type is tar's own first character:
// '-' regular file, 'l' symlink, 'h' hard link, 'd' directory. The type matters
// for the companion gate — a symlink in lib/ is still a shipped file.
function tarListTyped(file) {
  const out = execFileSync("tar", ["--force-local", "-tvzf", file], { encoding: "utf8", maxBuffer: 1024 * 1024 * 64 });
  const entries = [];
  for (const line of out.split(/\r?\n/).filter(Boolean)) {
    const m = line.match(/^(.)\S+\s+\S+\s+\d+\s+\d{4}-\d\d-\d\d \d\d:\d\d (.+)$/);
    if (!m) throw new Error(`cannot parse tar listing line: ${line}`);
    let path = m[2];
    let target = null;
    if (m[1] === "l") {
      const i = path.indexOf(" -> ");
      if (i !== -1) { target = path.slice(i + 4); path = path.slice(0, i); }
    } else if (m[1] === "h") {
      const i = path.indexOf(" link to ");
      if (i !== -1) { target = path.slice(i + 9); path = path.slice(0, i); }
    }
    entries.push({ type: m[1], path: path.replace(/^\.\//, ""), target });
  }
  return entries;
}

function tarMember(file, member) {
  return execFileSync("tar", ["--force-local", "-xzf", file, "-O", member], {
    maxBuffer: 1024 * 1024 * 512,
  });
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
  const fail = (msg) => {
    console.error(`check-coverage FAIL — ${base}: ${msg}`);
    process.exit(1);
  };
  const entries = tarListTyped(artifact);

  // (d) the server binary must be in the archive, or there is nothing to assert against.
  const pgBin = entries.find((e) => e.type === "-" && /^[^/]+\/pgsql\/bin\/postgres$/.test(e.path));
  if (!pgBin) fail("no regular file pgsql/bin/postgres inside the archive — not a PostgreSQL companion, nothing to assert coverage over");
  const top = pgBin.path.split("/")[0];

  // (a) the version compiled into the shipped server binary (PG_VERSION_STR:
  // "PostgreSQL 16.15 on x86_64-pc-linux-gnu, compiled by ..."), read from the archive.
  const binText = tarMember(artifact, pgBin.path).toString("latin1");
  const compiled = new Set([...binText.matchAll(/PostgreSQL (\d+\.\d+) on [A-Za-z0-9_]+-/g)].map((m) => m[1]));
  if (compiled.size !== 1) {
    fail(`could not read exactly one compiled-in PostgreSQL version from pgsql/bin/postgres (found: ${compiled.size ? [...compiled].join(", ") : "none"})`);
  }
  const compiledVersion = [...compiled][0];

  const comps = Array.isArray(sbom.components) ? sbom.components : [];
  const pgComps = comps.filter((c) => c && c.name === "postgresql");
  if (pgComps.length !== 1) {
    fail(`the SBOM has ${pgComps.length} \`postgresql\` components; the shipped pgsql/bin/postgres is PostgreSQL ${compiledVersion} and the SBOM must name it exactly once`);
  }
  const pg = pgComps[0];
  if (pg.version !== compiledVersion) {
    fail(`SBOM \`postgresql\` component is version ${pg.version}, but the pgsql/bin/postgres in the archive is PostgreSQL ${compiledVersion}`);
  }
  if (!String(pg.purl || "").startsWith(`pkg:generic/postgresql@${compiledVersion}`)) {
    fail(`SBOM \`postgresql\` purl '${pg.purl}' does not carry version ${compiledVersion}`);
  }
  if (!(pg.hashes || []).some((h) => h.alg === "SHA-256" && /^[0-9a-f]{64}$/.test(h.content))) {
    fail("SBOM `postgresql` component has no SHA-256 hash");
  }

  // (c) every other component must be a library naming a regular file that is really in the archive.
  const libPrefix = `${top}/pgsql/lib/`;
  const inArchive = new Map(entries.map((e) => [e.path, e]));
  const covered = new Set();
  for (const c of comps) {
    if (c === pg) continue;
    if (c.type !== "library") fail(`unexpected SBOM component '${c.name}' of type '${c.type}' — a companion SBOM holds the postgresql component and bundled libraries only`);
    const loc = c.evidence && c.evidence.occurrences && c.evidence.occurrences[0] && c.evidence.occurrences[0].location;
    if (!loc || !loc.startsWith("pgsql/lib/") || loc.includes("..")) {
      fail(`SBOM library component '${c.name}' does not name a file under pgsql/lib/ (evidence.occurrences[0].location = ${JSON.stringify(loc)})`);
    }
    const member = `${top}/${loc}`;
    const e = inArchive.get(member);
    if (!e) fail(`SBOM library component '${c.name}' (${loc}) names a file that is NOT in the archive — phantom component`);
    if (e.type !== "-") fail(`SBOM library component '${c.name}' (${loc}) names a ${e.type === "l" ? "symlink" : "non-regular file"}, not a regular file whose bytes can be hashed`);
    const want = ((c.hashes || []).find((h) => h.alg === "SHA-256") || {}).content;
    const got = createHash("sha256").update(tarMember(artifact, member)).digest("hex");
    if (want !== got) fail(`SBOM library component '${c.name}' (${loc}) hash ${want} does not match the file in the archive (${got})`);
    covered.add(member);
  }

  // (b) every non-PostgreSQL-built file or symlink under pgsql/lib/ must be covered.
  const libFiles = entries.filter((e) => e.type !== "d" && e.path.startsWith(libPrefix));
  const uncovered = libFiles
    .filter((e) => !isPgBuilt(e.path.slice(libPrefix.length)) && !covered.has(e.path))
    .map((e) => e.path.slice(top.length + 1))
    .sort();
  if (uncovered.length) {
    fail(`pgsql/lib/ ships file(s) that are neither PostgreSQL-built (allowlist in check-coverage.mjs) nor SBOM components: ${uncovered.join(", ")}`);
  }

  console.log(
    `check-coverage OK — ${base}: SBOM \`postgresql\` ${pg.version} = the version compiled into the archive's pgsql/bin/postgres; ${covered.size} bundled librar${covered.size === 1 ? "y" : "ies"} covered and hash-matched; all ${libFiles.length} pgsql/lib/ entries are accounted for (PostgreSQL-built allowlist or SBOM component)`,
  );
  process.exit(0);
} else {
  console.error(
    `check-coverage FAIL — ${base}: unrecognized artifact naming — cannot determine which coverage rule applies`,
  );
  process.exit(1);
}
