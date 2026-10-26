#!/usr/bin/env bash
# install_prompts_test.sh — T-165 (v0.16.0 PUB-6 fix pass): runs install.sh's REAL
# body to prove the two defects that a pure-function test cannot reach.
#
#   P7  a malformed database URL (the CEO typed `cockpit`) must be refused with a
#       plain-English message BEFORE any env file is written — both when it comes
#       from AIREOS_DB_URL and when it is read from a non-terminal stdin — while a
#       valid URL whose password contains a raw '@' still goes straight through
#       (`read -e` must not break piped input either: P6).
#   P9  a re-run must RESTART the units (`systemctl enable --now` is a no-op on an
#       active unit and Caddy reads AIREOS_SITE_ADDRESS only at start), the DONE
#       line must print only after that succeeded, and a failed restart must fail
#       loudly with no DONE line.
#
# Like install_audit_key_test.sh it needs root and a disposable filesystem, and it
# DELETES /etc/aireos and /var/lib/aireos between legs, so it additionally refuses
# to run anywhere that is not visibly a container:
#
#   MSYS_NO_PATHCONV=1 docker run --rm -v "D:/path/to/repo:/work" -w /work \
#     -v aireos_gomodcache:/go/pkg/mod golang:1.26 bash infra/offline/install_prompts_test.sh
#
# It builds the real authctl (step 3 shells out to it), fakes every other bundle
# binary (`migrate up` and `credentials genkey` succeed), and puts a recording
# stub `systemctl` first on PATH.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# The bootstrap operator password is generated PER RUN (T-168), never committed:
# a literal here is exactly what secret scanners flag as a "Generic Password".
# 24 hex chars from the kernel CSPRNG; nothing asserts on its value.
TEST_OP_PW="$(od -An -N12 -tx1 /dev/urandom | tr -d ' \n')"
[ "${#TEST_OP_PW}" -eq 24 ] || { echo "FAIL could not generate a random test password from /dev/urandom"; exit 1; }

if [ "$(id -u)" != "0" ]; then
  echo "SKIP install_prompts_test.sh -- needs root (useradd/chown); run it inside a disposable container (see this file's header)"
  exit 0
fi
if [ ! -f /.dockerenv ] && [ "${AIREOS_TEST_DISPOSABLE:-}" != "1" ]; then
  echo "SKIP install_prompts_test.sh -- this test deletes /etc/aireos and /var/lib/aireos; it only runs in a container (or with AIREOS_TEST_DISPOSABLE=1)"
  exit 0
fi

pass=0
fail=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1 -- $2"; fail=$((fail + 1)); }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BUNDLE="$WORK/bundle"
STUBBIN="$WORK/stubbin"
mkdir -p "$BUNDLE/bin" "$BUNDLE/web" "$BUNDLE/config" "$STUBBIN"

echo "== build the real authctl; fake the rest of the bundle =="
if ! ( cd "$REPO_ROOT" && go build -o "$BUNDLE/bin/authctl" ./packages/go/auth/cmd/authctl ); then
  echo "FAIL build authctl -- cannot proceed without it"; exit 1
fi
for svc in api audit-service session-broker connector-service policy-service mcp-gateway; do
  cat > "$BUNDLE/bin/$svc" <<'EOF'
#!/bin/sh
if [ "$1" = "credentials" ] && [ "$2" = "genkey" ]; then echo "fake0000000000000000000000000000000000000000000000000000resource"; exit 0; fi
if [ "$1" = "migrate" ] && [ "$2" = "up" ]; then exit 0; fi
exit 1
EOF
  chmod +x "$BUNDLE/bin/$svc"
done
: > "$BUNDLE/web/index.html"
touch "$BUNDLE/config/Caddyfile"
for svc in api audit broker connector policy mcp-gateway caddy; do touch "$BUNDLE/config/aireos-$svc.service"; done
cp "$SCRIPT_DIR/install.sh" "$BUNDLE/install.sh"; chmod +x "$BUNDLE/install.sh"

# Recording systemctl stub; SYSTEMCTL_FAIL_ON=<verb> makes that verb exit 1.
cat > "$STUBBIN/systemctl" <<'EOF'
#!/bin/sh
echo "$*" >> "$SYSTEMCTL_LOG"
[ -n "${SYSTEMCTL_FAIL_ON:-}" ] && [ "$1" = "$SYSTEMCTL_FAIL_ON" ] && exit 1
exit 0
EOF
chmod +x "$STUBBIN/systemctl"
export SYSTEMCTL_LOG="$WORK/systemctl.log"

fresh() { rm -rf /etc/aireos /var/lib/aireos; rm -f /etc/systemd/system/aireos-*.service; : > "$SYSTEMCTL_LOG"; }
GOOD_URL='postgres://cockpit:p@ss@w0rd@127.0.0.1:5432/cockpit?sslmode=disable'
SEVEN="aireos-api.service aireos-audit.service aireos-broker.service aireos-connector.service aireos-policy.service aireos-mcp-gateway.service aireos-caddy.service"

# run_install <stdin-file> [VAR=val ...] — operator + profile always supplied
# (the point is the DB URL and the systemd step); output to $WORK/out.log.
run_install() {
  local stdin="$1"; shift
  env PATH="$STUBBIN:$PATH" \
    AIREOS_DEPLOYMENT_PROFILE=lan-only \
    AIREOS_BOOTSTRAP_OPERATOR_USER=testop AIREOS_BOOTSTRAP_OPERATOR_PASSWORD="$TEST_OP_PW" \
    "$@" bash "$BUNDLE/install.sh" <"$stdin" >"$WORK/out.log" 2>&1
}
NOSTDIN=/dev/null

echo "== P7: unattended AIREOS_DB_URL=cockpit is refused before ANY env file is written =="
fresh
run_install "$NOSTDIN" AIREOS_DB_URL=cockpit AIREOS_SKIP_SYSTEMCTL=1; rc=$?
[ "$rc" -ne 0 ] && ok "P7 unattended: exit non-zero (rc=$rc)" || bad "P7 unattended: exit" "accepted 'cockpit' (rc=0)"
grep -q 'postgres://<user>:<password>@<host>:5432/<database>?sslmode=disable' "$WORK/out.log" \
  && ok "P7 unattended: message shows the expected URL shape" || bad "P7 unattended: message" "$(cat "$WORK/out.log")"
ls /etc/aireos/*.env >/dev/null 2>&1 && bad "P7 unattended: env files" "env files were written: $(ls /etc/aireos)" || ok "P7 unattended: no env file written"
grep -q 'migration failed' "$WORK/out.log" && bad "P7 unattended: wrong failure" "reached migration (the old misleading hint)" || ok "P7 unattended: did not reach migration"

echo "== P7: a bad URL on a non-terminal stdin fails at once with the same message, no env file =="
fresh
printf 'cockpit\n' > "$WORK/stdin-bad"
run_install "$WORK/stdin-bad" AIREOS_SKIP_SYSTEMCTL=1; rc=$?
[ "$rc" -ne 0 ] && ok "P7 piped: exit non-zero (rc=$rc)" || bad "P7 piped: exit" "accepted 'cockpit' from stdin (rc=0)"
grep -q 'postgres://<user>:<password>@<host>:5432/<database>?sslmode=disable' "$WORK/out.log" \
  && ok "P7 piped: message shows the expected URL shape" || bad "P7 piped: message" "$(cat "$WORK/out.log")"
ls /etc/aireos/*.env >/dev/null 2>&1 && bad "P7 piped: env files" "env files were written" || ok "P7 piped: no env file written"

echo "== P6/P7: a valid URL with a raw '@' in the password piped through 'read -e' is accepted verbatim =="
fresh
printf '%s\n' "$GOOD_URL" > "$WORK/stdin-good"
run_install "$WORK/stdin-good" AIREOS_SKIP_SYSTEMCTL=1; rc=$?
[ "$rc" -eq 0 ] && ok "P6/P7 piped valid URL: install completes (rc=0)" || bad "P6/P7 piped valid URL" "rc=$rc: $(cat "$WORK/out.log")"
[ "$(sed -n 's/^DATABASE_URL=//p' /etc/aireos/aireos.env)" = "$GOOD_URL" ] \
  && ok "P6/P7 piped valid URL: written to aireos.env byte-for-byte" || bad "P6/P7 piped valid URL" "DATABASE_URL in aireos.env differs: $(sed -n 's/^DATABASE_URL=//p' /etc/aireos/aireos.env)"

echo "== P6/P7 on a REAL terminal: arrow-key line editing works, and a bad URL is re-asked =="
# `script` gives install.sh a genuine pty (so [ -t 0 ] is true and readline is
# live) and feeds it the keystrokes. Left-arrow is ESC [ D: without `read -e` the
# username would be stored as the literal bytes "admn^[[Di" (PUB-6 P6).
if ! command -v script >/dev/null 2>&1; then
  echo "SKIP pty legs -- 'script' (util-linux) not installed in this container"
else
  fresh
  (
    export PATH="$STUBBIN:$PATH" AIREOS_DEPLOYMENT_PROFILE=lan-only AIREOS_BOOTSTRAP_OPERATOR_PASSWORD="$TEST_OP_PW" AIREOS_SKIP_SYSTEMCTL=1
    unset AIREOS_BOOTSTRAP_OPERATOR_USER AIREOS_DB_URL
    printf 'admn\033[Di\n%s\n%s\n' cockpit "$GOOD_URL" | script -qec "bash $BUNDLE/install.sh" /dev/null > "$WORK/pty.log" 2>&1
  ); rc=$?
  [ "$rc" -eq 0 ] && ok "P6/P7 pty: install completes after one re-ask (rc=0)" || bad "P6/P7 pty: install" "rc=$rc: $(cat -v "$WORK/pty.log" | tail -15)"
  GOT_USER="$(sed -n 's/^AIREOS_BOOTSTRAP_OPERATOR_USER=//p' /etc/aireos/aireos.env 2>/dev/null)"
  [ "$GOT_USER" = "admin" ] && ok "P6 pty: Left-arrow editing worked -- operator username stored as 'admin'" || bad "P6 pty: line editing" "operator username stored as '$(printf '%s' "$GOT_USER" | cat -v)', want 'admin'"
  [ "$(grep -c 'not a PostgreSQL connection URL' "$WORK/pty.log")" = "1" ] && ok "P7 pty: the bad URL got the plain-English message exactly once, then was re-asked" || bad "P7 pty: re-ask" "message count != 1: $(cat -v "$WORK/pty.log" | tail -15)"
  [ "$(sed -n 's/^DATABASE_URL=//p' /etc/aireos/aireos.env 2>/dev/null)" = "$GOOD_URL" ] && ok "P7 pty: the corrected URL is what was written" || bad "P7 pty: written URL" "$(sed -n 's/^DATABASE_URL=//p' /etc/aireos/aireos.env 2>/dev/null)"
fi

echo "== P9: a fresh install enables and RESTARTS all seven units, DONE only afterwards =="
fresh
run_install "$NOSTDIN" AIREOS_DB_URL="$GOOD_URL" AIREOS_SITE_ADDRESS=a.test; rc=$?
[ "$rc" -eq 0 ] && ok "P9 install: rc=0" || bad "P9 install" "rc=$rc: $(cat "$WORK/out.log")"
missing=""
for u in $SEVEN; do grep -qE "^restart (.* )?$u( |\$)" "$SYSTEMCTL_LOG" || missing="$missing $u"; done
[ -z "$missing" ] && ok "P9 install: systemctl restart issued for all seven units" || bad "P9 install: restart" "not restarted:$missing -- log: $(tr '\n' ';' < "$SYSTEMCTL_LOG")"
grep -q '^DONE\|\[install\].*DONE' "$WORK/out.log" && ok "P9 install: DONE printed" || bad "P9 install: DONE" "missing: $(cat "$WORK/out.log")"

echo "== P9: a RE-RUN with a changed site address restarts the (already active) units again =="
: > "$SYSTEMCTL_LOG"
run_install "$NOSTDIN" AIREOS_DB_URL="$GOOD_URL" AIREOS_SITE_ADDRESS=b.test; rc=$?
[ "$rc" -eq 0 ] && ok "P9 re-run: rc=0" || bad "P9 re-run" "rc=$rc"
grep -q '^AIREOS_SITE_ADDRESS=b.test$' /etc/aireos/aireos.env && ok "P9 re-run: aireos.env carries the NEW address" || bad "P9 re-run" "aireos.env address not updated"
missing=""
for u in $SEVEN; do grep -qE "^restart (.* )?$u( |\$)" "$SYSTEMCTL_LOG" || missing="$missing $u"; done
[ -z "$missing" ] && ok "P9 re-run: all seven restarted, so the new env is what serves" || bad "P9 re-run: restart" "not restarted:$missing"

echo "== P9: a FAILED restart fails loudly — non-zero exit and NO DONE line =="
: > "$SYSTEMCTL_LOG"
run_install "$NOSTDIN" AIREOS_DB_URL="$GOOD_URL" AIREOS_SITE_ADDRESS=c.test SYSTEMCTL_FAIL_ON=restart; rc=$?
[ "$rc" -ne 0 ] && ok "P9 failed restart: exit non-zero (rc=$rc)" || bad "P9 failed restart" "install reported success"
grep -q 'DONE' "$WORK/out.log" && bad "P9 failed restart: DONE" "DONE line printed despite the failed restart" || ok "P9 failed restart: no DONE line"
grep -q 'could not enable and restart' "$WORK/out.log" && ok "P9 failed restart: plain-English message" || bad "P9 failed restart: message" "$(cat "$WORK/out.log")"

fresh
echo
echo "== summary: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
