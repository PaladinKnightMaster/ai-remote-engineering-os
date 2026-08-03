#!/usr/bin/env bash
# install_profile_test.sh — table-driven unit test for install.sh's deployment
# profile helpers (ADR-0068 §D1; OI-21..23). This is the ticket's named live
# test for OI-22 (VER-6): it proves the vpn-first/dmz-first loopback refusal
# without ever standing up a VPN.
#
# Sources install.sh; install.sh's own sourceable guard (BASH_SOURCE[0] != $0)
# returns before the root check / installer body, so this never touches a real
# host — no user, no directories, no systemd, no DB.
#
# NOTE: install.sh's `set -euo pipefail` is shell-global, not file-scoped, so
# it leaks into THIS shell once sourced. Every helper call below is therefore
# made inside an `if`/command-substitution context (never a bare statement),
# so a refusal's `exit 1` (inside a subshell) or non-zero return never aborts
# this script under -e.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/install.sh"

pass=0
fail=0

# call <profile> <site> — runs aireos_assert_profile in a subshell (so its
# die()/exit never escapes) and prints "<rc>|<message>".
call() {
  local profile="$1" site="$2" out rc
  if out="$( aireos_assert_profile "$profile" "$site" 2>&1 )"; then
    rc=0
  else
    rc=$?
  fi
  printf '%s|%s' "$rc" "$out"
}

# expect_refuse <name> <profile> <site> <must-contain> — asserts refusal
# (non-zero) AND that the message names the given substring (OI-22's wording
# requirement — never just a non-zero exit code).
expect_refuse() {
  local name="$1" profile="$2" site="$3" must_contain="$4" result rc msg
  result="$(call "$profile" "$site")"
  rc="${result%%|*}"; msg="${result#*|}"
  if [ "$rc" -eq 0 ]; then
    echo "FAIL $name -- expected refusal, got acceptance"
    fail=$((fail + 1)); return
  fi
  case "$msg" in
    *"$must_contain"*) ;;
    *) echo "FAIL $name -- refusal message does not name '$must_contain': $msg"
       fail=$((fail + 1)); return ;;
  esac
  echo "PASS $name -- refused, names '$must_contain'"
  pass=$((pass + 1))
}

# expect_pass <name> <profile> <site> — asserts acceptance (exit 0).
expect_pass() {
  local name="$1" profile="$2" site="$3" result rc msg
  result="$(call "$profile" "$site")"
  rc="${result%%|*}"; msg="${result#*|}"
  if [ "$rc" -ne 0 ]; then
    echo "FAIL $name -- expected acceptance, got refusal: $msg"
    fail=$((fail + 1)); return
  fi
  echo "PASS $name -- accepted"
  pass=$((pass + 1))
}

echo "== must refuse =="
expect_refuse "profile unset"                    ""          ""                     "lan-only"
expect_refuse "profile 'lanonly' (typo)"          "lanonly"   ""                     "lanonly"
expect_refuse "profile 'LAN-ONLY' (no case fold)" "LAN-ONLY"  ""                     "LAN-ONLY"
expect_refuse "profile 'vpn' (not vpn-first)"     "vpn"       ""                     "vpn"
expect_refuse "vpn-first + unset site"            "vpn-first" ""                     "vpn-first"
expect_refuse "vpn-first + localhost"             "vpn-first" "localhost"            "vpn-first"
expect_refuse "vpn-first + 127.0.0.1"             "vpn-first" "127.0.0.1"            "vpn-first"
expect_refuse "vpn-first + 127.1.2.3"             "vpn-first" "127.1.2.3"            "vpn-first"
expect_refuse "vpn-first + ::1"                   "vpn-first" "::1"                  "vpn-first"
expect_refuse "vpn-first + [::1]:8443"            "vpn-first" "[::1]:8443"           "vpn-first"
expect_refuse "vpn-first + https://localhost:8443" "vpn-first" "https://localhost:8443" "vpn-first"
expect_refuse "vpn-first + cockpit.localhost"     "vpn-first" "cockpit.localhost"    "vpn-first"
expect_refuse "vpn-first + 'localhost 127.0.0.1' (all loopback)" "vpn-first" "localhost 127.0.0.1" "vpn-first"
expect_refuse "dmz-first + localhost"             "dmz-first" "localhost"            "dmz-first"

echo "== must pass =="
expect_pass "lan-only + unset site"                          "lan-only"  ""
expect_pass "lan-only + localhost"                            "lan-only"  "localhost"
expect_pass "lan-only + 'localhost host42' (wd1 value)"        "lan-only"  "localhost host42"
expect_pass "vpn-first + cockpit.corp.example"                 "vpn-first" "cockpit.corp.example"
expect_pass "vpn-first + 'localhost cockpit.corp.example' (one routable token)" "vpn-first" "localhost cockpit.corp.example"
expect_pass "vpn-first + https://cockpit.corp.example:8443"    "vpn-first" "https://cockpit.corp.example:8443"
expect_pass "vpn-first + 10.10.0.4"                            "vpn-first" "10.10.0.4"
expect_pass "vpn-first + :443 (bare port, binds every interface)" "vpn-first" ":443"
expect_pass "vpn-first + 0.0.0.0 (unspecified, not loopback)"  "vpn-first" "0.0.0.0"
expect_pass "dmz-first + gw.example.com"                       "dmz-first" "gw.example.com"

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
