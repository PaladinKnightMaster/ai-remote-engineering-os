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
# Two phases, not one, because of a real ordering constraint: POST /v1/resources
# never accepts a client-supplied id (services/api/internal/server/server.go's
# decodeInput calls json.Decoder.DisallowUnknownFields, which rejects an "id"
# field with 400 — the id is always server-assigned). connector_credentials is
# keyed by that same resource_id (a SOFT reference, no cross-service FK —
# services/connector-service/migrations/0001_credentials.up.sql), so the
# credential cannot be bound to a resource that does not exist yet:
#
#   1. generate --account <name> [--port 22]
#        Generates an ed25519 CLIENT keypair, installs the PUBLIC half into
#        <name>'s authorized_keys with a restricted option set (see below),
#        and prints exactly what to paste into the cockpit's "Create loopback
#        demo target" action: host, port, username, AND the host's own SSH
#        host public key (ssh_host_key — see the note below; the private
#        client key is never printed).
#
#   2. bind --resource-id <uuid>
#        Run AFTER the cockpit call returns the new resource's id. Loads the
#        already-generated private key into connector-service custody via the
#        EXISTING `connector-service credentials set-secret` CLI — no second
#        credential path; ADR-0053 stays the sole decrypt authority.
#
#   --undo
#        Removes the authorized_keys entry and (if bound) the stored
#        credential. Idempotent at any phase — safe to run repeatedly, before
#        generate, between generate and bind, or after bind.
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
#                  (generated keypair + the tiny ACCOUNT/PORT/RESOURCE_ID
#                  state file this script itself owns; readable by the
#                  aireos service user, per the brief)
#   AIREOS_SVC_USER default aireos (the service user/group the state
#                  directory is readable by; matches install.sh's SVC_USER)
set -euo pipefail

log() { printf '\033[1;34m[loopback-demo]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[loopback-demo] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

ETC="${AIREOS_ETC:-/etc/aireos}"
PREFIX="${AIREOS_PREFIX:-/opt/aireos}"
STATE_DIR="${AIREOS_STATE:-/var/lib/aireos/loopback-demo}"
SVC_USER="${AIREOS_SVC_USER:-aireos}"
KEY_FILE="$STATE_DIR/id_ed25519"
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
  setup-loopback-demo.sh --undo
EOF
}

[ "$(id -u)" = "0" ] || die "must run as root"

MODE="${1:-}"
[ -n "$MODE" ] || { usage >&2; exit 1; }
shift || true

# aireos_read_state <key> — reads a key=value line out of $STATE_FILE, empty
# if the file or key is absent.
aireos_read_state() {
  [ -f "$STATE_FILE" ] || { printf ''; return 0; }
  grep -E "^$1=" "$STATE_FILE" 2>/dev/null | tail -n1 | cut -d= -f2- || true
}

# aireos_write_state <key> <value> — upserts a key=value line in $STATE_FILE.
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

case "$MODE" in
  generate)
    PARSED="$(aireos_loopback_parse_generate_args "$@")" || { usage >&2; exit 1; }
    ACCOUNT="${PARSED%%|*}"
    PORT="${PARSED##*|}"

    # What this script needs is a REACHABLE sshd, not a particular systemd
    # arrangement — so test the port, and fall back to the unit check only when
    # no TCP probe is possible.
    #
    # Checking `systemctl is-active ssh|sshd` alone is wrong on Ubuntu 24.04,
    # which is this product's own primary install target: it ships sshd
    # **socket-activated**, so `ssh.socket` listens on 0.0.0.0:22 while
    # `ssh.service` stays *inactive* until a connection actually arrives. The
    # unit-only check therefore reported "no sshd running" on a host where sshd
    # was running and accepting connections — found at v0.12.0's GATE-4 §human
    # walkthrough on a real installed stack, where it blocked the documented
    # quickstart (ONB-5) at its first step.
    if ! timeout 3 bash -c "cat < /dev/null > /dev/tcp/127.0.0.1/$PORT" 2>/dev/null; then
      systemctl is-active --quiet ssh 2>/dev/null \
        || systemctl is-active --quiet sshd 2>/dev/null \
        || systemctl is-active --quiet ssh.socket 2>/dev/null \
        || die "no sshd reachable on 127.0.0.1:$PORT (and neither the ssh/sshd service nor ssh.socket is active) — install and start one before running this script"
    fi
    id "$ACCOUNT" >/dev/null 2>&1 || die "no such account '$ACCOUNT' — create the dedicated demo account first"
    command -v ssh-keygen >/dev/null 2>&1 || die "ssh-keygen not found (openssh-client)"

    HOME_DIR="$(getent passwd "$ACCOUNT" | cut -d: -f6)"
    [ -n "$HOME_DIR" ] && [ -d "$HOME_DIR" ] || die "account '$ACCOUNT' has no usable home directory"
    SSH_DIR="$HOME_DIR/.ssh"
    AUTHKEYS="$SSH_DIR/authorized_keys"
    install -d -m 700 -o "$ACCOUNT" -g "$ACCOUNT" "$SSH_DIR" \
      || die "cannot create $SSH_DIR (is $HOME_DIR writable?)"
    touch "$AUTHKEYS" 2>/dev/null || die "cannot write $AUTHKEYS"
    chown "$ACCOUNT:$ACCOUNT" "$AUTHKEYS"; chmod 600 "$AUTHKEYS"

    HOST_KEY_LINE=""
    for f in /etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ecdsa_key.pub /etc/ssh/ssh_host_rsa_key.pub; do
      if [ -r "$f" ] && [ -z "$HOST_KEY_LINE" ]; then HOST_KEY_LINE="$(cat "$f")"; fi
    done
    [ -n "$HOST_KEY_LINE" ] || die "no readable sshd host public key found under /etc/ssh (checked ed25519/ecdsa/rsa) — cannot pin ssh_host_key"
    # FIXED (was a KNOWN LIMITATION found live-testing T-118b against a stock
    # multi-key sshd): services/session-broker/internal/sshpty/sshpty.go's
    # dialClient used to set HostKeyCallback: ssh.FixedHostKey(pinnedKey) without
    # constraining ssh.ClientConfig.HostKeyAlgorithms, so golang.org/x/crypto/ssh
    # negotiated whichever host key type IT preferred (empirically
    # ecdsa-sha2-nistp256 over ssh-ed25519), independent of which type this
    # script pinned — a multi-key sshd could make the governed connection fail
    # even with a correct pin. T-123 (already on dev) fixed dialClient to
    # constrain HostKeyAlgorithms to the pinned key's own type, so this no
    # longer needs a warning here regardless of how many host key types sshd
    # offers.

    log "generating a fresh ed25519 client keypair in $STATE_DIR"
    install -d -m 750 -o root -g "$SVC_USER" "$STATE_DIR"
    rm -f "$KEY_FILE" "$PUB_FILE"
    ssh-keygen -q -t ed25519 -N "" -C "$MARKER" -f "$KEY_FILE" \
      || die "ssh-keygen failed"
    chown root:"$SVC_USER" "$KEY_FILE" "$PUB_FILE"; chmod 640 "$KEY_FILE"; chmod 644 "$PUB_FILE"

    RAW_PUB="$(cat "$PUB_FILE")"
    ENTRY="$(aireos_loopback_authkey_line "$RAW_PUB" "$MARKER")" \
      || die "generated public key was not well-formed (unexpected ssh-keygen output)"

    log "installing the restricted authorized_keys entry for $ACCOUNT"
    CURRENT="$(cat "$AUTHKEYS" 2>/dev/null || true)"
    STRIPPED="$(aireos_loopback_strip_marker "$CURRENT" "$MARKER")"
    { [ -n "$STRIPPED" ] && printf '%s\n' "$STRIPPED"; printf '%s' "$ENTRY"; } > "$AUTHKEYS.new"
    install -m 600 -o "$ACCOUNT" -g "$ACCOUNT" "$AUTHKEYS.new" "$AUTHKEYS"
    rm -f "$AUTHKEYS.new"

    aireos_write_state ACCOUNT "$ACCOUNT"
    aireos_write_state PORT "$PORT"
    aireos_write_state HOST_KEY "$HOST_KEY_LINE"

    log "done — paste these into the cockpit's 'Create loopback demo target' action:"
    echo "  host:         127.0.0.1"
    echo "  port:         $PORT"
    echo "  username:     $ACCOUNT"
    echo "  ssh host key: $HOST_KEY_LINE"
    log "the private key never leaves this host; after the cockpit returns the new resource's id, run: $0 bind --resource-id <id>"
    ;;

  bind)
    RESOURCE_ID="$(aireos_loopback_parse_bind_args "$@")" || { usage >&2; exit 1; }
    [ -f "$KEY_FILE" ] || die "no generated key found in $STATE_DIR — run '$0 generate --account <name>' first"
    [ -x "$PREFIX/bin/connector-service" ] || die "connector-service CLI not found at $PREFIX/bin/connector-service"
    [ -f "$ETC/aireos.env" ] || die "$ETC/aireos.env not found — is this an installed host?"
    [ -f "$ETC/connector.env" ] || die "$ETC/connector.env not found — is this an installed host?"

    DB_URL="$(grep -E '^DATABASE_URL=' "$ETC/aireos.env" | tail -n1 | cut -d= -f2-)"
    RESOURCE_KEY="$(grep -E '^AIREOS_RESOURCE_SECRET_KEY=' "$ETC/connector.env" | tail -n1 | cut -d= -f2-)"
    [ -n "$DB_URL" ] || die "DATABASE_URL missing from $ETC/aireos.env"
    [ -n "$RESOURCE_KEY" ] || die "AIREOS_RESOURCE_SECRET_KEY missing from $ETC/connector.env"

    log "loading the private key into connector custody for resource $RESOURCE_ID"
    DATABASE_URL="$DB_URL" AIREOS_RESOURCE_SECRET_KEY="$RESOURCE_KEY" \
      "$PREFIX/bin/connector-service" credentials set-secret "$RESOURCE_ID" --key-file "$KEY_FILE"

    aireos_write_state RESOURCE_ID "$RESOURCE_ID"
    log "bound — the demo resource is ready to run a governed action against"
    ;;

  --undo)
    ACCOUNT="$(aireos_read_state ACCOUNT)"
    RESOURCE_ID="$(aireos_read_state RESOURCE_ID)"

    if [ -n "$ACCOUNT" ] && id "$ACCOUNT" >/dev/null 2>&1; then
      HOME_DIR="$(getent passwd "$ACCOUNT" | cut -d: -f6)"
      AUTHKEYS="$HOME_DIR/.ssh/authorized_keys"
      if [ -f "$AUTHKEYS" ]; then
        CURRENT="$(cat "$AUTHKEYS")"
        STRIPPED="$(aireos_loopback_strip_marker "$CURRENT" "$MARKER")"
        if [ -n "$STRIPPED" ]; then printf '%s\n' "$STRIPPED" > "$AUTHKEYS.new"; else : > "$AUTHKEYS.new"; fi
        install -m 600 -o "$ACCOUNT" -g "$ACCOUNT" "$AUTHKEYS.new" "$AUTHKEYS"
        rm -f "$AUTHKEYS.new"
        log "removed the authorized_keys entry for $ACCOUNT (if it was present)"
      fi
    else
      log "no known account on record (or it no longer exists) — nothing to remove from authorized_keys"
    fi

    if [ -n "$RESOURCE_ID" ]; then
      if [ -x "$PREFIX/bin/connector-service" ] && [ -f "$ETC/aireos.env" ] && [ -f "$ETC/connector.env" ]; then
        DB_URL="$(grep -E '^DATABASE_URL=' "$ETC/aireos.env" | tail -n1 | cut -d= -f2-)"
        RESOURCE_KEY="$(grep -E '^AIREOS_RESOURCE_SECRET_KEY=' "$ETC/connector.env" | tail -n1 | cut -d= -f2-)"
        if [ -n "$DB_URL" ] && [ -n "$RESOURCE_KEY" ]; then
          DATABASE_URL="$DB_URL" AIREOS_RESOURCE_SECRET_KEY="$RESOURCE_KEY" \
            "$PREFIX/bin/connector-service" credentials unset-secret "$RESOURCE_ID"
          log "removed the stored credential for resource $RESOURCE_ID (if it was present)"
        else
          log "connector env incomplete — skipping stored-credential removal"
        fi
      else
        log "connector-service CLI or env not found — skipping stored-credential removal"
      fi
    else
      log "no bound resource on record — nothing to remove from connector custody"
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
