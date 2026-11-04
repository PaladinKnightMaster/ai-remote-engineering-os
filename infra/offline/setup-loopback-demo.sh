#!/usr/bin/env bash
# setup-loopback-demo.sh — the operator-run HOST half of the ONB-3 loopback
# demo target (T-118b; ADR-0068 §D8, release criteria ONB-3/ONB-4). Run as
# root on the installed host, AFTER install.sh.
#
# The CEO ratified a split of trust over a one-click path: giving a service
# the ability to write authorized_keys on its own host is a new privileged
# capability this release cannot justify (its capstone is a security audit).
# So this script — run BY THE OPERATOR, never by a service — does everything
# that touches the filesystem or SSH trust; the cockpit (POST /v1/resources,
# POST /v1/runs) does everything that is governed. Neither side crosses into
# the other's half: this script NEVER creates the resource row (that would be
# writing the database directly, bypassing the governed create path ONB-4
# requires), and the cockpit NEVER touches the filesystem.
#
# v0.17.0 (ADR-0079): the KEY is generated INSIDE connector-service, which keeps the
# private half; this script only ever receives the PUBLIC half. No private key is
# written to disk any more. setup.sh now does this for you at install time (opt-in)
# and the cockpit's 'Create loopback demo target' button binds the key; this script
# remains for operators who declined the demo at setup, or who want the manual form:
#
#   1. generate --account <name> [--port 22]
#        Asks the connector for a PENDING key (`credentials generate-pending`),
#        installs its public half into <name>'s authorized_keys with a restricted
#        option set (see below), and prints the values the cockpit's manual form
#        asks for: host, port, username, AND the host's own SSH host public key
#        (ssh_host_key - see the note below). The cockpit's one-click button needs
#        none of them.
#
#   2. bind --resource-id <uuid>      (manual form only)
#        The one-click button binds for you. After the MANUAL form, this moves the
#        pending key onto the resource you created (`credentials bind-pending`). It
#        also still loads a private key left on disk by a pre-v0.17.0 generate.
#        The id is server-assigned (POST /v1/resources rejects a client-supplied
#        id), which is why binding is a separate step from generating.
#
#   --undo [--resource-id <uuid>]
#        Removes the authorized_keys entry and the pending credential. For a key the
#        cockpit already bound, add --resource-id to delete that stored credential too.
#        Idempotent at any phase.
#
# A note on ssh_host_key: the release criteria's cockpit fields are named as
# "host, port, username", but services/session-broker/internal/store/store.go
# LoadTargetCoords treats ssh_host_key as a MANDATORY resource label
# (ErrTargetIncomplete if absent) — host-key pinning via ssh.FixedHostKey is
# not optional on either the interactive or the non-interactive (remote.exec)
# path. Omitting it would create a resource that can never actually connect,
# defeating ONB-3. This script prints it as a fourth value alongside host/
# port/username; see the ticket report for this called out explicitly.
#
# A note on no-pty: services/mcp-gateway/internal/brokerexec/brokerexec.go
# (the client the governed remote.exec chain uses) hands off to
# services/session-broker/internal/sshpty/exec.go, documented in its own
# header as "Non-interactive command exec over SSH ... runs ONE command (no
# PTY, no shell)". The governed demo action never allocates a PTY, so
# `no-pty` in the restricted authorized_keys entry below is safe — it is not
# a workaround, it reflects what the product's own governed exec path does.
#
# Env vars (all optional; defaults match install.sh's layout):
#   AIREOS_ETC     default /etc/aireos            (aireos.env + connector.env)
#   AIREOS_PREFIX  default /opt/aireos             (bin/connector-service)
#   AIREOS_STATE   default /var/lib/aireos/loopback-demo
#                  (the tiny ACCOUNT/PORT/RESOURCE_ID state file this script
#                  itself owns; readable by the aireos service user)
#   AIREOS_SVC_USER default aireos (the service user/group the state
#                  directory is readable by; matches install.sh's SVC_USER)
set -euo pipefail

log() { printf '\033[1;34m[loopback-demo]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[loopback-demo] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

ETC="${AIREOS_ETC:-/etc/aireos}"
PREFIX="${AIREOS_PREFIX:-/opt/aireos}"
STATE_DIR="${AIREOS_STATE:-/var/lib/aireos/loopback-demo}"
SVC_USER="${AIREOS_SVC_USER:-aireos}"
KEY_FILE="$STATE_DIR/id_ed25519"   # LEGACY only: a pre-v0.17.0 generate wrote its private key here
PUB_FILE="$STATE_DIR/id_ed25519.pub"
STATE_FILE="$STATE_DIR/state"

# The comment stamped on the authorized_keys line this script installs — the
# sole anchor undo (and a re-run of generate) uses to find and remove exactly
# that line, never any entry the operator or another tool added by hand.
MARKER="aireos-loopback-demo"

# The restricted option set (OpenSSH authorized_keys(5)). remote.exec never
# needs a PTY (see header note), so all four restrictions plus the umbrella
# `restrict` (which implies them and more — port/agent/X11 forwarding, PTY
# allocation are all denied) are safe to combine; `restrict` alone would
# already cover the other four, but they are listed explicitly so the intent
# reads without cross-referencing ssh_config(5)'s `restrict` expansion.
RESTRICT_OPTS="restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty"

# ---------------------------------------------------------------------------
# Pure helpers (sourceable + unit-tested by setup_loopback_demo_test.sh).
# No filesystem/network/systemctl access below this point until the
# sourceable guard.
# ---------------------------------------------------------------------------

# aireos_loopback_parse_generate_args <args...> — prints "ACCOUNT|PORT" on
# success. --account must look like a plausible unix account name; --port
# (default 22) must be numeric. Non-zero + a stderr message on any problem.
aireos_loopback_parse_generate_args() {
  local account="" port="22"
  while [ $# -gt 0 ]; do
    case "$1" in
      --account)
        [ $# -ge 2 ] || { echo "--account requires a value" >&2; return 1; }
        account="$2"; shift 2 ;;
      --port)
        [ $# -ge 2 ] || { echo "--port requires a value" >&2; return 1; }
        port="$2"; shift 2 ;;
      *) echo "generate: unknown argument '$1'" >&2; return 1 ;;
    esac
  done
  case "$account" in
    "") echo "generate requires --account <a dedicated unix account name>" >&2; return 1 ;;
    *[!A-Za-z0-9_-]*) echo "generate: --account '$account' is not a plausible unix account name" >&2; return 1 ;;
  esac
  case "$port" in
    ""|*[!0-9]*) echo "generate: --port '$port' must be numeric" >&2; return 1 ;;
  esac
  printf '%s|%s\n' "$account" "$port"
}

# aireos_loopback_parse_bind_args <args...> — prints the resource-id on
# success.
aireos_loopback_parse_bind_args() {
  local rid=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --resource-id)
        [ $# -ge 2 ] || { echo "--resource-id requires a value" >&2; return 1; }
        rid="$2"; shift 2 ;;
      *) echo "bind: unknown argument '$1'" >&2; return 1 ;;
    esac
  done
  [ -n "$rid" ] || { echo "bind requires --resource-id <uuid>" >&2; return 1; }
  if ! aireos_loopback_is_uuid "$rid"; then
    # PUB-6 P14: a pasted placeholder ("PASTE-THE-ID") used to be forwarded to
    # connector-service and surface as a raw SQLSTATE 22P02. Say what an id
    # looks like and where it comes from instead.
    echo "bind: '$rid' is not a resource id. A resource id is 36 characters: 8-4-4-4-12 hex digits separated by dashes, for example 0190abcd-0000-7000-8000-000000000001. Use the id shown in the cockpit for the loopback demo resource you created, not placeholder text from a guide." >&2
    return 1
  fi
  printf '%s\n' "$rid"
}

# aireos_loopback_is_uuid <value> — true iff <value> is exactly the canonical
# 8-4-4-4-12 hexadecimal UUID shape (any case, any version digit).
aireos_loopback_is_uuid() {
  [[ "$1" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$ ]]
}

# aireos_loopback_authkey_line <raw-pubkey-line> <marker> — prints the fully
# restricted authorized_keys line to install, given the raw line ssh-keygen
# wrote to the .pub file ("<type> <base64> [comment...]"). Fails (empty
# stdout, non-zero) on anything that is not a well-formed ssh-ed25519 public
# key line: a malformed entry either silently fails to authorize, or
# authorizes more than intended, and neither is acceptable here. The
# installed line always carries OUR marker as its comment, discarding
# whatever comment ssh-keygen generated (typically user@host) — so undo's
# match is exact regardless of what host/user produced the key.
aireos_loopback_authkey_line() {
  local raw="$1" marker="$2" type keydata
  type="$(printf '%s' "$raw" | awk '{print $1}')"
  keydata="$(printf '%s' "$raw" | awk '{print $2}')"
  [ "$type" = "ssh-ed25519" ] || return 1
  [ -n "$keydata" ] || return 1
  case "$keydata" in
    *[!A-Za-z0-9+/=]*) return 1 ;;
  esac
  # An ssh-ed25519 public key's base64 field is ALWAYS exactly 68 characters
  # (SSH wire format: 4+11 bytes for the type string, 4+32 for the 32-byte
  # key, 51 bytes total, base64-encoded with no padding needed — verified
  # against real `ssh-keygen -t ed25519` output). This catches the truncated/
  # corrupted case the docstring warns about that a bare alphabet check
  # cannot: a short valid-looking fragment like "AAAA" passes the alphabet
  # test but is not a real key.
  [ "${#keydata}" -eq 68 ] || return 1
  [ -n "$marker" ] || return 1
  printf '%s %s %s %s\n' "$RESTRICT_OPTS" "$type" "$keydata" "$marker"
}

# aireos_loopback_strip_marker <content> <marker> — prints <content> with any
# line ENDING in " <marker>" removed. Anchored at end-of-line (not a bare
# substring test) so a comment that merely mentions the marker in passing
# survives — only the line whose actual trailing comment field IS the marker
# is removed. Used by both `generate` (replace a stale entry before appending
# a fresh one — idempotent re-run) and `--undo` (remove it for good). Safe on
# empty content and on content with no matching line (prints content
# unchanged); never errors under `set -e` at the call site. $marker is always
# this script's own hardcoded constant (never external input), so using it
# unescaped in the regex is safe.
aireos_loopback_strip_marker() {
  local content="$1" marker="$2"
  if [ -z "$content" ]; then
    printf ''
    return 0
  fi
  # Anchored at end-of-line (not a bare substring match): a comment that
  # merely mentions the marker in passing, without it being the actual
  # trailing comment field, must survive.
  printf '%s\n' "$content" | grep -vE " ${marker}\$" || true
}

# ---------------------------------------------------------------------------
# Host helpers (T-176c; ADR-0079 D3/D6/D7). FUNCTIONS ONLY: nothing runs when this
# file is sourced. setup.sh sources it, so the install-time demo step and this
# script's own modes share ONE implementation of the account, the restricted
# authorized_keys entry and the connector calls - and, like everything here, they
# are run by the OPERATOR as root; no service ever calls them.
# ---------------------------------------------------------------------------

DEMO_ACCOUNT_DEFAULT="aireos-demo"
DEMO_LABEL="loopback-demo"   # the connector's pending-credential label AND the resource name the cockpit looks for
DEMO_SSH_PORT=22             # fixed: POST /v1/demo-target creates the resource on 22 (Commander ruling)

# aireos_demo_decision <env-value> <is-terminal 0|1> <answer> - "yes" or "no".
# AIREOS_LOOPBACK_DEMO=yes|no decides and nothing is asked; unset: on a terminal the
# answer decides (empty = the default, Yes), off a terminal it is no.
aireos_demo_decision() {
  local envv="${1,,}" tty="$2" ans="${3,,}"
  case "$envv" in
    yes|no) printf '%s\n' "$envv"; return 0 ;;
    "") ;;
    *) echo "AIREOS_LOOPBACK_DEMO must be yes or no (it is '$1')" >&2; return 1 ;;
  esac
  [ "$tty" = 1 ] || { echo no; return 0; }
  case "$ans" in ""|y|yes) echo yes ;; *) echo no ;; esac
}

# aireos_loopback_ask_demo <setup-dir> - asks ONCE (setup.sh, on a terminal, AIREOS_LOOPBACK_DEMO unset), saying first
# exactly what will be created. Sets DEMO_ANSWER; an empty answer is the default, Yes (aireos_demo_decision).
aireos_loopback_ask_demo() {
  {
    printf '
Optional: set up a local demo target, so you can try a governed action in the browser right after setup.
'
    printf '  This creates a local account "%s" (no password), and one restricted SSH key for it: the key can run commands
' "$DEMO_ACCOUNT_DEFAULT"
    printf '  but cannot open a terminal or forward ports, and its private half is generated and kept inside AI ReOS, never written to disk.
'
    printf '  Undo it any time with:  sudo %s/setup.sh --remove-demo
' "$1"
  } >&2
  read -r -p 'Set up the demo target? [Y/n] ' DEMO_ANSWER || DEMO_ANSWER=n
}

# aireos_loopback_envval <NAME> <env-file> - the value of one line; the file is NEVER sourced.
aireos_loopback_envval() {
  if declare -F aireos_persisted_value >/dev/null; then aireos_persisted_value "$@"; return 0; fi
  [ -f "$2" ] && sed -n "s/^$1=//p" "$2" | head -n1
  return 0
}

# aireos_loopback_connector <credentials subcommand> [args...] - runs the connector CLI. The database URL and the
# resource key reach it as ENVIRONMENT only (never argv). Stdout/stderr are the CLI's own.
aireos_loopback_connector() {
  local db key
  [ -x "$PREFIX/bin/connector-service" ] || { log "connector-service CLI not found at $PREFIX/bin/connector-service"; return 1; }
  db="$(aireos_loopback_envval DATABASE_URL "$ETC/aireos.env")"
  key="$(aireos_loopback_envval AIREOS_RESOURCE_SECRET_KEY "$ETC/connector.env")"
  [ -n "$db" ] && [ -n "$key" ] || { log "DATABASE_URL (in $ETC/aireos.env) or AIREOS_RESOURCE_SECRET_KEY (in $ETC/connector.env) is missing - is this an installed host?"; return 1; }
  DATABASE_URL="$db" AIREOS_RESOURCE_SECRET_KEY="$key" "$PREFIX/bin/connector-service" credentials "$@"
}

# aireos_loopback_ssh_banner [port] - true iff something on 127.0.0.1:<port> answers with an SSH banner. An actual
# connection, never `systemctl is-active ssh`: Ubuntu 24.04's sshd is socket-activated (ssh.socket listens, ssh.service
# is inactive until a client arrives), so a unit check reports "no sshd" on a host where sshd is running (v0.12.0 GATE-4).
aireos_loopback_ssh_banner() {
  local port="${1:-22}"
  timeout 5 bash -c 'exec 3<>"/dev/tcp/127.0.0.1/$1" && read -r -t 4 line <&3 && [[ "$line" == SSH-* ]]' _ "$port" 2>/dev/null
}

# aireos_loopback_host_key_file - the sshd host public key to pin (ed25519 first); the file's path on stdout.
aireos_loopback_host_key_file() {
  local f
  for f in /etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ecdsa_key.pub /etc/ssh/ssh_host_rsa_key.pub; do
    [ -r "$f" ] && { printf '%s\n' "$f"; return 0; }
  done
  return 1
}

# aireos_read_state <key> / aireos_write_state <key> <value> - the tiny key=value state file this script owns.
aireos_read_state() {
  [ -f "$STATE_FILE" ] || { printf ''; return 0; }
  grep -E "^$1=" "$STATE_FILE" 2>/dev/null | tail -n1 | cut -d= -f2- || true
}
aireos_write_state() {
  local key="$1" value="$2" tmp
  install -d -m 750 -o root -g "$SVC_USER" "$STATE_DIR"
  tmp="$(mktemp)"
  if [ -f "$STATE_FILE" ]; then
    grep -v -E "^$key=" "$STATE_FILE" > "$tmp" 2>/dev/null || true
  fi
  printf '%s=%s\n' "$key" "$value" >> "$tmp"
  install -m 640 -o root -g "$SVC_USER" "$tmp" "$STATE_FILE"
  rm -f "$tmp"
}

# Everything inside the demo account's home is done AS THE ACCOUNT, never as root. The account (or anything running as it)
# owns ~/.ssh and may replace authorized_keys or ~/.ssh itself with a symlink to a root-only file; a root-run touch, chown, cat
# or write through that link is a privilege escalation (the T-172a class, fde52f3). Run as the account, the same operation can
# touch nothing the account could not already touch. Root only creates the account (and its home) at account-creation time.
#
# aireos_loopback_as_account <account> <present|install|remove> [entry-line]
#   present  true iff authorized_keys carries our entry
#   install  installs EXACTLY <entry-line>, replacing any earlier entry of ours, leaving every other line alone
#   remove   removes our entry; true if one was there, false (nothing rewritten) if not
# The new file is written inside ~/.ssh and renamed over authorized_keys: rename replaces a symlink, it never writes through one.
aireos_loopback_as_account() {
  local account="$1" mode="$2" entry="${3:-}" ent uid gid home
  ent="$(getent passwd "$account")" || return 1
  IFS=: read -r _ _ uid gid _ home _ <<<"$ent"
  [ -n "$home" ] && [ -d "$home" ] || { log "account '$account' has no usable home directory"; return 1; }
  command -v setpriv >/dev/null 2>&1 || { log "setpriv (util-linux) is required to edit authorized_keys as the account"; return 1; }
  setpriv --reuid "$uid" --regid "$gid" --clear-groups bash -c "$(declare -f aireos_loopback_strip_marker)"'
    set -eu; umask 077
    mode="$1"; marker="$2"; entry="$3"; ssh="$4/.ssh"; f="$ssh/authorized_keys"
    if [ "$mode" = present ]; then [ -f "$f" ] && grep -qE " ${marker}\$" "$f"; exit $?; fi
    cur=""; [ ! -f "$f" ] || cur="$(cat "$f")"
    if [ "$mode" = remove ]; then
      printf "%s\n" "$cur" | grep -qE " ${marker}\$" || exit 1
    else
      mkdir -p "$ssh"; chmod 700 "$ssh"
    fi
    str="$(aireos_loopback_strip_marker "$cur" "$marker")"
    { [ -z "$str" ] || printf "%s\n" "$str"; [ "$mode" != install ] || printf "%s\n" "$entry"; } > "$f.new"
    chmod 600 "$f.new"; mv -f "$f.new" "$f"' _ "$mode" "$MARKER" "$entry" "$home" || return 1
}

# aireos_loopback_demo_present <account> - the entry is the real artifact of a prepared demo, so a re-run needs no state of
# its own to know (and never mints a second key).
aireos_loopback_demo_present() { aireos_loopback_as_account "$1" present 2>/dev/null; }
aireos_loopback_install_authkey() { aireos_loopback_as_account "$1" install "$2"; }
aireos_loopback_remove_authkey() { aireos_loopback_as_account "$1" remove; }

# aireos_loopback_issue <account> <port> - the connector generates the key (ADR-0079 D1); only its PUBLIC half comes
# back, and goes into <account>'s authorized_keys with the restricted options. The private key never exists on this
# host's disk. A pending key left by an earlier attempt that never reached authorized_keys is useless (nobody saw its
# public half), so it is replaced rather than refused.
aireos_loopback_issue() {
  local account="$1" port="$2" hkf pub entry
  hkf="$(aireos_loopback_host_key_file)" || { log "no readable sshd host public key under /etc/ssh (checked ed25519/ecdsa/rsa) - cannot pin ssh_host_key"; return 1; }
  aireos_loopback_connector remove-pending --label "$DEMO_LABEL" >/dev/null 2>&1 || true
  pub="$(aireos_loopback_connector generate-pending --label "$DEMO_LABEL" --account "$account" --host-key-file "$hkf")" \
    || { log "the connector could not generate the demo key (the message above says why)"; return 1; }
  entry="$(aireos_loopback_authkey_line "$pub" "$MARKER")" \
    || { aireos_loopback_connector remove-pending --label "$DEMO_LABEL" >/dev/null 2>&1 || true; log "the connector returned something that is not an ssh-ed25519 public key"; return 1; }
  aireos_loopback_install_authkey "$account" "$entry" \
    || { aireos_loopback_connector remove-pending --label "$DEMO_LABEL" >/dev/null 2>&1 || true; return 1; }
  aireos_write_state ACCOUNT "$account"; aireos_write_state PORT "$port"; aireos_write_state HOST_KEY "$(cat "$hkf")"
}

# aireos_loopback_prepare_demo - setup.sh's opt-in demo step (ADR-0079 D3). Returns 0 when it prepared the demo, 10
# when it was already prepared (nothing touched), 1 when it had to skip (one plain sentence says why; the install is unaffected).
aireos_loopback_prepare_demo() {
  local account="$DEMO_ACCOUNT_DEFAULT"
  if aireos_loopback_demo_present "$account"; then
    log "The demo target is already set up for the account '$account'; nothing to do."; return 10
  fi
  aireos_loopback_ssh_banner "$DEMO_SSH_PORT" \
    || { log "The demo target was skipped: no SSH server answers on 127.0.0.1:$DEMO_SSH_PORT. AI ReOS itself is installed. To add the demo later, install and start an SSH server and run setup.sh again."; return 1; }
  if ! id "$account" >/dev/null 2>&1; then
    # '*' = no password can ever log in, yet (unlike '!') sshd still accepts the key even where UsePAM is off.
    useradd --create-home --shell /bin/bash -p '*' "$account" || { log "The demo target was skipped: could not create the account '$account'."; return 1; }
    aireos_write_state ACCOUNT_CREATED yes
    log "created the local account '$account' (no password; key login only)"
  fi
  aireos_loopback_issue "$account" "$DEMO_SSH_PORT" || { log "The demo target was skipped (see above). AI ReOS itself is installed."; return 1; }
  log "prepared the demo target: a restricted SSH key for '$account' (its private half stays inside AI ReOS)"
}

# aireos_loopback_remove_demo - ADR-0079 D7. Removes our authorized_keys entry and the PENDING credential; idempotent
# (a second run finds nothing and rewrites nothing).
aireos_loopback_remove_demo() {
  local account; account="$(aireos_read_state ACCOUNT)"; account="${account:-$DEMO_ACCOUNT_DEFAULT}"
  if aireos_loopback_remove_authkey "$account"; then log "removed the demo key from the authorized_keys of '$account'"
  else log "no demo key in the authorized_keys of '$account' - nothing to remove there"; fi
  if aireos_loopback_connector remove-pending --label "$DEMO_LABEL" >/dev/null 2>&1; then log "removed the pending demo credential (if there was one)"
  else log "could not reach the connector's store - a pending demo credential, if any, was NOT removed"; fi
}

# aireos_loopback_remove_account - deletes the demo account, but ONLY one this tooling created (state ACCOUNT_CREATED).
aireos_loopback_remove_account() {
  local account; account="$(aireos_read_state ACCOUNT)"; account="${account:-$DEMO_ACCOUNT_DEFAULT}"
  if ! id "$account" >/dev/null 2>&1; then log "the account '$account' does not exist - nothing to remove"; return 0; fi
  if [ "$(aireos_read_state ACCOUNT_CREATED)" != yes ]; then
    log "the account '$account' was not created by AI ReOS setup, so it is left alone (remove it yourself if you want it gone)"; return 0
  fi
  if userdel --remove "$account" >/dev/null 2>&1; then log "removed the account '$account' and its home directory"; else log "could not remove the account '$account'"; fi
}

# Sourceable guard (install_profile_test.sh's own shape): a test harness can
# `source` this file to unit-test the pure helpers above without running any
# of the root-only, host-mutating logic below.
if [ "${BASH_SOURCE[0]}" != "${0}" ]; then return 0; fi

# ---------------------------------------------------------------------------
# CLI entry
# ---------------------------------------------------------------------------

usage() {
  cat <<'EOF'
usage:
  setup-loopback-demo.sh generate --account <name> [--port 22]
  setup-loopback-demo.sh bind --resource-id <uuid>
  setup-loopback-demo.sh --undo [--resource-id <uuid>]
EOF
}

[ "$(id -u)" = "0" ] || die "must run as root"

MODE="${1:-}"
[ -n "$MODE" ] || { usage >&2; exit 1; }
shift || true

case "$MODE" in
  generate)
    PARSED="$(aireos_loopback_parse_generate_args "$@")" || { usage >&2; exit 1; }
    ACCOUNT="${PARSED%%|*}"
    PORT="${PARSED##*|}"

    # What this script needs is a REACHABLE sshd, not a particular systemd
    # arrangement - so test the port by connecting (see aireos_loopback_ssh_banner),
    # and fall back to the unit check only when no TCP probe is possible.
    if ! aireos_loopback_ssh_banner "$PORT"; then
      systemctl is-active --quiet ssh 2>/dev/null \
        || systemctl is-active --quiet sshd 2>/dev/null \
        || systemctl is-active --quiet ssh.socket 2>/dev/null \
        || die "no sshd reachable on 127.0.0.1:$PORT (and neither the ssh/sshd service nor ssh.socket is active) — install and start one before running this script"
    fi
    id "$ACCOUNT" >/dev/null 2>&1 || die "no such account '$ACCOUNT' — create the dedicated demo account first"

    # FIXED (T-123): the broker constrains HostKeyAlgorithms to the pinned key's own
    # type, so a multi-key sshd no longer makes the governed connection fail, whichever
    # host key type is pinned.
    log "asking the connector to generate the demo key (the private key stays inside it; nothing is written to disk here)"
    aireos_loopback_issue "$ACCOUNT" "$PORT" || die "could not prepare the demo key (the message above says why)"
    HOST_KEY_LINE="$(aireos_read_state HOST_KEY)"
    rm -f "$KEY_FILE" "$PUB_FILE"   # a private key left by the pre-v0.17.0 generate: it is superseded

    log "done. The restricted authorized_keys entry is installed for $ACCOUNT."
    log "NEXT: in the cockpit, click 'Create loopback demo target' - it creates the resource and binds the key for you."
    log "(Prefer the manual form on port ${PORT}? These are the values it asks for:)"
    echo "  host:         127.0.0.1"
    echo "  port:         $PORT"
    echo "  username:     $ACCOUNT"
    echo "  ssh host key: $HOST_KEY_LINE"
    [ "$PORT" = 22 ] || log "NOTE: the one-click button always uses port 22; for port $PORT use the manual form and then: $0 bind --resource-id <id>"
    ;;

  bind)
    # The cockpit's 'Create loopback demo target' button binds the key itself. This mode remains for the MANUAL
    # form (a resource you created by hand) and for a key left on disk by a pre-v0.17.0 generate.
    RESOURCE_ID="$(aireos_loopback_parse_bind_args "$@")" || { usage >&2; exit 1; }
    if [ -f "$KEY_FILE" ]; then
      log "loading the legacy on-disk private key into connector custody for resource $RESOURCE_ID"
      aireos_loopback_connector set-secret "$RESOURCE_ID" --key-file "$KEY_FILE" || die "could not load the legacy key"
    else
      log "binding the connector-generated demo key to resource $RESOURCE_ID"
      aireos_loopback_connector bind-pending --label "$DEMO_LABEL" --resource-id "$RESOURCE_ID" \
        || die "nothing was bound. If you used the cockpit's 'Create loopback demo target' button the key is already bound and you do not need this command; otherwise run '$0 generate --account <name>' first"
    fi
    aireos_write_state RESOURCE_ID "$RESOURCE_ID"
    log "bound — the demo resource is ready to run a governed action against"
    ;;

  --undo)
    # --resource-id: a resource the cockpit bound (the cockpit does not tell this script its id).
    UNDO_RID="$(aireos_read_state RESOURCE_ID)"
    if [ "${1:-}" = "--resource-id" ]; then UNDO_RID="$(aireos_loopback_parse_bind_args "$@")" || { usage >&2; exit 1; }; fi

    aireos_loopback_remove_demo   # the authorized_keys entry + the pending credential
    if [ -n "$UNDO_RID" ]; then
      if aireos_loopback_connector unset-secret "$UNDO_RID"; then log "removed the stored credential for resource $UNDO_RID (if it was present)"
      else log "could not remove the stored credential for resource $UNDO_RID"; fi
    else
      log "no bound resource on record - if the cockpit bound the key, add '--resource-id <id>' to also delete the stored credential"
    fi
    rm -f "$KEY_FILE" "$PUB_FILE"
    log "undo complete (idempotent — safe to run again)"
    ;;

  -h|--help)
    usage
    ;;

  *)
    usage >&2
    die "unknown mode '$MODE'"
    ;;
esac
