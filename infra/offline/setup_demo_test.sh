#!/usr/bin/env bash
# setup_demo_test.sh — T-176c2: the install-time demo step and its undo (ADR-0079 D3/D6/D7; FRX-5/FRX-6).
# Runs setup-loopback-demo.sh's REAL host helpers (the ones setup.sh calls) against a fake connector-service, a real
# account and a real authorized_keys, and drives `setup.sh --remove-demo` itself. The end-to-end proof, against a real
# connector, sshd and a governed action, is later-host-test.sh leg D.
#
#   decision     AIREOS_LOOPBACK_DEMO=yes|no wins and asks nothing; unset + terminal -> the answer, empty = Yes;
#                unset + no terminal -> no; anything else is refused
#   prepare      account created with no usable password; ONE connector call; authorized_keys holds EXACTLY RESTRICT_OPTS
#                + the key + the marker; nothing private on disk; the connector's env never reaches argv
#   re-run       a prepared demo is skipped and a second key is never generated
#   skip         no SSH banner on 22 -> one sentence, nothing created, the connector never called
#   remove       the entry (only ours) and the pending credential go; account only when asked AND created by setup;
#                a second run changes nothing
#
# It creates users and writes /etc/aireos, /opt/aireos and /var/lib/aireos, so like install_prompts_test.sh it only
# runs in a container:
#   MSYS_NO_PATHCONV=1 docker run --rm -v "D:/path/to/repo:/work" -w /work golang:1.26 bash infra/offline/setup_demo_test.sh
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "$(id -u)" != 0 ] || { [ ! -f /.dockerenv ] && [ "${AIREOS_TEST_DISPOSABLE:-}" != 1 ]; }; then
  echo "SKIP setup_demo_test.sh -- needs root in a disposable container (see this file's header)"; exit 0
fi
pass=0; fail=0
ok() { echo "PASS $1"; pass=$((pass + 1)); }
bad() { echo "FAIL $1 -- $2"; fail=$((fail + 1)); }
check() { local name="$1"; shift; if "$@"; then ok "$name"; else bad "$name" "condition false"; fi; }

# shellcheck disable=SC1091
source "$SCRIPT_DIR/setup-loopback-demo.sh"
set +e   # the sourced script turns -e on; this test checks return codes itself
log() { :; }   # quiet; the helpers' messages are asserted through return codes and files
ACCT="$DEMO_ACCOUNT_DEFAULT"; LOG=/tmp/fake-connector.log; : > "$LOG"
getent group aireos >/dev/null || groupadd aireos
rm -rf /etc/aireos /opt/aireos /var/lib/aireos; install -d /etc/aireos /opt/aireos/bin
[ -f /etc/ssh/ssh_host_ed25519_key.pub ] || { mkdir -p /etc/ssh; ssh-keygen -A >/dev/null; }
DBURL='postgres://u:p@localhost:5432/d'; RKEY="resource-key-$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')"
printf 'DATABASE_URL=%s\n' "$DBURL" > /etc/aireos/aireos.env; printf 'AIREOS_RESOURCE_SECRET_KEY=%s\n' "$RKEY" > /etc/aireos/connector.env
ssh-keygen -q -t ed25519 -N '' -f /tmp/fakepub; FAKEPUB="$(awk '{print $1" "$2}' /tmp/fakepub.pub)"
cat > /opt/aireos/bin/connector-service <<'EOF'
#!/bin/sh
# records its argv and whether the secrets arrived as ENVIRONMENT; prints only a public key for generate-pending
echo "$* env_db=${DATABASE_URL:+set} env_key=${AIREOS_RESOURCE_SECRET_KEY:+set}" >> /tmp/fake-connector.log
[ "$2" = generate-pending ] && cat /tmp/fakepub.pub | awk '{print $1" "$2}'
exit 0
EOF
chmod +x /opt/aireos/bin/connector-service
banner_yes() { return 0; }; banner_no() { return 1; }
calls() { grep -c "credentials $1" "$LOG" || true; }
authkeys() { echo "/home/$ACCT/.ssh/authorized_keys"; }
cleanup_acct() { userdel --remove "$ACCT" >/dev/null 2>&1 || true; rm -rf "$STATE_DIR"; : > "$LOG"; }

echo "== decision: aireos_demo_decision <env> <tty> <answer> =="
for c in "yes|0||yes" "no|1|y|no" "YES|0||yes" "|1||yes" "|1|y|yes" "|1|Yes|yes" "|1|n|no" "|1|nope|no" "|0||no" "|0|yes|no"; do
  IFS='|' read -r e t a want <<<"$c"; got="$(aireos_demo_decision "$e" "$t" "$a")"
  [ "$got" = "$want" ] && ok "decision env='$e' tty=$t answer='$a' -> $want" || bad "decision env='$e' tty=$t answer='$a'" "got '$got', want '$want'"
done
aireos_demo_decision maybe 1 "" >/dev/null 2>&1 && bad "decision: AIREOS_LOOPBACK_DEMO=maybe" "was accepted" || ok "decision: AIREOS_LOOPBACK_DEMO=maybe is refused"
OUT="$(AIREOS_LOOPBACK_DEMO=maybe bash "$SCRIPT_DIR/setup.sh" 2>&1)"; rc=$?
{ [ "$rc" != 0 ] && grep -q 'must be yes or no' <<<"$OUT" && grep -q 'Nothing was changed' <<<"$OUT"; } && ok "setup.sh refuses a bad AIREOS_LOOPBACK_DEMO before changing anything" || bad "setup.sh bad env" "rc=$rc $OUT"

echo "== the terminal question: asked once, says what it creates, empty = Yes =="
if command -v script >/dev/null 2>&1; then
  for ans in "" "n"; do
    printf '%s\n' "$ans" | script -qec "bash -c 'source $SCRIPT_DIR/setup-loopback-demo.sh; aireos_loopback_ask_demo /opt/bundle; echo ANS=[\$DEMO_ANSWER]'" /dev/null > /tmp/ask.out 2>&1
    want=yes; [ -n "$ans" ] && want=no; got="$(grep -o 'ANS=\[[^]]*\]' /tmp/ask.out | sed 's/ANS=\[\(.*\)\]/\1/')"
    [ "$(aireos_demo_decision "" 1 "$got")" = "$want" ] && ok "prompt answer '$ans' -> $want" || bad "prompt answer '$ans'" "got '$got': $(cat -v /tmp/ask.out)"
  done
  { grep -q "$ACCT" /tmp/ask.out && grep -q 'restricted SSH key' /tmp/ask.out && grep -q 'sudo /opt/bundle/setup.sh --remove-demo' /tmp/ask.out; } \
    && ok "the question names the account, the restricted key and the undo command" || bad "question text" "$(cat -v /tmp/ask.out)"
  [ "$(grep -c 'Set up the demo target?' /tmp/ask.out)" = 1 ] && ok "the question appears once" || bad "asked once" "$(grep -c 'Set up the demo target?' /tmp/ask.out)"
else echo "SKIP pty leg -- 'script' not installed"; fi

echo "== the SSH precheck is a real connection =="
python3 - <<'EOF' &
import socket,threading,time
def serve(port,msg):
    s=socket.socket(); s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1); s.bind(("127.0.0.1",port)); s.listen(5)
    def run():
        while True:
            c,_=s.accept(); c.sendall(msg); time.sleep(1); c.close()
    threading.Thread(target=run,daemon=True).start()
serve(22291,b"SSH-2.0-OpenSSH_9.6\r\n"); serve(22292,b"HTTP/1.1 400 Bad Request\r\n")
time.sleep(30)
EOF
LISTENER=$!; sleep 1
aireos_loopback_ssh_banner 22291 && ok "a port that sends an SSH banner passes" || bad "banner" "SSH banner refused"
aireos_loopback_ssh_banner 22292 && bad "banner" "a non-SSH service passed" || ok "a port that answers something else fails"
aireos_loopback_ssh_banner 22293 && bad "banner" "a closed port passed" || ok "a closed port fails"
kill "$LISTENER" 2>/dev/null

echo "== prepare: the demo is made once, from the connector's public half only =="
cleanup_acct; aireos_loopback_ssh_banner() { banner_yes; }
aireos_loopback_prepare_demo; rc=$?
[ "$rc" = 0 ] && ok "prepare returns 0" || bad "prepare" "rc=$rc"
[ "$(getent shadow "$ACCT" | cut -d: -f2)" = '*' ] && ok "the account has no usable password" || bad "account password" "$(getent shadow "$ACCT")"
[ "$(getent passwd "$ACCT" | cut -d: -f7)" = /bin/bash ] && ok "the account has a real login shell" || bad "shell" "$(getent passwd "$ACCT")"
[ "$(cat "$(authkeys)")" = "$RESTRICT_OPTS $FAKEPUB $MARKER" ] && ok "authorized_keys holds EXACTLY RESTRICT_OPTS + the key + the marker" || bad "authorized_keys" "$(cat "$(authkeys)")"
[ "$(stat -c '%a %U' "$(authkeys)")" = "600 $ACCT" ] && ok "authorized_keys is 0600 and owned by the account" || bad "authorized_keys mode" "$(stat -c '%a %U' "$(authkeys)")"
[ "$(calls generate-pending)" = 1 ] && ok "exactly one generate-pending call" || bad "generate-pending count" "$(calls generate-pending)"
grep -q -- "--label loopback-demo --account $ACCT --host-key-file /etc/ssh/ssh_host_ed25519_key.pub env_db=set env_key=set" "$LOG" \
  && ok "the call carries label/account/host-key-file, and the DB URL and resource key arrive as environment" || bad "connector argv" "$(cat "$LOG")"
grep -qF -e "$DBURL" -e "$RKEY" "$LOG" && bad "argv secrecy" "a secret appeared in the connector's argv" || ok "neither the DB URL nor the resource key appears in argv"
grep -rIl -e 'PRIVATE KEY' "$STATE_DIR" "/home/$ACCT" 2>/dev/null | grep -q . && bad "private key on disk" "found one" || ok "no private key anywhere in the state dir or the account's home"
[ ! -e "$STATE_DIR/id_ed25519" ] && ok "no id_ed25519 in the state dir" || bad "id_ed25519" "exists"
[ "$(aireos_read_state ACCOUNT_CREATED)" = yes ] && ok "state records that setup created the account" || bad "ACCOUNT_CREATED" "$(cat "$STATE_FILE")"

echo "== re-run: a prepared demo is skipped; no second key =="
aireos_loopback_prepare_demo; rc=$?
{ [ "$rc" = 10 ] && [ "$(calls generate-pending)" = 1 ] && [ "$(wc -l < "$(authkeys)")" = 1 ]; } && ok "re-run returns 10, generates no second key, leaves authorized_keys at one line" || bad "re-run" "rc=$rc generate-pending=$(calls generate-pending)"
# negative control: with the skip check disabled the SAME re-run DOES mint a second key, so the assertion above can fail
( aireos_loopback_demo_present() { return 1; }; aireos_loopback_prepare_demo >/dev/null 2>&1 )
[ "$(calls generate-pending)" = 2 ] && ok "negative control: without the skip, a second key IS generated (the re-run assertion can fail)" || bad "negative control" "generate-pending=$(calls generate-pending)"

echo "== remove: only our entry goes; idempotent =="
printf '%s\n' "ssh-ed25519 AAAAoperatorsownkey operator@laptop" >> "$(authkeys)"
aireos_loopback_remove_demo; : > "$LOG"
{ ! grep -q " $MARKER\$" "$(authkeys)" && grep -q operator@laptop "$(authkeys)"; } && ok "our entry is gone and the operator's own key survives" || bad "remove" "$(cat "$(authkeys)")"
sum1="$(cksum < "$(authkeys)")"; aireos_loopback_remove_demo
[ "$(cksum < "$(authkeys)")" = "$sum1" ] && ok "a second remove changes nothing" || bad "second remove" "authorized_keys changed"
[ "$(calls remove-pending)" -ge 1 ] && ok "remove-pending was called" || bad "remove-pending" "$(cat "$LOG")"

echo "== setup.sh --remove-demo: account only when asked, and only if setup created it =="
OUT="$(AIREOS_REMOVE_DEMO_ACCOUNT=no bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null 2>&1)"; rc=$?
{ [ "$rc" = 0 ] && id "$ACCT" >/dev/null 2>&1; } && ok "AIREOS_REMOVE_DEMO_ACCOUNT=no keeps the account" || bad "keep account" "rc=$rc $OUT"
{ grep -q 'NOT the stored key' <<<"$OUT" && grep -q -- '--undo --resource-id' <<<"$OUT"; } && ok "it says the cockpit's removal leaves the stored key, and how to delete it" || bad "bound-key note" "$OUT"
OUT="$(bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null 2>&1)"
id "$ACCT" >/dev/null 2>&1 && ok "no terminal and no variable: the account is kept (default No)" || bad "default no" "$OUT"
OUT="$(AIREOS_REMOVE_DEMO_ACCOUNT=yes bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null 2>&1)"; rc=$?
{ [ "$rc" = 0 ] && ! id "$ACCT" >/dev/null 2>&1; } && ok "AIREOS_REMOVE_DEMO_ACCOUNT=yes removes the account setup created" || bad "remove account" "rc=$rc $OUT"
OUT="$(AIREOS_REMOVE_DEMO_ACCOUNT=yes bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null 2>&1)"; rc=$?
[ "$rc" = 0 ] && ok "a second --remove-demo succeeds" || bad "second --remove-demo" "rc=$rc $OUT"
rm -rf "$STATE_DIR"; useradd --create-home -s /bin/bash "$ACCT"   # an account NOT created by setup: no ACCOUNT_CREATED state
AIREOS_REMOVE_DEMO_ACCOUNT=yes bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null >/dev/null 2>&1
id "$ACCT" >/dev/null 2>&1 && ok "an account setup did not create is never deleted" || bad "foreign account" "was deleted"

echo "== the demo account must not be able to steer root: symlinked authorized_keys and symlinked ~/.ssh (T-172a class) =="
# Root-run steps (prepare, generate, --remove-demo, --undo) must never touch a path the account planted a link to. The canary is a
# root-only file / directory the account has no right to; its owner, mode and content (and the directory listing) must not change.
FAKEBIN=/tmp/fakebin; mkdir -p "$FAKEBIN"; printf '#!/bin/sh\nexit 0\n' > "$FAKEBIN/systemctl"; chmod +x "$FAKEBIN/systemctl"   # "an sshd is active" for the CLI modes
as_acct() { setpriv --reuid "$(id -u "$ACCT")" --regid "$(id -g "$ACCT")" --clear-groups "$@"; }
canary_state() { stat -c '%U:%G %a' "$1"; cat "$1"; }
steer_runs() {   # run every root-run mode once; all must leave the canary alone
  aireos_loopback_prepare_demo >/dev/null 2>&1 || true
  aireos_loopback_remove_demo >/dev/null 2>&1 || true
  PATH="$FAKEBIN:$PATH" bash "$SCRIPT_DIR/setup-loopback-demo.sh" generate --account "$ACCT" >/dev/null 2>&1 || true
  PATH="$FAKEBIN:$PATH" bash "$SCRIPT_DIR/setup-loopback-demo.sh" --undo >/dev/null 2>&1 || true
  bash "$SCRIPT_DIR/setup.sh" --remove-demo </dev/null >/dev/null 2>&1 || true
}
cleanup_acct; aireos_loopback_ssh_banner() { banner_yes; }
useradd --create-home --shell /bin/bash -p '*' "$ACCT"; aireos_write_state ACCOUNT_CREATED yes
printf 'root-only canary\n' > /etc/aireos-canary; chmod 600 /etc/aireos-canary; before="$(canary_state /etc/aireos-canary)"
# (1) ~/.ssh/authorized_keys -> a root-only file
as_acct mkdir -p "/home/$ACCT/.ssh"; as_acct ln -sfn /etc/aireos-canary "/home/$ACCT/.ssh/authorized_keys"
steer_runs
[ "$(canary_state /etc/aireos-canary)" = "$before" ] && ok "authorized_keys -> /etc/<root file>: owner, mode and content unchanged after prepare, remove, generate, --undo, --remove-demo" || bad "symlinked authorized_keys" "the canary changed: $(canary_state /etc/aireos-canary)"
# (2) ~/.ssh itself -> a root-owned directory holding an authorized_keys
rm -rf "/home/$ACCT/.ssh"; mkdir -p /etc/aireos-canary.d; printf 'root-only canary\n' > /etc/aireos-canary.d/authorized_keys; chmod 600 /etc/aireos-canary.d/authorized_keys
dbefore="$(canary_state /etc/aireos-canary.d/authorized_keys; ls -la /etc/aireos-canary.d)"
as_acct ln -sfn /etc/aireos-canary.d "/home/$ACCT/.ssh"
steer_runs
[ "$(canary_state /etc/aireos-canary.d/authorized_keys; ls -la /etc/aireos-canary.d)" = "$dbefore" ] && ok "~/.ssh -> a root-owned directory: its file and its listing unchanged after the same five runs" || bad "symlinked ~/.ssh" "the canary directory changed"
# the control, in the same run: the SAME runs, with the account's link pointing at a path the account CAN write, do take effect
as_acct rm -f "/home/$ACCT/.ssh"; as_acct mkdir -p "/home/$ACCT/.ssh"; as_acct ln -sfn "/home/$ACCT/real_keys" "/home/$ACCT/.ssh/authorized_keys"; as_acct touch "/home/$ACCT/real_keys"
aireos_loopback_prepare_demo >/dev/null 2>&1; rc=$?
[ -f "/home/$ACCT/.ssh/authorized_keys" ] && [ ! -L "/home/$ACCT/.ssh/authorized_keys" ] && grep -q " $MARKER\$" "/home/$ACCT/.ssh/authorized_keys" && ok "control: the demo step still works over a link the account itself owns (rename replaces the link)" || bad "control" "rc=$rc $(ls -la /home/$ACCT/.ssh)"
rm -rf /etc/aireos-canary /etc/aireos-canary.d

echo "== skip: no SSH server on 22 -> nothing created, connector never called =="
cleanup_acct; aireos_loopback_ssh_banner() { banner_no; }
aireos_loopback_prepare_demo; rc=$?
{ [ "$rc" = 1 ] && ! id "$ACCT" >/dev/null 2>&1 && [ ! -s "$LOG" ]; } && ok "prepare returns 1, creates no account, calls no connector" || bad "skip" "rc=$rc log=$(cat "$LOG")"

cleanup_acct
echo "== summary: $pass passed, $fail failed =="
[ "$fail" = 0 ]
