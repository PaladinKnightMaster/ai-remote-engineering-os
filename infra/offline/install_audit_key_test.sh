#!/usr/bin/env bash
# install_audit_key_test.sh — G1 named live test for OI-41 (ADR-0069 D5,
# AUD-35, T-125a): proves install.sh's audit chain-head signing key is
# idempotent across re-installs, lives ONLY in audit.env, and that
# $STATE/audit is provisioned like the installer's other state directories.
#
# Unlike install_profile_test.sh (which SOURCES install.sh to exercise pure
# helper functions with no side effects — see that file's own header), this
# test runs install.sh's REAL body: OI-41 is a claim about actual on-disk file
# mode/ownership and byte-identical key reuse across runs, which a pure
# function cannot demonstrate. That means this test needs root (useradd,
# chown) and a disposable filesystem, so it must run inside a throwaway
# container rather than on a bare host:
#
#   docker run --rm -v "$PWD":/work -w /work -v aireos_gomodcache:/go/pkg/mod \
#     golang:1.23 bash infra/offline/install_audit_key_test.sh
#
# If invoked as a non-root user (e.g. accidentally reached via `task test` on
# a bare developer machine) it SKIPS rather than mutating a real host's
# /etc/aireos or creating a real system user.
#
# It builds a REAL authctl binary — the only bundle "binary" this test needs
# to be genuine, since it is what step 3c actually shells out to
# (`authctl genkeypair`) — and fakes the rest of the bundle. It never reaches
# a real Postgres: AIREOS_DB_URL is a placeholder, so step 7 (migrations) is
# EXPECTED to fail on every run. Both installs are asserted through step 6
# only, which is everything OI-41 is about; the real installed-stack proof
# (including migrations under a live Postgres) is `task wd1`, deferred to
# T-127 per this ticket's brief.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

if [ "$(id -u)" != "0" ]; then
  echo "SKIP install_audit_key_test.sh -- needs root (useradd/chown); run it inside a disposable container (see this file's header)"
  exit 0
fi

pass=0
fail=0

# assert_true <name> <command...> — PASS iff <command...> exits 0.
assert_true() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "PASS $name"; pass=$((pass + 1))
  else
    echo "FAIL $name"; fail=$((fail + 1))
  fi
}

# assert_eq <name> <got> <want>
assert_eq() {
  local name="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    echo "PASS $name -- '$got'"; pass=$((pass + 1))
  else
    echo "FAIL $name -- got '$got', want '$want'"; fail=$((fail + 1))
  fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BUNDLE="$WORK/bundle"
mkdir -p "$BUNDLE/bin" "$BUNDLE/web" "$BUNDLE/config"

echo "== build the real authctl (the only genuine bundle binary this test needs — 3c shells out to it) =="
if ! ( cd "$REPO_ROOT" && go build -o "$BUNDLE/bin/authctl" ./packages/go/auth/cmd/authctl ); then
  echo "FAIL build authctl -- cannot proceed without it"
  fail=$((fail + 1))
  echo; echo "$pass passed, $fail failed"; exit 1
fi

echo "== fake the rest of the bundle (unused before step 7's migrations, which never succeed here) =="
for svc in api audit-service session-broker policy-service mcp-gateway; do
  printf '#!/bin/sh\nexit 1\n' > "$BUNDLE/bin/$svc"
  chmod +x "$BUNDLE/bin/$svc"
done
# connector-service is ALSO invoked in step 3 for "credentials genkey" (real
# resource-key generation, before our own step 3c) — unlike the other five, a
# bare exit-1 fake would kill install.sh before it ever reaches 3c/step 6.
cat > "$BUNDLE/bin/connector-service" <<'EOF'
#!/bin/sh
if [ "$1" = "credentials" ] && [ "$2" = "genkey" ]; then
  echo "fake0000000000000000000000000000000000000000000000000000resource"
  exit 0
fi
exit 1
EOF
chmod +x "$BUNDLE/bin/connector-service"
: > "$BUNDLE/web/index.html"
touch "$BUNDLE/config/Caddyfile"
for svc in api audit broker connector policy mcp-gateway caddy; do
  touch "$BUNDLE/config/aireos-$svc.service"
done
cp "$SCRIPT_DIR/install.sh" "$BUNDLE/install.sh"
chmod +x "$BUNDLE/install.sh"

run_install() {
  AIREOS_DEPLOYMENT_PROFILE=lan-only \
  AIREOS_BOOTSTRAP_OPERATOR_USER=testop \
  AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=testpw123 \
  AIREOS_DB_URL="postgres://unused:unused@127.0.0.1:5432/unused?sslmode=disable" \
  AIREOS_SKIP_SYSTEMCTL=1 \
  bash "$BUNDLE/install.sh" >"$WORK/install.log" 2>&1
}

AUDIT_ENV=/etc/aireos/audit.env
SHARED_ENV=/etc/aireos/aireos.env
STATE_AUDIT=/var/lib/aireos/audit

echo "== first install run =="
run_install || true
if grep -q 'migration failed for' "$WORK/install.log"; then
  echo "PASS install.sh reached step 7 and failed there as expected (no real postgres) -- steps 1-6 (incl. audit.env) ran to completion"; pass=$((pass + 1))
else
  echo "FAIL install.sh did not fail at the expected step (migration, step 7) on the first run -- log follows:"; fail=$((fail + 1))
  cat "$WORK/install.log"
fi

assert_true "audit.env created" test -f "$AUDIT_ENV"
SECRET1="$(sed -n 's/^AIREOS_AUDIT_CHAIN_SIGN_KEY=//p' "$AUDIT_ENV" 2>/dev/null)"
if [[ "$SECRET1" =~ ^[0-9a-f]{128}$ ]]; then
  echo "PASS secret is well-formed 128-hex"; pass=$((pass + 1))
else
  echo "FAIL secret is well-formed 128-hex -- got (${#SECRET1} chars) '$SECRET1'"; fail=$((fail + 1))
fi

PUB1="$(sed -n 's/^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=//p' "$SHARED_ENV" 2>/dev/null)"
if [[ "$PUB1" =~ ^[0-9a-f]{64}$ ]]; then
  echo "PASS public key is well-formed 64-hex in aireos.env"; pass=$((pass + 1))
else
  echo "FAIL public key is well-formed 64-hex in aireos.env -- got (${#PUB1} chars) '$PUB1'"; fail=$((fail + 1))
fi

assert_eq "audit.env mode 600" "$(stat -c '%a' "$AUDIT_ENV" 2>/dev/null)" "600"
assert_eq "audit.env owned by aireos:aireos" "$(stat -c '%U:%G' "$AUDIT_ENV" 2>/dev/null)" "aireos:aireos"

if [ -n "$SECRET1" ] && grep -qF "$SECRET1" "$SHARED_ENV" 2>/dev/null; then
  echo "FAIL secret does not appear in aireos.env -- FOUND IT (this is the RCE-blast-radius defect ADR-0069 D5 exists to prevent)"; fail=$((fail + 1))
else
  echo "PASS secret does not appear in aireos.env"; pass=$((pass + 1))
fi

assert_true "\$STATE/audit created" test -d "$STATE_AUDIT"
STATE_MODE="$(stat -c '%a %U:%G' "$STATE_AUDIT" 2>/dev/null)"
SESS_MODE="$(stat -c '%a %U:%G' /var/lib/aireos/sessions 2>/dev/null)"
assert_eq "\$STATE/audit mode+ownership matches the other state dirs (sessions)" "$STATE_MODE" "$SESS_MODE"

echo "== second install run (OI-41: must REUSE, never regenerate) =="
run_install || true
SECRET2="$(sed -n 's/^AIREOS_AUDIT_CHAIN_SIGN_KEY=//p' "$AUDIT_ENV" 2>/dev/null)"
PUB2="$(sed -n 's/^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=//p' "$SHARED_ENV" 2>/dev/null)"
assert_eq "OI-41: secret is BYTE-IDENTICAL across re-install" "$SECRET2" "$SECRET1"
assert_eq "OI-41: public key is BYTE-IDENTICAL across re-install" "$PUB2" "$PUB1"

echo "== third install run: partial-state recovery (Commander review finding) =="
# Simulate a realistic crash-recovery path: audit.env was written (secret on
# disk) but a prior run was interrupted/failed BEFORE aireos.env's rewrite, so
# the public key line is gone from the shared env while the secret survives.
# The regenerate-on-either-missing `||` this replaced would silently orphan
# every historical chain-head signature here -- the exact OI-41 failure this
# ticket exists to prevent. The fix must DERIVE the public half from the
# still-present secret, never mint a fresh pair.
sed -i '/^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=/d' "$SHARED_ENV"
if grep -q '^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=' "$SHARED_ENV" 2>/dev/null; then
  echo "FAIL setup: could not remove AIREOS_AUDIT_CHAIN_PUBLIC_KEY from aireos.env"; fail=$((fail + 1))
else
  echo "PASS setup: aireos.env's public-key line removed, audit.env's secret left intact"; pass=$((pass + 1))
fi
run_install || true
SECRET3="$(sed -n 's/^AIREOS_AUDIT_CHAIN_SIGN_KEY=//p' "$AUDIT_ENV" 2>/dev/null)"
PUB3="$(sed -n 's/^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=//p' "$SHARED_ENV" 2>/dev/null)"
assert_eq "partial-state recovery: secret is STILL BYTE-IDENTICAL (not regenerated)" "$SECRET3" "$SECRET1"
assert_eq "partial-state recovery: public key is RE-DERIVED to the same original value" "$PUB3" "$PUB1"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
