#!/usr/bin/env bash
# install_profile_test.sh — table-driven unit test for install.sh's deployment
# profile helpers (ADR-0068 §D1; OI-21..23) AND its pairing-enablement
# resolution (ADR-0068 §D10; T-114a Commander-review addendum). This is the
# ticket's named live test for OI-22 (VER-6): it proves the vpn-first/dmz-first
# loopback refusal without ever standing up a VPN. It also proves
# AIREOS_PAIRING_ENABLED's env > persisted > "0" precedence, closing the
# defect where a hardcoded `0` in the aireos.env heredoc silently disabled a
# deliberately-enabled pairing session on every re-run.
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
expect_refuse "vpn-first + localhost.localdomain" "vpn-first" "localhost.localdomain" "vpn-first"
expect_refuse "vpn-first + 0:0:0:0:0:0:0:1"       "vpn-first" "0:0:0:0:0:0:0:1"      "vpn-first"
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

# expect_resolve <name> <env-value> <persisted> <answer> <want> — asserts
# aireos_resolve_profile's pure precedence (env > answer > persisted > empty).
expect_resolve() {
  local name="$1" env_value="$2" persisted="$3" answer="$4" want="$5" got
  got="$(aireos_resolve_profile "$env_value" "$persisted" "$answer")"
  if [ "$got" = "$want" ]; then
    echo "PASS $name -- resolved '$got'"
    pass=$((pass + 1))
  else
    echo "FAIL $name -- expected '$want', got '$got'"
    fail=$((fail + 1))
  fi
}

echo "== aireos_resolve_profile (pure precedence: env > answer > persisted > empty) =="
expect_resolve "env-only"                                    "vpn-first" ""          ""           "vpn-first"
expect_resolve "persisted-only, empty answer (OI-23 no-op)"  ""          "lan-only"  ""           "lan-only"
expect_resolve "env set, persisted set, differ -- env wins"  "dmz-first" "lan-only"  ""           "dmz-first"
expect_resolve "nothing set anywhere -- empty"                ""          ""          ""           ""

echo "== OI-23 re-assert: a changed profile on re-run is re-checked against OI-22 =="
CHANGED="$(aireos_resolve_profile "" "lan-only" "vpn-first")"
if [ "$CHANGED" = "vpn-first" ]; then
  echo "PASS resolve: persisted='lan-only' answer='vpn-first' -- answer wins ('vpn-first')"
  pass=$((pass + 1))
else
  echo "FAIL resolve: persisted='lan-only' answer='vpn-first' -- expected 'vpn-first', got '$CHANGED'"
  fail=$((fail + 1))
fi
expect_refuse "changed profile (persisted lan-only -> answer vpn-first) + loopback site re-asserts OI-22" "$CHANGED" "localhost" "vpn-first"

echo "== resolved-empty profile is refused, never silently installed =="
EMPTY="$(aireos_resolve_profile "" "" "")"
expect_refuse "resolve('','','') -> empty -> assertion refuses" "$EMPTY" "" "lan-only"

echo "== aireos_persisted_value (generalized from aireos_persisted_profile) =="
TMPD="$(mktemp -d)"
trap 'rm -rf "$TMPD"' EXIT

printf 'AIREOS_SITE_ADDRESS=localhost\nAIREOS_DEPLOYMENT_PROFILE=vpn-first\n' > "$TMPD/present.env"
GOT="$(aireos_persisted_value "AIREOS_DEPLOYMENT_PROFILE" "$TMPD/present.env")"
if [ "$GOT" = "vpn-first" ]; then
  echo "PASS persisted-value(profile): line present -- got 'vpn-first'"; pass=$((pass + 1))
else
  echo "FAIL persisted-value(profile): line present -- got '$GOT'"; fail=$((fail + 1))
fi

printf 'AIREOS_SITE_ADDRESS=localhost\n' > "$TMPD/absent.env"
GOT="$(aireos_persisted_value "AIREOS_DEPLOYMENT_PROFILE" "$TMPD/absent.env")"
if [ -z "$GOT" ]; then
  echo "PASS persisted-value(profile): file present, line absent -- got empty"; pass=$((pass + 1))
else
  echo "FAIL persisted-value(profile): file present, line absent -- got '$GOT'"; fail=$((fail + 1))
fi

GOT="$(aireos_persisted_value "AIREOS_DEPLOYMENT_PROFILE" "$TMPD/does-not-exist.env")"
if [ -z "$GOT" ]; then
  echo "PASS persisted-value(profile): file missing entirely -- got empty"; pass=$((pass + 1))
else
  echo "FAIL persisted-value(profile): file missing entirely -- got '$GOT'"; fail=$((fail + 1))
fi

# aireos_persisted_value reused for a SECOND key (AIREOS_PAIRING_ENABLED) in the
# SAME file, proving the generalization actually generalized rather than just
# renaming a still-profile-only function.
printf 'AIREOS_SITE_ADDRESS=localhost\nAIREOS_DEPLOYMENT_PROFILE=lan-only\nAIREOS_PAIRING_ENABLED=1\n' > "$TMPD/both.env"
GOT="$(aireos_persisted_value "AIREOS_PAIRING_ENABLED" "$TMPD/both.env")"
if [ "$GOT" = "1" ]; then
  echo "PASS persisted-value(pairing): line present alongside profile -- got '1'"; pass=$((pass + 1))
else
  echo "FAIL persisted-value(pairing): line present alongside profile -- got '$GOT'"; fail=$((fail + 1))
fi

# expect_pairing_resolve <name> <env-value> <persisted> <want> — asserts
# aireos_resolve_pairing_enabled's precedence (env > persisted > "0", no
# prompt leg — ADR-0068 §D10, T-114a Commander-review addendum).
expect_pairing_resolve() {
  local name="$1" env_value="$2" persisted="$3" want="$4" got
  got="$(aireos_resolve_pairing_enabled "$env_value" "$persisted")"
  if [ "$got" = "$want" ]; then
    echo "PASS $name -- resolved '$got'"
    pass=$((pass + 1))
  else
    echo "FAIL $name -- expected '$want', got '$got'"
    fail=$((fail + 1))
  fi
}

echo "== aireos_resolve_pairing_enabled (env > persisted > \"0\"; no prompt) =="
expect_pairing_resolve "env wins over a differing persisted value" "1" "0" "1"
expect_pairing_resolve "persisted survives when env is unset (the re-run fix)" "" "1" "1"
expect_pairing_resolve "neither set -- safe default \"0\" (SEC-41, fresh install)" "" "" "0"

# --- T-165 / PUB-6 P7: database URL shape ------------------------------------
expect_url() {   # expect_url <name> <ok|bad> <url>
  local name="$1" want="$2" url="$3" got=bad
  if aireos_valid_db_url "$url"; then got=ok; fi
  if [ "$got" = "$want" ]; then
    echo "PASS db-url $name -- $got"; pass=$((pass + 1))
  else
    echo "FAIL db-url $name -- expected $want, got $got"; fail=$((fail + 1))
  fi
}

echo "== aireos_valid_db_url (PUB-6 P7) =="
expect_url "postgres:// canonical"                        ok  'postgres://cockpit:pw@localhost:5432/cockpit?sslmode=disable'
expect_url "postgresql:// scheme"                         ok  'postgresql://cockpit:pw@db.corp.example:5432/cockpit'
expect_url "raw @ in the password stays valid (CEO's real one)" ok 'postgres://cockpit:p@ss@w0rd@10.0.0.5:5432/cockpit?sslmode=disable'
expect_url "no port"                                      ok  'postgres://cockpit:pw@db/cockpit'
expect_url "no userinfo"                                  ok  'postgres://db.example:5432/cockpit'
expect_url "bracketed IPv6 host"                          ok  'postgres://cockpit:pw@[::1]:5432/cockpit'
expect_url "empty"                                        bad ''
expect_url "bare word 'cockpit' (the P7 input)"           bad 'cockpit'
expect_url "http:// scheme"                               bad 'http://cockpit:pw@localhost:5432/cockpit'
expect_url "mysql:// scheme"                              bad 'mysql://cockpit:pw@localhost:3306/cockpit'
expect_url "scheme only"                                  bad 'postgres://'
expect_url "empty host (postgres:///db)"                  bad 'postgres:///cockpit'
expect_url "userinfo but empty host"                      bad 'postgres://cockpit:pw@/cockpit'
expect_url "non-numeric port"                             bad 'postgres://cockpit:pw@localhost:abc/cockpit'
expect_url "whitespace inside"                            bad 'postgres://cockpit:pw@local host:5432/cockpit'
expect_url "the old placeholder pasted verbatim, missing scheme" bad 'user:pass@host:5432/cockpit?sslmode=disable'

# the hint must show the expected shape and must NEVER echo a candidate value
HINT="$(aireos_db_url_hint)"
case "$HINT" in
  *"postgres://USER:PASSWORD@HOST:5432/DATABASE?sslmode=disable"*) echo "PASS db-url hint shows the expected shape"; pass=$((pass + 1)) ;;
  *) echo "FAIL db-url hint does not show the expected shape: $HINT"; fail=$((fail + 1)) ;;
esac

# --- T-165 / PUB-6 P9: a re-run RESTARTS the units ---------------------------
# A stub systemctl on PATH records its argv. `enable --now` on an already-active
# unit is a no-op, so only an explicit `restart` of every unit makes the env a
# re-run just wrote take effect.
echo "== aireos_enable_and_restart_units (PUB-6 P9) =="
SCBIN="$TMPD/scbin"; mkdir -p "$SCBIN"
cat > "$SCBIN/systemctl" <<'STUB'
#!/bin/sh
echo "$*" >> "$SYSTEMCTL_LOG"
[ -n "${SYSTEMCTL_FAIL_ON:-}" ] && [ "$1" = "$SYSTEMCTL_FAIL_ON" ] && exit 1
exit 0
STUB
chmod +x "$SCBIN/systemctl"
SEVEN="aireos-api.service aireos-audit.service aireos-broker.service aireos-connector.service aireos-policy.service aireos-mcp-gateway.service aireos-caddy.service"

export SYSTEMCTL_LOG="$TMPD/systemctl.log"; : > "$SYSTEMCTL_LOG"
if PATH="$SCBIN:$PATH" aireos_enable_and_restart_units; then rc=0; else rc=$?; fi
if [ "$rc" -eq 0 ]; then echo "PASS restart: happy path returns 0"; pass=$((pass + 1)); else echo "FAIL restart: happy path rc=$rc"; fail=$((fail + 1)); fi
missing=""
for u in $SEVEN; do
  grep -qE "^restart (.* )?$u( |\$)" "$SYSTEMCTL_LOG" || missing="$missing $u"
done
if [ -z "$missing" ]; then echo "PASS restart: all seven units named in a 'systemctl restart'"; pass=$((pass + 1)); else echo "FAIL restart: not restarted:$missing -- log: $(tr '\n' ';' < "$SYSTEMCTL_LOG")"; fail=$((fail + 1)); fi
if grep -q -- '--now' "$SYSTEMCTL_LOG"; then echo "FAIL restart: still uses enable --now (a no-op on an active unit)"; fail=$((fail + 1)); else echo "PASS restart: no enable --now"; pass=$((pass + 1)); fi

for failing in daemon-reload enable restart; do
  : > "$SYSTEMCTL_LOG"
  if SYSTEMCTL_FAIL_ON="$failing" PATH="$SCBIN:$PATH" aireos_enable_and_restart_units; then rc=0; else rc=$?; fi
  if [ "$rc" -ne 0 ]; then echo "PASS restart: failing '$failing' makes the function fail (rc=$rc)"; pass=$((pass + 1)); else echo "FAIL restart: failing '$failing' was swallowed"; fail=$((fail + 1)); fi
done

echo
echo "$pass passed, $fail failed"
[ "$fail" -eq 0 ]
