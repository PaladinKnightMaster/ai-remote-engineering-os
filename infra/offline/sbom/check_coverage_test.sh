#!/usr/bin/env bash
# check_coverage_test.sh — rule 12 for the INS-7 gate: check-coverage.mjs's companion branch is shown
# PASSING on the real artifact and on an OpenSSL-bundled control, and FAILING — naming the cause — on
# each mutation below. A coverage gate never seen failing is not evidence (ADR-0070 D9).
#
# Usage: bash infra/offline/sbom/check_coverage_test.sh [companion.tar.gz]
#   default: dist/offline/aireos-postgres-ubuntu2404.tar.gz (+ its .sbom.json), built by
#   build-postgres-companions.sh. Needs node, tar, gzip, and Docker + dist/pgsql-work/ctl-ubuntu2404
#   (T-171's OpenSSL-bundled control tree; `task pgsql-trees-smoke -- ubuntu2404` regenerates it) for
#   the bundled control. Works on scratch copies under dist/check-coverage-test/ only: the real
#   artifacts are read, never written. Archives are mutated WITHOUT extracting them (gunzip + tar -r/--delete).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
GATE="$HERE/check-coverage.mjs"
REAL="${1:-$REPO_ROOT/dist/offline/aireos-postgres-ubuntu2404.tar.gz}"
OS="$(basename "$REAL" .tar.gz)"; OS="${OS#aireos-postgres-}"; TOP="aireos-postgres-$OS"
CTL="$REPO_ROOT/dist/pgsql-work/ctl-$OS"
SCR="$REPO_ROOT/dist/check-coverage-test"
log() { echo "[check-coverage-test] $*"; }
die() { echo "[check-coverage-test] FAIL: $*" >&2; exit 1; }
[ -f "$REAL" ] && [ -f "$REAL.sbom.json" ] || die "need $REAL and $REAL.sbom.json (run build-postgres-companions.sh)"
[ -f "$CTL/share/aireos/COMPONENTS.json" ] || die "no bundled control tree at $CTL (task pgsql-trees-smoke -- $OS)"
rm -rf "$SCR"; mkdir -p "$SCR"; trap 'rm -rf "$SCR"' EXIT

pass() {   # <what> <archive> <sbom>: the gate must pass
  local out; out="$(node "$GATE" "$2" "$3" 2>&1)" || { echo "$out" >&2; die "CONTROL INVALID: '$1' should PASS"; }
  log "PASS ok ($1): $out"
}
fails() {  # <what> <needle> <archive> <sbom>: the gate must fail AND say <needle>
  local out rc=0; out="$(node "$GATE" "$3" "$4" 2>&1)" || rc=$?
  [ "$rc" -ne 0 ] || die "GATE CANNOT FAIL: '$1' still passed: $out"
  grep -qF -- "$2" <<<"$out" || { echo "$out" >&2; die "'$1' failed but did not say '$2'"; }
  log "FAIL ok ($1): rc=$rc — $out"
}
sbom_mut() {   # <src> <dst> <js body mutating b>
  node -e 'const fs=require("fs");const b=JSON.parse(fs.readFileSync(process.argv[1]));'"$3"';fs.writeFileSync(process.argv[2],JSON.stringify(b,null,2));' "$1" "$2"
}

# --- the clean passes ---------------------------------------------------------------------------
pass "real $OS companion" "$REAL" "$REAL.sbom.json"
PGSQL_TREE="$CTL" OUT_DIR="$SCR/bundled" bash "$HERE/../postgres/build-postgres-companions.sh" "$OS" >"$SCR/bundled-build.log" 2>&1 \
  || { tail -15 "$SCR/bundled-build.log" >&2; die "could not package the OpenSSL-bundled control"; }
B="$SCR/bundled/$TOP.tar.gz"
grep -q 'covered and hash-matched' "$SCR/bundled-build.log" && grep -qE '[1-9][0-9]* bundled librar' "$SCR/bundled-build.log" \
  || die "the bundled control did not exercise the bundled-library path"
pass "OpenSSL-bundled control (generator path with bundled components)" "$B" "$B.sbom.json"

# --- (i) postgresql component deleted -------------------------------------------------------------
sbom_mut "$REAL.sbom.json" "$SCR/i.json" 'b.components=b.components.filter(c=>c.name!=="postgresql")'
fails "(i) postgresql component deleted" "0 \`postgresql\` components" "$REAL" "$SCR/i.json"
# --- (ii) version changed to 16.14 -----------------------------------------------------------------
sbom_mut "$REAL.sbom.json" "$SCR/ii.json" 'b.components.find(c=>c.name==="postgresql").version="16.14"'
fails "(ii) postgresql version 16.14" "is version 16.14" "$REAL" "$SCR/ii.json"
# --- (iii) an extra, unknown pgsql/lib/libplanted.so.1 repacked in -----------------------------------
gzip -dc "$REAL" >"$SCR/iii.tar"; mkdir "$SCR/plant"; printf 'not postgres\n' >"$SCR/plant/libplanted.so.1"
tar --force-local -rf "$SCR/iii.tar" --owner=0 --group=0 --numeric-owner --transform "s,^,$TOP/pgsql/lib/," -C "$SCR/plant" libplanted.so.1
gzip -n -c "$SCR/iii.tar" >"$SCR/iii.tar.gz"; cp "$REAL.sbom.json" "$SCR/iii.tar.gz.sbom.json"
cp "$SCR/iii.tar.gz" "$SCR/aireos-postgres-planted.tar.gz"
fails "(iii) planted pgsql/lib/libplanted.so.1" "pgsql/lib/libplanted.so.1" "$SCR/aireos-postgres-planted.tar.gz" "$SCR/iii.tar.gz.sbom.json"
# --- (iv) a phantom bundled component ------------------------------------------------------------------
sbom_mut "$REAL.sbom.json" "$SCR/iv.json" 'b.components.push({"bom-ref":"x","type":"library","name":"libphantom9","version":"1","purl":"pkg:deb/ubuntu/libphantom9@1","hashes":[{alg:"SHA-256",content:"0".repeat(64)}],evidence:{occurrences:[{location:"pgsql/lib/libphantom.so.9"}]}})'
fails "(iv) phantom bundled component" "phantom component" "$REAL" "$SCR/iv.json"
# --- (v) the bundled control with one bundled component removed ------------------------------------------
sbom_mut "$B.sbom.json" "$SCR/v.json" 'b.components=b.components.filter(c=>!(c.evidence&&c.evidence.occurrences[0].location==="pgsql/lib/libssl.so.3"))'
fails "(v) bundled control, libssl.so.3 component removed" "pgsql/lib/libssl.so.3" "$B" "$SCR/v.json"
# --- extras: a wrong hash, an archive with no server binary, the v0.16.0 package-closure companion -------
sbom_mut "$B.sbom.json" "$SCR/vi.json" 'b.components.find(c=>c.type==="library").hashes[0].content="f".repeat(64)'
fails "(vi) bundled component with a wrong hash" "does not match the file in the archive" "$B" "$SCR/vi.json"
cp "$SCR/iii.tar" "$SCR/vii.tar"; tar --force-local --delete -f "$SCR/vii.tar" "$TOP/pgsql/bin/postgres"
gzip -n -c "$SCR/vii.tar" >"$SCR/aireos-postgres-nobin.tar.gz"
fails "(vii) archive without pgsql/bin/postgres" "no regular file pgsql/bin/postgres" "$SCR/aireos-postgres-nobin.tar.gz" "$REAL.sbom.json"
OLD="$REPO_ROOT/dist/v0.16.0-release/aireos-postgres-ubuntu2404.tar.gz"
if [ -f "$OLD" ]; then   # read-only use of the preserved release artifact
  fails "(viii) the v0.16.0 package-closure companion" "no regular file pgsql/bin/postgres" "$OLD" "$OLD.sbom.json"
else log "(viii) skipped: $OLD not present"; fi
log "ALL CHECK-COVERAGE CONTROLS OBSERVED (2 passes, 8 failures each naming its cause)"
