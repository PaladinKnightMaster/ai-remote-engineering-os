#!/usr/bin/env bash
# sec45_bundle_mutation_test.sh — SEC-45, the SECOND of GATE-3's two negative
# proofs for v0.13.0 (the first, SEC-43 chain forgery, landed at T-127b):
# mutate exactly ONE byte in a signed, shipped release artifact -> signature
# verification MUST fail. Criteria: SEC-45, VER-16 (a negative-proof test
# must itself be proven able to fail), VER-6/G1 (live, no mocks). Exercises
# the VER-11 provenance gate (check-provenance.sh). Binding spec: ADR-0070
# D1 (both artifacts signed), D2 (openssl verification), D9 (the gate).
#
# Runs on the HOST, no container: only bash, openssl, node, and coreutils
# (dd/od/cmp/stat) are needed, all already required elsewhere in this repo's
# offline tooling (check-provenance.sh itself needs openssl+node).
#
# Design, point by point (each exists because a prior ticket got it wrong):
#
#  - BOTH shipped artifacts (ADR-0070 D1: the core bundle AND the postgres
#    companion, which is the one that runs install-postgres.sh AS ROOT).
#  - Per-artifact scratch dir, ONE artifact per dir, so check-provenance.sh
#    (which scans every *.tar.gz in OUT_DIR) can only ever report on the one
#    artifact under test, and the FAIL line can be asserted unambiguously.
#  - CONTROL FIRST, on the COPIES: before any mutation, the unmodified copy
#    is proven to verify clean via raw openssl AND via check-provenance.sh,
#    on the identical bytes and the identical code path the mutation proof
#    later uses. A negative proof that could never have passed proves
#    nothing (the T-127b lesson).
#  - The mutation is proven REAL, not assumed: the byte actually read at the
#    target offset is XORed with 0xFF (guaranteed to differ -- x ^ 0xFF is
#    never equal to x for any byte value) rather than blindly writing a
#    fixed value that might already be there. Then cmp -l is used to assert
#    EXACTLY ONE byte differs from the original, AND that file size is
#    unchanged -- proving a mutation, not a truncation.
#  - The offset is filesize/2 -- deep in the compressed payload, never the
#    gzip header.
#  - The SIGNATURE axis is asserted SPECIFICALLY, separate from any SBOM/tar
#    failure a corrupted .tar.gz also triggers (check-coverage.mjs shells
#    out to `tar -tzf`, which can fail independently on a corrupt stream).
#    Asserting only "check-provenance.sh exited non-zero" would let a *tar*
#    failure masquerade as a *signature* proof -- the exact axis-conflation
#    defect that cost T-127b a wd1 run. So this script asserts, separately:
#      1. raw `openssl pkeyutl -verify` FAILS on the mutated copy, and
#      2. check-provenance.sh exits non-zero AND its output contains the
#         literal "<artifact> signature does NOT verify" line.
#    Both are POSITIVE markers (an actual string/exit code asserted), never
#    merely the absence of a success line.
#  - dist/offline/ is NEVER touched: every read of the real artifacts is a
#    `cp` into a scratch dir; every mutation happens on the copy. A trap
#    removes all scratch dirs on exit (disk is tight on this host).
#  - Byte mutation is done via `node` (not bash printf \xHH escapes -- this
#    host's git-bash printf builtin was observed to mis-handle \x escape
#    sequences combined with variable expansion, so a Node one-liner doing
#    a plain buffer XOR-and-write is used instead: simpler and unambiguous).
#
# This is a NEGATIVE PROOF, not a dev-loop gate: unlike check-provenance.sh
# (which self-skips loudly when nothing has been built yet -- a legitimate,
# different case), THIS script dies loudly if the real artifacts or the
# fixture public key are missing. Grafting a self-skip onto a negative proof
# is precisely the S-25 dead-guardrail pattern this whole release exists to
# hunt.
#
# Usage:
#   bash infra/offline/sec45_bundle_mutation_test.sh
# Needs dist/offline/{aireos-offline-<ver>.tar.gz,aireos-postgres-*.tar.gz}
# already built AND signed (task offline-bundle && task sign-release --
# --key <fixture-or-real-key>), and the fixture public key at
# infra/offline/testdata/fixture-release-key/FIXTURE-DO-NOT-USE-release.pub.pem.
#
# VER-16 self-test (fail-proof) mode: set SEC45_TEST_SKIP_MUTATION=1 to make
# the "mutation" step a no-op (write the SAME byte back) for BOTH artifacts,
# which must make this script FAIL -- and fail specifically on the signature
# assertions, not the mutation-integrity ones. NEVER set this in a real run;
# it exists only so this script's own ability to fail can be demonstrated.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DIST_DIR="$REPO_ROOT/dist/offline"
FIXTURE_PUB="$REPO_ROOT/infra/offline/testdata/fixture-release-key/FIXTURE-DO-NOT-USE-release.pub.pem"
CHECK_PROVENANCE="$SCRIPT_DIR/check-provenance.sh"

pass=0
fail=0

assert_true() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "PASS $name"; pass=$((pass + 1))
  else
    echo "FAIL $name"; fail=$((fail + 1))
  fi
}

assert_false() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "FAIL $name -- expected a non-zero exit, got 0"; fail=$((fail + 1))
  else
    echo "PASS $name"; pass=$((pass + 1))
  fi
}

assert_eq() {
  local name="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    echo "PASS $name -- '$got'"; pass=$((pass + 1))
  else
    echo "FAIL $name -- got '$got', want '$want'"; fail=$((fail + 1))
  fi
}

assert_contains() {
  local name="$1" haystack="$2" needle="$3"
  if printf '%s\n' "$haystack" | grep -qF "$needle"; then
    echo "PASS $name"; pass=$((pass + 1))
  else
    echo "FAIL $name -- did not find '$needle' in output"; fail=$((fail + 1))
    echo "  --- captured output ---"; printf '%s\n' "$haystack" | sed 's/^/  | /'
  fi
}

[ -f "$FIXTURE_PUB" ] || { echo "ERROR: fixture public key not found at $FIXTURE_PUB -- this is a negative proof, it must not self-skip"; exit 1; }

SCRATCH_DIRS=()
cleanup() {
  local d
  for d in "${SCRATCH_DIRS[@]:-}"; do
    [ -n "$d" ] && rm -rf "$d"
  done
}
trap cleanup EXIT

# node one-liner: read the byte at OFFSET, XOR it with 0xFF (unless SKIP is
# "1", in which case write the same byte back -- a no-op mutation used only
# by the VER-16 fail-proof leg), write it back, print "<orig> <new>".
mutate_one_byte() {
  local file="$1" offset="$2" skip="$3"
  node -e '
    const fs = require("fs");
    const file = process.argv[1];
    const offset = parseInt(process.argv[2], 10);
    const skip = process.argv[3] === "1";
    const fd = fs.openSync(file, "r+");
    const buf = Buffer.alloc(1);
    fs.readSync(fd, buf, 0, 1, offset);
    const orig = buf[0];
    if (!skip) { buf[0] = orig ^ 0xFF; }
    fs.writeSync(fd, buf, 0, 1, offset);
    fs.closeSync(fd);
    console.log(orig + " " + buf[0]);
  ' "$file" "$offset" "$skip"
}

# name:basename pairs -- BOTH shipped artifacts (ADR-0070 D1).
ARTIFACTS=(
  "core-bundle:aireos-offline-0.13.0.tar.gz"
  "postgres-companion:aireos-postgres-ubuntu2404.tar.gz"
)

for entry in "${ARTIFACTS[@]}"; do
  label="${entry%%:*}"
  base="${entry#*:}"
  src="$DIST_DIR/$base"
  src_sig="$src.sig"
  src_sbom="$src.sbom.json"

  echo
  echo "== $label ($base) =="

  if [ ! -f "$src" ] || [ ! -f "$src_sig" ] || [ ! -f "$src_sbom" ]; then
    echo "ERROR: missing $src (or its .sig / .sbom.json) -- build+sign it first; this negative proof does not self-skip"
    fail=$((fail + 1))
    continue
  fi

  scratch="$(mktemp -d)"
  SCRATCH_DIRS+=("$scratch")
  cp "$src" "$scratch/$base"
  cp "$src_sig" "$scratch/$base.sig"
  cp "$src_sbom" "$scratch/$base.sbom.json"

  copy="$scratch/$base"
  copy_sig="$copy.sig"

  # --- CONTROL: the untouched COPY verifies clean -- same bytes, same code
  # path the mutation proof below will use, so the control and the proof are
  # not silently exercising different behavior. ---
  assert_true "$label control: raw openssl verify on unmutated copy" \
    openssl pkeyutl -verify -pubin -inkey "$FIXTURE_PUB" -rawin -in "$copy" -sigfile "$copy_sig"

  ctrl_out="$(OUT_DIR="$scratch" RELEASE_PUBLIC_KEY="$FIXTURE_PUB" bash "$CHECK_PROVENANCE" 2>&1)"
  ctrl_status=$?
  assert_eq "$label control: check-provenance.sh exits 0 on unmutated copy" "$ctrl_status" "0"

  # --- MUTATE exactly one byte, deep in the compressed payload (filesize/2,
  # never the gzip header), guaranteed to differ (XOR 0xFF). ---
  size="$(stat -c '%s' "$copy")"
  offset=$((size / 2))
  skip_mutation="0"
  [ -n "${SEC45_TEST_SKIP_MUTATION:-}" ] && skip_mutation="1"
  bytes_out="$(mutate_one_byte "$copy" "$offset" "$skip_mutation")"
  echo "  byte@offset $offset: $bytes_out (orig new)"

  mutated_size="$(stat -c '%s' "$copy")"
  assert_eq "$label mutation: file size unchanged" "$mutated_size" "$size"

  diff_count="$(cmp -l "$src" "$copy" 2>/dev/null | wc -l | tr -d ' ')"
  if [ "$skip_mutation" = "1" ]; then
    assert_eq "$label mutation (VER-16 fail-proof mode): zero bytes differ (no-op by design)" "$diff_count" "0"
  else
    assert_eq "$label mutation: exactly ONE byte differs from the original" "$diff_count" "1"
  fi

  # --- DETECTION: the signature axis, isolated from any SBOM/tar failure a
  # corrupted archive also triggers (see header). ---
  assert_false "$label detection: raw openssl verify FAILS on the mutated copy" \
    openssl pkeyutl -verify -pubin -inkey "$FIXTURE_PUB" -rawin -in "$copy" -sigfile "$copy_sig"

  prov_out="$(OUT_DIR="$scratch" RELEASE_PUBLIC_KEY="$FIXTURE_PUB" bash "$CHECK_PROVENANCE" 2>&1)"
  prov_status=$?
  if [ "$prov_status" -ne 0 ]; then
    echo "PASS $label detection: check-provenance.sh exits non-zero on the mutated copy"; pass=$((pass + 1))
  else
    echo "FAIL $label detection: check-provenance.sh exits non-zero on the mutated copy -- got exit 0"; fail=$((fail + 1))
  fi
  assert_contains "$label detection: output names THIS artifact's 'signature does NOT verify'" \
    "$prov_out" "$base signature does NOT verify"
done

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
