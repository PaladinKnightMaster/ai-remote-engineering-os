#!/usr/bin/env bash
# setup_loopback_demo_test.sh — table-driven unit test for
# setup-loopback-demo.sh's pure helpers (T-118b; ADR-0068 §D8, ONB-3/ONB-4).
# Same shape as install_profile_test.sh: source the script (which returns
# early behind its own sourceable guard, BASH_SOURCE[0] != $0, before any
# root-only/host-mutating logic runs) and exercise its argument-parsing,
# authorized_keys-line-construction, and undo-matcher functions directly — no
# root, no sshd, no real account, no systemd, no DB.
#
# The line-construction test is the one that matters most: a malformed
# authorized_keys entry either silently fails to authorize the client key (the
# demo target never works) or, worse, authorizes more than intended (a
# restriction option dropped or mis-joined). Both are covered below.
#
# NOTE: setup-loopback-demo.sh's `set -euo pipefail` is shell-global once
# sourced (not file-scoped), same caveat install_profile_test.sh documents —
# every helper call below runs inside an `if`/command-substitution context so
# a helper's non-zero return never aborts this script under -e.
#
# ADDED (T-133a, OI-38): one live check below DOES need root + a fake
# `systemctl` on PATH — it invokes the real CLI entry (below the sourceable
# guard) to prove `generate` accepts a socket-activated-only sshd rather than
# rejecting it. It SKIPS rather than failing when not run as root, same
# pattern as install_audit_key_test.sh.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/setup-loopback-demo.sh"

pass=0
fail=0

ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1 -- $2"; fail=$((fail + 1)); }

echo "== aireos_loopback_parse_generate_args =="

# expect_generate_ok <name> <want-account> <want-port> <args...>
expect_generate_ok() {
  local name="$1" want_account="$2" want_port="$3"; shift 3
  local out rc
  if out="$(aireos_loopback_parse_generate_args "$@" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -ne 0 ]; then bad "$name" "expected acceptance, got refusal: $out"; return; fi
  local got_account="${out%%|*}" got_port="${out##*|}"
  if [ "$got_account" = "$want_account" ] && [ "$got_port" = "$want_port" ]; then
    ok "$name -- account='$got_account' port='$got_port'"
  else
    bad "$name" "expected '$want_account|$want_port', got '$out'"
  fi
}

# expect_generate_refuse <name> <args...> — asserts refusal (any nonzero).
expect_generate_refuse() {
  local name="$1"; shift
  local out rc
  if out="$(aireos_loopback_parse_generate_args "$@" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -eq 0 ]; then
    bad "$name" "expected refusal, got acceptance ('$out')"
  else
    ok "$name -- refused ('$out')"
  fi
}

expect_generate_ok "account only, default port"        "aireos-demo" "22" --account aireos-demo
expect_generate_ok "account + explicit port"            "aireos-demo" "2222" --account aireos-demo --port 2222
expect_generate_ok "account with underscore and digits" "demo_2" "22" --account demo_2
expect_generate_ok "account with hyphen"                "aireos-loopback" "22" --account aireos-loopback

expect_generate_refuse "no arguments at all"                         # no --account
expect_generate_refuse "missing --account value"       --account
expect_generate_refuse "empty account"                  --account ""
expect_generate_refuse "account with a space"            --account "aireos demo"
expect_generate_refuse "account with a slash"            --account "aireos/demo"
expect_generate_refuse "account with shell metacharacters" --account 'a;rm -rf /'
expect_generate_refuse "port is not numeric"             --account aireos-demo --port abc
expect_generate_refuse "port is empty"                   --account aireos-demo --port ""
expect_generate_refuse "unknown flag"                    --account aireos-demo --bogus x

echo "== aireos_loopback_parse_bind_args =="

expect_bind_ok() {
  local name="$1" want="$2"; shift 2
  local out rc
  if out="$(aireos_loopback_parse_bind_args "$@" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -ne 0 ]; then bad "$name" "expected acceptance, got refusal: $out"; return; fi
  if [ "$out" = "$want" ]; then ok "$name -- '$out'"; else bad "$name" "expected '$want', got '$out'"; fi
}
expect_bind_refuse() {
  local name="$1"; shift
  local out rc
  if out="$(aireos_loopback_parse_bind_args "$@" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -eq 0 ]; then bad "$name" "expected refusal, got acceptance ('$out')"; else ok "$name -- refused"; fi
}

expect_bind_ok "well-formed uuid" "0190abcd-0000-7000-8000-000000000001" --resource-id 0190abcd-0000-7000-8000-000000000001
expect_bind_refuse "no arguments"
expect_bind_refuse "missing value"        --resource-id
expect_bind_refuse "empty value"          --resource-id ""
expect_bind_refuse "unknown flag"         --bogus x

echo "== aireos_loopback_authkey_line =="

MARKER="aireos-loopback-demo"
# A real `ssh-keygen -t ed25519` public-key field: always exactly 68 base64
# characters (see the implementation's comment for the byte-math). Using a
# genuine one here — not a shortened stand-in — is what makes the length
# check below meaningful.
GOOD_KEYDATA="AAAAC3NzaC1lZDI1NTE5AAAAIDBW1UcckoHRnBiFg8uDK8QWN0hpPHzWKFGhwim2P5Cr"
GOOD_PUB="ssh-ed25519 $GOOD_KEYDATA someone@somewhere"

# expect_line_ok <name> <raw-pubkey> — asserts the built line carries the
# restrict options, the original type+key verbatim, and ends with our marker
# (not whatever comment the raw line carried).
expect_line_ok() {
  local name="$1" raw="$2" out rc
  if out="$(aireos_loopback_authkey_line "$raw" "$MARKER" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -ne 0 ]; then bad "$name" "expected a built line, got refusal: $out"; return; fi
  case "$out" in
    "restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty ssh-ed25519 $GOOD_KEYDATA $MARKER")
      ok "$name -- '$out'" ;;
    *) bad "$name" "unexpected line: '$out'" ;;
  esac
}
expect_line_refuse() {
  local name="$1" raw="$2" out rc
  if out="$(aireos_loopback_authkey_line "$raw" "$MARKER" 2>&1)"; then rc=0; else rc=$?; fi
  if [ "$rc" -eq 0 ]; then bad "$name" "expected refusal, got a built line: '$out'"; else ok "$name -- refused"; fi
}

expect_line_ok "well-formed ssh-ed25519 line, comment discarded" "$GOOD_PUB"
expect_line_ok "well-formed line with no comment at all" "ssh-ed25519 $GOOD_KEYDATA"

expect_line_refuse "wrong key type (rsa, not ed25519)" "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC someone@somewhere"
expect_line_refuse "empty input"                        ""
expect_line_refuse "type only, no key material"          "ssh-ed25519"
expect_line_refuse "key material truncated (looks like valid base64 but is the wrong length — the alphabet check alone would miss this)" "ssh-ed25519 AAAA someone@somewhere"
expect_line_refuse "key material has shell metacharacters" 'ssh-ed25519 AAAA;rm-rf someone@somewhere'
expect_line_refuse "garbage, not a key line at all"      "not a key line"

# empty marker must also refuse — an unmarked line can never be found again by
# undo, so installing one would be a silent one-way authorization.
out="$(aireos_loopback_authkey_line "$GOOD_PUB" "" 2>&1)" && rc=0 || rc=$?
if [ "${rc:-1}" -ne 0 ]; then ok "empty marker -- refused"; else bad "empty marker" "expected refusal, got '$out'"; fi

echo "== aireos_loopback_strip_marker (the undo matcher) =="

MARKED_LINE="restrict,no-pty ssh-ed25519 AAAA $MARKER"
OTHER_LINE="ssh-ed25519 BBBB someone-elses-key@laptop"

# expect_strip <name> <content> <want>
expect_strip() {
  local name="$1" content="$2" want="$3" out
  out="$(aireos_loopback_strip_marker "$content" "$MARKER")"
  if [ "$out" = "$want" ]; then
    ok "$name"
  else
    bad "$name" "expected '$want', got '$out'"
  fi
}

expect_strip "removes the sole marked line, leaving nothing" "$MARKED_LINE" ""
expect_strip "leaves an unrelated line untouched" "$OTHER_LINE" "$OTHER_LINE"
expect_strip "removes only the marked line among several" \
  "$(printf '%s\n%s' "$OTHER_LINE" "$MARKED_LINE")" \
  "$OTHER_LINE"
expect_strip "removes the marked line wherever it sits (first)" \
  "$(printf '%s\n%s' "$MARKED_LINE" "$OTHER_LINE")" \
  "$OTHER_LINE"
expect_strip "empty content stays empty" "" ""
expect_strip "a line that merely CONTAINS the marker word, but not as its comment, is untouched" \
  "ssh-ed25519 CCCC a-comment-that-mentions-${MARKER}-in-passing-but-isnt-the-tag" \
  "ssh-ed25519 CCCC a-comment-that-mentions-${MARKER}-in-passing-but-isnt-the-tag"

echo
echo "== OI-38: socket-activated sshd is a supported precondition, and this test asserts it (T-133a) =="
# Everything above sources the script to unit-test its pure helpers -- no
# root, no sshd, no systemd. This check is different: OI-38 is about the
# `generate` mode's REAL sshd-reachability probe (lines ~257-262 of
# setup-loopback-demo.sh), which lives below the sourceable guard and runs
# only when the script is invoked as its own process. Proving it needs root
# (the script's own `[ "$(id -u)" = "0" ]` gate) and a way to control
# `systemctl is-active`, so — same pattern as install_audit_key_test.sh —
# this SKIPS rather than failing when not run as root; run inside golang:1.26
# (or another root container) to actually exercise it.
if [ "$(id -u)" != "0" ]; then
  echo "SKIP OI-38 live check -- needs root to reach setup-loopback-demo.sh's real CLI entry; run inside golang:1.26 (or another root container)"
else
  FAKEBIN="$(mktemp -d)"
  # A minimal `systemctl is-active --quiet <unit>` stand-in: only ssh.socket
  # reports active -- ssh/sshd themselves stay inactive, exactly Ubuntu
  # 24.04's socket-activated sshd shape (this product's own primary install
  # target) that blocked the documented quickstart at v0.12.0's GATE-4.
  cat > "$FAKEBIN/systemctl" <<'FAKE'
#!/bin/sh
unit=""
for a in "$@"; do unit="$a"; done
case "$unit" in
  ssh.socket) exit 0 ;;
  ssh|sshd) exit 1 ;;
  *) exit 1 ;;
esac
FAKE
  chmod +x "$FAKEBIN/systemctl"

  OI38_ACCOUNT="aireos-oi38-nonexistent-$$"
  OI38_PORT=39217   # an ephemeral port nothing listens on in this test container
  # The sourced setup-loopback-demo.sh left `set -e` shell-global (see this
  # file's header note) -- a bare `var="$(failing-cmd)"` would abort THIS
  # script right here, before ever inspecting the result. Same `if`-guarded
  # idiom the expect_* helpers above use.
  if OI38_OUT="$(PATH="$FAKEBIN:$PATH" bash "$SCRIPT_DIR/setup-loopback-demo.sh" generate --account "$OI38_ACCOUNT" --port "$OI38_PORT" 2>&1)"; then
    OI38_RC=0
  else
    OI38_RC=$?
  fi
  rm -rf "$FAKEBIN"

  # The reachability check must NOT reject a socket-activated-only sshd (that
  # is the OI-38 regression). The script should instead pass it and die LATER
  # at the next, unrelated check (the account does not exist) -- proving it
  # actually got past the sshd gate rather than never reaching it for some
  # other reason.
  if echo "$OI38_OUT" | grep -q "no sshd reachable"; then
    bad "OI-38: accepts a socket-activated sshd" "the script rejected ssh.socket-only as if no sshd were reachable at all -- output: $OI38_OUT"
  elif [ "$OI38_RC" -ne 0 ] && echo "$OI38_OUT" | grep -q "no such account"; then
    ok "OI-38: accepts a socket-activated sshd -- passed the reachability check, died later at the unrelated/expected 'no such account' check"
  else
    bad "OI-38: accepts a socket-activated sshd" "unexpected outcome (rc=$OI38_RC): $OI38_OUT"
  fi
fi

echo
echo "== summary: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
