#!/usr/bin/env bash
# install.sh — offline installer for the AI ReOS application tier (§OI-b, ADR-0051).
#
# Run as root on the TARGET host, from the unpacked bundle directory, AFTER
# a PostgreSQL 16 + pgcrypto with a cockpit database + role exists. The normal
# entry point is setup.sh beside this file (T-174, ADR-0077), which installs the
# PostgreSQL companion, generates the DB password and then runs this script; this
# script alone is the bring-your-own-PostgreSQL / unattended path (it needs a URL).
#
# It deploys the binaries + SPA + config, mints the cluster keys, provisions the
# single bootstrap operator, validates the DB + runs every service's migrations,
# and enables the systemd units.
#
# Provisioning (ADR-0051 OQ-4): interactive prompts by default; set the env vars
# below for an unattended install (the automated WD-1 demo uses that path).
# Idempotent (OI-2): re-running preserves existing cluster keys and re-applies
# migrations as no-ops; the operator is env-defined (no duplicate rows). A re-run
# RESTARTS the seven units so the env it just wrote (site address, profile,
# pairing) is what is actually serving — `enable --now` alone would not.
#
# Env overrides (any unset value is prompted unless noted):
#   AIREOS_DB_URL                       postgres connection string (required)
#   AIREOS_BOOTSTRAP_OPERATOR_USER      operator username
#   AIREOS_BOOTSTRAP_OPERATOR_PASSWORD  operator password (argon2id-hashed here)
#                                        The operator, in this order (ADR-0078 §D4): any of the three above
#                                        set -> used; else aireos.env already holds one -> kept, nothing asked
#                                        (a re-run); else AIREOS_OPERATOR_SETUP=browser (set by setup.sh) ->
#                                        none written, the first operator is made in the browser with a setup
#                                        code; else (a bare install.sh) the prompts.
#   AIREOS_SITE_ADDRESS                 Caddy site address (default: localhost)
#   AIREOS_DEPLOYMENT_PROFILE           lan-only | vpn-first | dmz-first (required,
#                                        never defaulted — ADR-0068 §D1/OI-21). Persisted
#                                        in aireos.env. vpn-first/dmz-first REFUSE a
#                                        loopback-only AIREOS_SITE_ADDRESS (OI-22): Caddy
#                                        is the product's only externally-bound process.
#   AIREOS_PAIRING_ENABLED              1 enables QR device pairing; unset/anything else
#                                        is off (ADR-0068 §D10/SEC-41 — the shipped
#                                        default on a fresh install). Never prompted: a
#                                        phone-pairing question at every air-gapped install
#                                        would be worse than a safe default. Resolution is
#                                        env > persisted (from a prior run's aireos.env) >
#                                        "0" — so a deliberate enable SURVIVES a re-run
#                                        instead of silently flipping back off (see
#                                        docs/devops/offline-install-v3.md).
#   AIREOS_SKIP_SYSTEMCTL=1             deploy + provision but don't enable units
#   AIREOS_HEALTH_TIMEOUT / _SETTLE     seconds to wait for the units to be active (60) and to stay
#                                        active unchanged (5) before DONE is printed (INS-10)
set -euo pipefail

PREFIX=/opt/aireos
ETC=/etc/aireos
STATE=/var/lib/aireos
ENV_FILE="$ETC/aireos.env"
SVC_USER=aireos
BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { printf '\033[1;34m[install]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[install] WARNING:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[install] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# --- deployment-profile + pairing-enablement helpers (ADR-0068 §D1/§D10) ----
# Kept free of every other section's state so install_profile_test.sh can
# source this file and exercise them in isolation (see the sourceable guard
# just below).

# aireos_site_is_loopback <token> — true (0) iff the single host/URL token
# resolves to a loopback address. Normalization order matters: strip scheme,
# strip path, detect a bare port (binds every interface — NOT loopback),
# unwrap a bracketed IPv6 literal, else strip a single :port (preserving a
# bare IPv6 literal like ::1, which itself contains colons).
aireos_site_is_loopback() {
  local token="$1" host colons
  token="$(printf '%s' "$token" | tr '[:upper:]' '[:lower:]')"
  token="${token#http://}"
  token="${token#https://}"
  token="${token%%/*}"
  # A bare port (":443": colon then digits only) binds every interface on the
  # host — not loopback. Checked as ^:[0-9]+$ rather than a plain leading-colon
  # test so a bare IPv6 literal like "::1" (also colon-led) falls through to
  # the host-extraction rules below instead of being misread as a port.
  [[ "$token" =~ ^:[0-9]+$ ]] && return 1
  if [[ "$token" == \[* ]]; then
    host="${token#\[}"
    host="${host%%]*}"
  else
    colons="$(printf '%s' "$token" | tr -cd ':' | wc -c)"
    if [ "$colons" -eq 1 ]; then
      host="${token%%:*}"
    else
      host="$token"   # 0 or 2+ colons: whole token is the host (preserves bare ::1)
    fi
  fi
  case "$host" in
    ""|localhost|localhost.localdomain|*.localhost|::1|0:0:0:0:0:0:0:1) return 0 ;;
  esac
  [[ "$host" =~ ^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]] && return 0
  return 1   # includes 0.0.0.0 / :: — unspecified, deliberately NOT loopback
}

# aireos_site_all_loopback <value> — true iff every whitespace-separated token
# in <value> is loopback (an empty/whitespace-only value has no tokens, so the
# loop never runs and this returns true — "no routable path configured"). One
# routable token is enough to pass (e.g. "localhost cockpit.corp" — the exact
# pattern run-wd1.sh already writes for AIREOS_SITE_ADDRESS).
aireos_site_all_loopback() {
  local value="$1" tok
  local -; set -f   # scope shell options locally, then disable pathname expansion (a "*" in $value must not glob against the cwd)
  for tok in $value; do
    aireos_site_is_loopback "$tok" || return 1
  done
  return 0
}

# aireos_persisted_value <var-name> <env-file> — echoes <var-name>'s line
# value from <env-file>, or nothing if the file is absent or carries no such
# line. No side effects; always "succeeds" so it is safe to capture via
# command substitution under this file's own `set -e`. Generalized from a
# profile-only aireos_persisted_profile (T-114a Commander-review addendum) so
# AIREOS_PAIRING_ENABLED's resolution below reuses this same tested machinery
# instead of a parallel implementation — both are "does a prior run's
# aireos.env already say something" lookups, differing only in which key.
aireos_persisted_value() {
  local var_name="$1" env_file="$2"
  if [ -f "$env_file" ]; then
    sed -n "s/^${var_name}=//p" "$env_file" | head -n1
  fi
  return 0
}

# aireos_resolve_profile <env-value> <persisted> <answer> — pure precedence,
# no I/O, no prompting (prompting is genuinely interactive and stays in the
# installer body): env wins; else a non-empty prompt answer; else persisted;
# else empty (aireos_assert_profile then refuses on empty — never a silent
# default).
aireos_resolve_profile() {
  local env_value="$1" persisted="$2" answer="$3"
  if [ -n "$env_value" ]; then
    printf '%s' "$env_value"
  elif [ -n "$answer" ]; then
    printf '%s' "$answer"
  else
    printf '%s' "$persisted"
  fi
}

# aireos_resolve_pairing_enabled <env-value> <persisted> — env wins; else a
# prior run's persisted value; else "0", the SEC-41 shipped default. No
# `<answer>` leg: unlike the profile, this is deliberately never prompted
# (ADR-0068 §D10 — prompting would push a phone-pairing question at every
# air-gapped install), so the precedence collapses to two real sources plus a
# safe literal fallback instead of aireos_resolve_profile's four-way one.
# Fixes the T-114a defect the Commander review caught: a hardcoded `0` in the
# aireos.env heredoc silently flipped a deliberately-enabled pairing session
# back off on every re-run (ADR-0051's idempotence guarantee / OI-31 both
# require a re-run to preserve existing configuration, not just existing rows).
aireos_resolve_pairing_enabled() {
  local env_value="$1" persisted="$2"
  if [ -n "$env_value" ]; then
    printf '%s' "$env_value"
  elif [ -n "$persisted" ]; then
    printf '%s' "$persisted"
  else
    printf '%s' "0"
  fi
}

# aireos_resolve_operator_mode <env-user> <env-hash> <env-password> <persisted-user> <persisted-hash> <setup-signal>
# — which of the four ways the operator comes about (ADR-0078 §D4, T-175c); pure, no I/O. Prints one of:
#   env        any AIREOS_BOOTSTRAP_OPERATOR_* is set: as always; anything it leaves out is prompted for
#   persisted  aireos.env already carries the user AND the hash: keep them, ask nothing (a re-run / upgrade)
#   browser    <setup-signal> is "browser" (setup.sh sets it): write NO operator, api starts in setup mode
#   prompt     a bare install.sh: ask at the terminal, as before (ADR-0078 alternative 1)
# The environment wins over what is persisted (that is how a lost password is recovered); a half-persisted pair
# is not an operator.
aireos_resolve_operator_mode() {
  local env_user="$1" env_hash="$2" env_pw="$3" p_user="$4" p_hash="$5" signal="$6"
  if [ -n "$env_user$env_hash$env_pw" ]; then printf env
  elif [ -n "$p_user" ] && [ -n "$p_hash" ]; then printf persisted
  elif [ "$signal" = browser ]; then printf browser
  else printf prompt; fi
}

# aireos_assert_profile <profile> <site> — die() (never silently default) if
# the profile is missing/unrecognised, or if vpn-first/dmz-first is paired
# with a site address that resolves to loopback-only.
aireos_assert_profile() {
  local profile="$1" site="$2"
  case "$profile" in
    lan-only|vpn-first|dmz-first) : ;;
    "") die "AIREOS_DEPLOYMENT_PROFILE is required and is never defaulted — set it to one of: lan-only, vpn-first, dmz-first" ;;
    *) die "invalid AIREOS_DEPLOYMENT_PROFILE '$profile' — must be exactly one of: lan-only, vpn-first, dmz-first" ;;
  esac
  [ "$profile" = "lan-only" ] && return 0
  if aireos_site_all_loopback "$site"; then
    die "profile '$profile' requires a routable AIREOS_SITE_ADDRESS, but got '${site:-<unset>}' — Caddy is the product's only externally-bound process, and a loopback edge reaches nothing from outside this host; either set AIREOS_SITE_ADDRESS to a routable hostname/IP, or install with AIREOS_DEPLOYMENT_PROFILE=lan-only instead"
  fi
  return 0
}

# --- database URL shape + systemd (re)start helpers (T-165; PUB-6 P7/P9) -----

# aireos_valid_db_url <url> — true (0) iff <url> is a postgres:// or
# postgresql:// URL with a non-empty host. PURE shape check — it never connects
# and never echoes the value (it may carry a password). The authority ends at the
# first '/', '?' or '#' and the userinfo ends at the LAST '@', exactly as Go's
# net/url splits it, so a password containing a raw '@' stays valid (the CEO's
# real password had one and worked end to end). A bare word like "cockpit" (P7)
# or a non-postgres scheme is refused; so is an empty host ("postgres:///db").
aireos_valid_db_url() {
  local url="$1" rest authority hostport host port
  case "$url" in
    postgres://*) rest="${url#postgres://}" ;;
    postgresql://*) rest="${url#postgresql://}" ;;
    *) return 1 ;;
  esac
  [[ "$url" =~ [[:space:]] ]] && return 1
  authority="${rest%%[/?#]*}"
  hostport="${authority##*@}"
  if [[ "$hostport" == \[* ]]; then
    host="${hostport#\[}"; host="${host%%]*}"
    port="${hostport#*]}"; port="${port#:}"
  else
    host="${hostport%%:*}"
    port=""; [[ "$hostport" == *:* ]] && port="${hostport#*:}"
  fi
  [ -n "$host" ] || return 1
  [[ "$port" =~ ^[0-9]*$ ]] || return 1
  return 0
}

# aireos_db_url_hint — the plain-English refusal for a value that is not a
# usable database URL. Deliberately does NOT take or print the value.
aireos_db_url_hint() {
  printf '%s' "That is not a PostgreSQL connection URL. It must start with postgres:// (or postgresql://) and name a host, in this shape: postgres://<user>:<password>@<host>:5432/<database>?sslmode=disable"
}

# The seven units install.sh owns, in start order.
AIREOS_UNITS=(aireos-api.service aireos-audit.service aireos-broker.service
  aireos-connector.service aireos-policy.service aireos-mcp-gateway.service
  aireos-caddy.service)

# aireos_enable_and_restart_units — daemon-reload, enable, then RESTART all
# seven units. `enable --now` alone is a no-op on an already-active unit, and
# every unit reads aireos.env (Caddy: AIREOS_SITE_ADDRESS, Caddyfile:17) only at
# process start — so on a re-run that changed the address / profile / pairing
# flag the old values kept serving while the DONE line announced the new ones
# (PUB-6 P9). restart also starts a stopped unit. Returns non-zero on the first
# failing systemctl call; the caller must die, never print DONE.
aireos_enable_and_restart_units() {
  systemctl daemon-reload || return 1
  systemctl enable "${AIREOS_UNITS[@]}" || return 1
  systemctl restart "${AIREOS_UNITS[@]}" || return 1
}

# --- INS-9 site-address sanity + INS-10 post-start health (T-174) -------------

# aireos_site_host <token> — the host of one site-address token (scheme, path, :port stripped; IPv6 brackets
# removed). Fails for a bare ":port", which binds every interface and has no host to compare.
aireos_site_host() {
  local t host
  t="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"; t="${t#http://}"; t="${t#https://}"; t="${t%%/*}"
  if [[ "$t" == \[* ]]; then host="${t#\[}"; host="${host%%]*}"
  elif [ "$(printf '%s' "$t" | tr -cd ':' | wc -c)" -eq 1 ]; then host="${t%%:*}"
  else host="$t"; fi
  [ -n "$host" ] || return 1
  printf '%s' "$host"
}

# aireos_site_port <token> <default> — the explicit :port of a site-address token, else <default>.
aireos_site_port() {
  local t="${1#http://}"; t="${t#https://}"; t="${t%%/*}"
  if [[ "$t" =~ ^(\[[^]]*\]|[^:]*):([0-9]+)$ ]]; then printf '%s' "${BASH_REMATCH[2]}"; else printf '%s' "$2"; fi
}

# aireos_local_addrs — every address configured on this host, one per line. `ip` where present; else IPv4 from
# /proc (Rocky 9 minimal may lack iproute). Prints nothing when neither works (callers then stay silent).
aireos_local_addrs() {
  if command -v ip >/dev/null 2>&1; then
    ip -o addr 2>/dev/null | awk '{sub(/\/.*/,"",$4); print $4}'
  elif [ -r /proc/net/fib_trie ]; then
    awk '/^Local:/{l=1} /^Main:/{l=0} l&&/\|--/{a=$2} l&&/\/32 host LOCAL/{print a}' /proc/net/fib_trie
  fi
  return 0
}

# aireos_warn_site_address <value> — INS-9, a WARNING only (never blocks): for each token of AIREOS_SITE_ADDRESS
# that neither is nor resolves to an address on this host, say so in one sentence. Loopback is always local.
aireos_warn_site_address() {
  local tok host ips ip mine found
  local -; set -f
  mine="$(aireos_local_addrs)"
  for tok in $1; do
    if aireos_site_is_loopback "$tok"; then continue; fi
    host="$(aireos_site_host "$tok")" || continue
    [[ "$host" == *\** ]] && continue
    [[ "$host" == *:* ]] && ! command -v ip >/dev/null 2>&1 && continue   # cannot list IPv6 addresses here: say nothing
    if [[ "$host" =~ ^[0-9.]+$ || "$host" == *:* ]]; then ips="$host"
    else ips="$(getent ahosts "$host" 2>/dev/null | awk '{print $1}' | sort -u || true)"; fi
    if [ -z "$ips" ]; then
      warn "AIREOS_SITE_ADDRESS '$tok' does not resolve to any address, so browsers will not reach this host by that name; fix the DNS name or set AIREOS_SITE_ADDRESS to this host's name or IP. Continuing."
      continue
    fi
    found=0
    for ip in $ips; do
      ip="${ip#::ffff:}"
      case "$ip" in 127.*|::1) found=1 ;; esac
      if [ -n "$mine" ] && grep -qxF -- "$ip" <<<"$mine"; then found=1; fi
    done
    if [ "$found" = 0 ] && [ -n "$mine" ]; then
      warn "AIREOS_SITE_ADDRESS '$tok' (${ips//$'\n'/ }) is not an address of this host, so browsers that use it will not reach Caddy here; set AIREOS_SITE_ADDRESS to this host's own name or IP. Continuing."
    fi
  done
  return 0
}

# aireos_unit_fail <unit> <state> — INS-10 failure: name the unit and where to look; never returns.
aireos_unit_fail() {
  die "$1 is not staying active (state: ${2:-unknown}), so the install is NOT complete. Look at: journalctl -u ${1%.service} -n 50 --no-pager   (and: systemctl status $1)"
}

# aireos_wait_units_stable <unit...> — INS-10 part 1. Within AIREOS_HEALTH_TIMEOUT (60 s) every unit must be
# `active` AND stay unchanged (same state, restart count and main PID) for AIREOS_HEALTH_SETTLE (5) consecutive
# one-second polls: a crash-loop that flickers `active` between restarts changes a PID or a counter and resets
# the streak. A unit in `failed` (e.g. start limit hit) is final: no waiting.
aireos_wait_units_stable() {
  local deadline=$((SECONDS + ${AIREOS_HEALTH_TIMEOUT:-60})) settle="${AIREOS_HEALTH_SETTLE:-5}" streak=0 u st cur bad changed
  local -A last=()
  while :; do
    bad=""; changed=""
    for u in "$@"; do
      st="$(systemctl is-active "$u" 2>/dev/null || true)"
      [ "$st" != failed ] || aireos_unit_fail "$u" failed
      if [ "$st" != active ]; then bad="${bad:-$u|$st}"; continue; fi
      cur="$(systemctl show -p ActiveState,NRestarts,MainPID --value "$u" 2>/dev/null | tr '\n' ' ')"
      if [ "${last[$u]:-$cur}" != "$cur" ]; then changed="${changed:-$u}"; fi
      last[$u]="$cur"
    done
    if [ -n "$bad" ] || [ -n "$changed" ]; then streak=0; else streak=$((streak + 1)); fi
    [ "$streak" -lt "$settle" ] || return 0
    if [ "$SECONDS" -ge "$deadline" ]; then
      if [ -n "$bad" ]; then aireos_unit_fail "${bad%%|*}" "${bad#*|}"; fi
      aireos_unit_fail "${changed:-$1}" "restarting"
    fi
    sleep 1
  done
}

# aireos_wait_https <site-address-value> — INS-10 part 2: every site-address token must get a 2xx/3xx answer
# from Caddy over HTTPS (curl -k: the local CA is not trusted yet). A hostname is pinned to 127.0.0.1 with
# --resolve (SNI stays the real name), an IPv4 literal is routed there with --connect-to; no DNS is needed.
aireos_wait_https() {
  local deadline=$((SECONDS + ${AIREOS_HEALTH_TIMEOUT:-60})) tok host port scheme url code; local -a args
  if ! command -v curl >/dev/null 2>&1; then
    warn "curl is not installed, so Caddy's HTTPS answer was not checked (the units themselves are up)."; return 0
  fi
  local -; set -f
  for tok in $1; do
    scheme=https; [[ "$tok" == http://* ]] && scheme=http
    port="$(aireos_site_port "$tok" "$([ "$scheme" = http ] && echo 80 || echo 443)")"
    host="$(aireos_site_host "$tok")" || host=localhost
    args=(-ks -o /dev/null -w '%{http_code}' --max-time 5); url="$scheme://$host:$port/"
    if [[ "$host" == *:* ]]; then url="$scheme://[$host]:$port/"
    elif [[ "$host" =~ ^[0-9.]+$ ]]; then args+=(--connect-to "$host:$port:127.0.0.1:$port")
    else args+=(--resolve "$host:$port:127.0.0.1"); fi
    until code="$(curl "${args[@]}" "$url" 2>/dev/null || true)"; [[ "$code" =~ ^[23] ]]; do
      [ "$SECONDS" -lt "$deadline" ] || die "Caddy did not answer HTTPS at $url (last status: ${code:-none}), so the install is NOT complete. Look at: journalctl -u aireos-caddy -n 50 --no-pager   (and: systemctl status aireos-caddy)"
      sleep 1
    done
  done
}

# Sourceable guard (install_profile_test.sh sources this file to reach the
# helpers above without running the installer body). NOTE: `set -euo pipefail`
# above already applies to the sourcing shell by the time this guard runs —
# bash `set` options are shell-global, not scoped to the sourced file — so a
# test that sources install.sh inherits -e/-u/pipefail too.
if [ "${BASH_SOURCE[0]}" != "${0}" ]; then return 0; fi

[ "$(id -u)" = "0" ] || die "must run as root"
[ -d "$BUNDLE_DIR/bin" ] && [ -d "$BUNDLE_DIR/web" ] && [ -d "$BUNDLE_DIR/config" ] \
  || die "run this from the unpacked bundle directory (expected bin/ web/ config/)"

# --- 1a. deployment profile (ADR-0068 §D1; OI-21..23) ------------------------
# Env beats persisted beats prompt. A refusal here must create no user, no
# directories, and deploy nothing — hence this runs before every other step.
# Resolution is the two testable helpers above (aireos_persisted_value,
# aireos_resolve_profile); only the interactive `read` stays here.
PERSISTED_PROFILE="$(aireos_persisted_value "AIREOS_DEPLOYMENT_PROFILE" "$ENV_FILE")"
ANSWER=""
if [ -z "${AIREOS_DEPLOYMENT_PROFILE:-}" ]; then
  if [ -n "$PERSISTED_PROFILE" ]; then
    read -e -r -p "Deployment profile [lan-only|vpn-first|dmz-first] (default: $PERSISTED_PROFILE): " ANSWER || true
  else
    read -e -r -p "Deployment profile [lan-only|vpn-first|dmz-first]: " ANSWER || true
  fi
fi
DEPLOYMENT_PROFILE="$(aireos_resolve_profile "${AIREOS_DEPLOYMENT_PROFILE:-}" "$PERSISTED_PROFILE" "$ANSWER")"
# Site address: env beats persisted beats localhost — like the profile. Without the persisted step a plain re-run
# (an upgrade: `sudo ./setup.sh` again) silently reset a LAN install to localhost and cut every other machine off.
SITE_ADDRESS="${AIREOS_SITE_ADDRESS:-$(aireos_persisted_value "AIREOS_SITE_ADDRESS" "$ENV_FILE")}"
aireos_assert_profile "$DEPLOYMENT_PROFILE" "$SITE_ADDRESS"
log "deployment profile: $DEPLOYMENT_PROFILE"
SITE_ADDRESS="${SITE_ADDRESS:-localhost}"
aireos_warn_site_address "$SITE_ADDRESS"   # INS-9: a warning only

# --- 1a'. unattended database URL shape (PUB-6 P7) ---------------------------
# An env-supplied AIREOS_DB_URL cannot be re-asked, so refuse it now — before any
# user, directory, key or env file exists. (The interactive prompt is step 5.)
if [ -n "${AIREOS_DB_URL:-}" ] && ! aireos_valid_db_url "$AIREOS_DB_URL"; then
  die "AIREOS_DB_URL is set but $(aireos_db_url_hint)"
fi

# --- 1b. pairing enablement (ADR-0068 §D10; T-114a Commander-review addendum)
# Env beats persisted beats "0" — no prompt (see aireos_resolve_pairing_enabled's
# comment). This is the SAME env>persisted precedence OI-21 already gives the
# deployment profile above, applied to a second variable — an ambient
# AIREOS_PAIRING_ENABLED in someone's shell profile has no MORE power here than
# an ambient AIREOS_DEPLOYMENT_PROFILE already has for the profile; a deliberate
# enable (env this run, or persisted from a prior one) survives a re-run, and a
# fresh install with neither set stays off (SEC-41's shipped default).
PERSISTED_PAIRING="$(aireos_persisted_value "AIREOS_PAIRING_ENABLED" "$ENV_FILE")"
PAIRING_ENABLED="$(aireos_resolve_pairing_enabled "${AIREOS_PAIRING_ENABLED:-}" "$PERSISTED_PAIRING")"
log "pairing enabled: $PAIRING_ENABLED"

# --- 1. service user + state dirs -------------------------------------------
if ! id "$SVC_USER" >/dev/null 2>&1; then
  log "create system user $SVC_USER"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null \
    || useradd -r -s /sbin/nologin "$SVC_USER"   # Ubuntu vs RHEL nologin path
fi
install -d -m 755 "$PREFIX/bin" "$PREFIX/web" "$ETC"
# $STATE is 0751, not 0750: the PostgreSQL companion keeps its data dir at $STATE/pgsql (ADR-0077 D4, 0700 aireos-pg),
# and aireos-pg must TRAVERSE here to reach it. A 0750 parent locked it out at the next checkpoint (PANIC ... pg_control:
# Permission denied; found by T-172b WD-1). Others can traverse but not list; every child below stays 0750.
install -d -m 751 -o "$SVC_USER" -g "$SVC_USER" "$STATE"
install -d -m 750 -o "$SVC_USER" -g "$SVC_USER" "$STATE/sessions" "$STATE/caddy" "$STATE/audit"

# --- 2. deploy binaries + SPA + config --------------------------------------
log "deploy binaries, SPA, config"
install -m 755 "$BUNDLE_DIR"/bin/* "$PREFIX/bin/"
rm -rf "${PREFIX:?}/web"/*
cp -a "$BUNDLE_DIR"/web/. "$PREFIX/web/"
install -m 644 "$BUNDLE_DIR/config/Caddyfile" "$ETC/Caddyfile"
install -m 644 "$BUNDLE_DIR"/config/*.service /etc/systemd/system/

# --- 3. cluster keys (idempotent: reuse existing) ---------------------------
# The resource key lives in connector.env since v0.2.0 (OI-6/ADR-0053: only
# connector-service may read it). A v0.1.0 aireos.env still carrying the key is
# the documented upgrade source — read it as the fallback, then relocate.
CONNECTOR_ENV="$ETC/connector.env"
if [ -f "$ENV_FILE" ]; then
  log "existing $ENV_FILE found — preserving cluster keys (idempotent re-run)"
  # Only source the key lines (no shell metachars); never the hash/url lines.
  eval "$(grep -E '^(AIREOS_PASETO_KEY|AIREOS_RESOURCE_SECRET_KEY)=' "$ENV_FILE" | sed 's/^/export /')"
fi
if [ -f "$CONNECTOR_ENV" ]; then
  # v0.2.0 layout — the connector-owned key wins over any legacy aireos.env line.
  eval "$(grep -E '^AIREOS_RESOURCE_SECRET_KEY=' "$CONNECTOR_ENV" | sed 's/^/export /')"
fi
PASETO_KEY="${AIREOS_PASETO_KEY:-$("$PREFIX/bin/authctl" genkey)}"
RESOURCE_KEY="${AIREOS_RESOURCE_SECRET_KEY:-$("$PREFIX/bin/connector-service" credentials genkey)}"
[ -n "$PASETO_KEY" ] && [ -n "$RESOURCE_KEY" ] || die "key generation failed"
[ "$PASETO_KEY" != "$RESOURCE_KEY" ] || die "key separation failed (PASETO == resource; ADR-0048 §7)"

# --- 3b. v0.4.0 asymmetric agent-plane keys + F3 command key (ADR-0060/OI-11) ---
# The agent-facing mcp-gateway holds NO symmetric cluster key (SEC-20): it verifies
# inbound with api's PUBLIC key and signs outbound with its OWN key; api signs
# agent-tool tokens with the agent-plane private key. Secrets live in per-holder env
# files (api.env, gateway.env); the gateway PUBLIC key — verified by policy/broker/
# audit — rides shared aireos.env. Idempotent: reuse existing pairs from those files;
# a v0.3.0 install (gateway on aireos.env) is upgraded here — the new bundle's
# gateway unit reads gateway.env and never the cluster key.
API_ENV="$ETC/api.env"
GATEWAY_ENV="$ETC/gateway.env"
if [ -f "$API_ENV" ]; then
  eval "$(grep -E '^AIREOS_API_AGENT_SIGN_KEY=' "$API_ENV" | sed 's/^/export /')"
fi
if [ -f "$GATEWAY_ENV" ]; then
  eval "$(grep -E '^(AIREOS_GATEWAY_SIGN_KEY|AIREOS_API_AGENT_PUBLIC_KEY|AIREOS_GATEWAY_COMMAND_KEY)=' "$GATEWAY_ENV" | sed 's/^/export /')"
fi
if [ -f "$ENV_FILE" ]; then
  eval "$(grep -E '^AIREOS_GATEWAY_PUBLIC_KEY=' "$ENV_FILE" | sed 's/^/export /')"
fi
# api agent-plane keypair (secret → api.env, public → gateway.env). Regenerate the
# whole pair if either half is missing (a public is useless without its secret).
API_AGENT_SK="${AIREOS_API_AGENT_SIGN_KEY:-}"
API_AGENT_PK="${AIREOS_API_AGENT_PUBLIC_KEY:-}"
if [ -z "$API_AGENT_SK" ] || [ -z "$API_AGENT_PK" ]; then
  KP="$("$PREFIX/bin/authctl" genkeypair)"; API_AGENT_SK="${KP%% *}"; API_AGENT_PK="${KP##* }"
fi
# gateway keypair (secret → gateway.env, public → aireos.env).
GW_SK="${AIREOS_GATEWAY_SIGN_KEY:-}"
GW_PK="${AIREOS_GATEWAY_PUBLIC_KEY:-}"
if [ -z "$GW_SK" ] || [ -z "$GW_PK" ]; then
  KP="$("$PREFIX/bin/authctl" genkeypair)"; GW_SK="${KP%% *}"; GW_PK="${KP##* }"
fi
# F3 command-at-rest key (gateway.env).
GW_CMD_KEY="${AIREOS_GATEWAY_COMMAND_KEY:-$("$PREFIX/bin/authctl" genaeskey)}"
[ -n "$API_AGENT_SK" ] && [ -n "$API_AGENT_PK" ] && [ -n "$GW_SK" ] && [ -n "$GW_PK" ] && [ -n "$GW_CMD_KEY" ] || die "v0.4.0 key generation failed"
# Authority separation (ADR-0060): the secrets must differ from the cluster key and
# from each other.
{ [ "$GW_CMD_KEY" != "$PASETO_KEY" ] && [ "$GW_CMD_KEY" != "$GW_SK" ]; } || die "command key must differ from the cluster / gateway sign keys (authority separation)"
[ "$API_AGENT_SK" != "$GW_SK" ] || die "api-agent and gateway sign keys must differ (per-workload keys, ADR-0060)"

# --- 3c. audit chain-head signing keypair (ADR-0069 D5/AUD-35; OI-41) -------
# A NEW Ed25519 keypair per install, same genkeypair idiom as 3b above. THE
# SECRET MUST NOT GO IN THE SHARED aireos.env: every service reads that file,
# so putting the signing secret there would let an RCE on ANY service forge
# chain heads — silently invalidating ADR-0069's stated residual (only
# audit-service RCE can forge). It lives ALONE in audit.env, the same
# least-privilege pattern api.env/gateway.env/connector.env already establish.
# The public half is not secret and rides shared aireos.env so operators and
# third-party verifiers can read it.
#
# Idempotent (OI-41 — an evidence-integrity property here, not hygiene): reuse
# an existing key from audit.env exactly like 3b reuses gateway.env/api.env.
# REGENERATING ON RE-INSTALL SILENTLY ORPHANS EVERY HISTORICAL CHAIN HEAD —
# the chain still verifies structurally while every signature becomes
# uncheckable. This is the single most likely implementation error in the
# release (ADR-0069 D5).
AUDIT_ENV="$ETC/audit.env"
if [ -f "$AUDIT_ENV" ]; then
  eval "$(grep -E '^AIREOS_AUDIT_CHAIN_SIGN_KEY=' "$AUDIT_ENV" | sed 's/^/export /')"
fi
if [ -f "$ENV_FILE" ]; then
  eval "$(grep -E '^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=' "$ENV_FILE" | sed 's/^/export /')"
fi
AUDIT_SK="${AIREOS_AUDIT_CHAIN_SIGN_KEY:-}"
AUDIT_PK="${AIREOS_AUDIT_CHAIN_PUBLIC_KEY:-}"
if [ -z "$AUDIT_SK" ]; then
  # No secret on disk at all: this is a genuinely fresh key.
  KP="$("$PREFIX/bin/authctl" genkeypair)"; AUDIT_SK="${KP%% *}"; AUDIT_PK="${KP##* }"
elif [ -z "$AUDIT_PK" ]; then
  # Secret present, public half missing — e.g. a prior run wrote audit.env
  # but was interrupted (or failed, see step 7) before aireos.env's rewrite.
  # DERIVE, never regenerate: an Ed25519 secret key is seed(32)||public(32),
  # so the trailing 64 hex chars of the 128-hex secret ARE the public key
  # (proven in T-124a's TestAuthctlKeyFormatCompatibility). Regenerating on
  # this partial state would silently orphan every historical chain-head
  # signature — the exact OI-41 failure this ticket exists to prevent — and
  # since the public key is always recoverable from the secret, there is
  # never a reason to.
  AUDIT_PK="${AUDIT_SK: -64}"
  log "re-derived the audit chain-head public key from the existing secret (aireos.env was missing it — OI-41 partial-state recovery)"
fi
[ -n "$AUDIT_SK" ] && [ -n "$AUDIT_PK" ] || die "audit chain-head key generation failed"
[ "$AUDIT_SK" != "$GW_SK" ] && [ "$AUDIT_SK" != "$API_AGENT_SK" ] || die "audit chain-head sign key must differ from the other per-workload keys (ADR-0060 authority separation)"

# --- 4. operator credentials (env > persisted > browser setup > prompt; OQ-4, ADR-0078 §D4) ----------------
OP_USER="${AIREOS_BOOTSTRAP_OPERATOR_USER:-}"
OP_HASH="${AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH:-}"
P_OP_USER="$(aireos_persisted_value AIREOS_BOOTSTRAP_OPERATOR_USER "$ENV_FILE")"
P_OP_HASH="$(aireos_persisted_value AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH "$ENV_FILE")"
OP_MODE="$(aireos_resolve_operator_mode "$OP_USER" "$OP_HASH" "${AIREOS_BOOTSTRAP_OPERATOR_PASSWORD:-}" "$P_OP_USER" "$P_OP_HASH" "${AIREOS_OPERATOR_SETUP:-}")"
case "$OP_MODE" in
  persisted)   # a re-run keeps the operator the last run wrote (like the site address): no prompt, nothing to type
    OP_USER="$P_OP_USER"; OP_HASH="$P_OP_HASH"
    log "operator: keeping the account already in $ENV_FILE" ;;
  browser)     # setup.sh: no operator lines at all, so api starts in setup mode and the first operator is made in the browser
    OP_USER=""; OP_HASH=""
    log "operator: none yet; the first one is created in the browser with a setup code (ADR-0078)" ;;
  *)
    if [ -z "$OP_HASH" ]; then
      [ -n "$OP_USER" ] || read -e -r -p "Operator username: " OP_USER
      OP_PW="${AIREOS_BOOTSTRAP_OPERATOR_PASSWORD:-}"
      if [ -z "$OP_PW" ]; then read -r -s -p "Operator password: " OP_PW; echo; fi
      [ -n "$OP_USER" ] && [ -n "$OP_PW" ] || die "operator username + password are required"
      OP_HASH="$("$PREFIX/bin/authctl" hash "$OP_PW")"
    fi ;;
esac

# --- 5. DB connection string ------------------------------------------------
DB_URL="${AIREOS_DB_URL:-}"   # shape already verified in step 1a' when env-supplied
while [ -z "$DB_URL" ]; do
  read -e -r -p "Postgres URL (postgres://<user>:<password>@<host>:5432/<database>?sslmode=disable): " DB_URL \
    || die "postgres connection URL required — set AIREOS_DB_URL, or run interactively. Install postgres 16 + pgcrypto first (see the offline postgres guide)"
  if ! aireos_valid_db_url "$DB_URL"; then
    # No terminal to re-ask on (piped stdin): fail now, before any env file is written.
    [ -t 0 ] || die "$(aireos_db_url_hint)"
    printf '\033[1;31m[install]\033[0m %s\n' "$(aireos_db_url_hint)" >&2
    DB_URL=""
  fi
done

# --- 6. write the env files (0600, owned by the service user) ---------------
# aireos.env = shared config for every unit. The resource key is NOT here: it
# lives in connector.env, referenced only by aireos-connector.service (SEC-13 —
# a v0.1.0 aireos.env carrying the key is rewritten without it on upgrade). The
# audit chain-head SECRET is likewise not here (see 3c) — only its PUBLIC key,
# which is safe to share, plus AIREOS_AUDIT_ANCHOR_PATH (ADR-0069 D4). Nothing
# consumes AIREOS_AUDIT_ANCHOR_PATH yet — its writer is T-125b — but it is
# provisioned here so install.sh is touched exactly once for this feature.
log "write $ENV_FILE"
( umask 077; cat > "$ENV_FILE" <<EOF
DATABASE_URL=$DB_URL
AIREOS_PASETO_KEY=$PASETO_KEY
AIREOS_GATEWAY_PUBLIC_KEY=$GW_PK
AIREOS_AUDIT_CHAIN_PUBLIC_KEY=$AUDIT_PK
AIREOS_AUDIT_ANCHOR_PATH=$STATE/audit/chain-heads.ndjson
AIREOS_CAST_DIR=$STATE/sessions
AIREOS_AUDIT_URL=http://127.0.0.1:8081
AIREOS_SITE_ADDRESS=$SITE_ADDRESS
AIREOS_DEPLOYMENT_PROFILE=$DEPLOYMENT_PROFILE
AIREOS_PAIRING_ENABLED=$PAIRING_ENABLED
API_ADDR=127.0.0.1:8080
AUDIT_ADDR=127.0.0.1:8081
BROKER_ADDR=127.0.0.1:8082
CONNECTOR_ADDR=127.0.0.1:8083
POLICY_ADDR=127.0.0.1:8084
MCP_ADDR=127.0.0.1:8085
CONNECTOR_URL=http://127.0.0.1:8083
POLICY_URL=http://127.0.0.1:8084
BROKER_URL=http://127.0.0.1:8082
GATEWAY_URL=http://127.0.0.1:8085
EOF
  # No operator lines in browser mode: an operator in the environment would always win over setup (ADR-0078 §D4).
  [ -z "$OP_HASH" ] || printf 'AIREOS_BOOTSTRAP_OPERATOR_USER=%s\nAIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH=%s\n' "$OP_USER" "$OP_HASH" >> "$ENV_FILE"
)
chown "$SVC_USER:$SVC_USER" "$ENV_FILE"; chmod 600 "$ENV_FILE"
log "write $CONNECTOR_ENV (sole holder of AIREOS_RESOURCE_SECRET_KEY)"
( umask 077; cat > "$CONNECTOR_ENV" <<EOF
AIREOS_RESOURCE_SECRET_KEY=$RESOURCE_KEY
EOF
)
chown "$SVC_USER:$SVC_USER" "$CONNECTOR_ENV"; chmod 600 "$CONNECTOR_ENV"

# api.env = api's agent-plane signing key only (ADR-0060; least-privilege, like
# connector.env). Loaded by aireos-api.service in addition to aireos.env.
log "write $API_ENV (api agent-plane signing key)"
( umask 077; cat > "$API_ENV" <<EOF
AIREOS_API_AGENT_SIGN_KEY=$API_AGENT_SK
EOF
)
chown "$SVC_USER:$SVC_USER" "$API_ENV"; chmod 600 "$API_ENV"

# audit.env = the chain-head signing SECRET only (ADR-0069 D5; same
# least-privilege pattern as api.env/connector.env). Loaded by
# aireos-audit.service in addition to aireos.env. NEVER write this key to
# aireos.env — see 3c above for why.
log "write $AUDIT_ENV (audit chain-head signing key)"
( umask 077; cat > "$AUDIT_ENV" <<EOF
AIREOS_AUDIT_CHAIN_SIGN_KEY=$AUDIT_SK
EOF
)
chown "$SVC_USER:$SVC_USER" "$AUDIT_ENV"; chmod 600 "$AUDIT_ENV"

# gateway.env = the agent-facing gateway's ENTIRE env (SEC-20: NO AIREOS_PASETO_KEY).
# Its own sign key + api's public key (verify inbound) + the F3 command key, plus the
# URLs/DB it needs. aireos-mcp-gateway.service loads ONLY this file, never aireos.env.
# API_URL (v0.6/OI-13) enables the run.launch delegated-launch forward: the gateway
# POSTs {agent, grant, plan} to api's subject-pinned internal route (ADR-0062); api
# verifies the forward with AIREOS_GATEWAY_PUBLIC_KEY, which already rides shared
# aireos.env (the OI-11 provisioning) — v0.6 writes NO new key material.
log "write $GATEWAY_ENV (keyless of the cluster — SEC-20)"
( umask 077; cat > "$GATEWAY_ENV" <<EOF
DATABASE_URL=$DB_URL
AIREOS_AUDIT_URL=http://127.0.0.1:8081
MCP_ADDR=127.0.0.1:8085
POLICY_URL=http://127.0.0.1:8084
BROKER_URL=http://127.0.0.1:8082
API_URL=http://127.0.0.1:8080
AIREOS_GATEWAY_SIGN_KEY=$GW_SK
AIREOS_API_AGENT_PUBLIC_KEY=$API_AGENT_PK
AIREOS_GATEWAY_COMMAND_KEY=$GW_CMD_KEY
EOF
)
chown "$SVC_USER:$SVC_USER" "$GATEWAY_ENV"; chmod 600 "$GATEWAY_ENV"

# --- 7. validate DB + run migrations (all five services share one DB) -------
log "validate DB connection + run migrations"
export DATABASE_URL="$DB_URL"
for svc in api audit-service session-broker connector-service policy-service mcp-gateway; do
  "$PREFIX/bin/$svc" migrate up \
    || die "migration failed for $svc — is postgres 16 reachable with pgcrypto available? (see the offline postgres guide)"
done

# --- 8. enable + start the services -----------------------------------------
if [ "${AIREOS_SKIP_SYSTEMCTL:-0}" = "1" ] || ! command -v systemctl >/dev/null 2>&1; then
  log "skipping systemctl (AIREOS_SKIP_SYSTEMCTL set or no systemd) — deploy + provision complete"
else
  log "enable + (re)start systemd units — a re-run must pick up the env it just wrote"
  aireos_enable_and_restart_units \
    || die "systemd could not enable and restart the seven aireos-* units — the install is NOT complete. See: systemctl status 'aireos-*' and journalctl -u aireos-api -u aireos-caddy"
  # INS-10: DONE is printed only once every unit (and aireos-postgres, when this host runs it) is active and STAYS
  # so, and Caddy answers HTTPS for the configured address. Each helper dies, naming the unit, on failure.
  log "wait for the units to stay active and for Caddy to answer HTTPS"
  mapfile -t HEALTH_UNITS < <(printf '%s\n' "${AIREOS_UNITS[@]}"; [ ! -f /etc/systemd/system/aireos-postgres.service ] || echo aireos-postgres.service)
  aireos_wait_units_stable "${HEALTH_UNITS[@]}"
  aireos_wait_https "$SITE_ADDRESS"
fi
log "DONE — cockpit installed (deployment profile: $DEPLOYMENT_PROFILE). Caddy serves https://$SITE_ADDRESS (self-signed local CA; import the CA root to remove browser warnings)."
# ADR-0069 D5/SEC-44: this public key is the trust root for offline
# third-party chain verification — record it OUT-OF-BAND (e.g. print this
# line to a separate channel, not just this host's install log) so a verifier
# can be handed it independently of anything this host later claims about
# itself. The key id a verifier derives is a pure function of this value
# (chain.KeyIDFor — first 16 hex chars of SHA-256(pubkey)); no fingerprint is
# computed here.
log "AUDIT CHAIN-HEAD PUBLIC KEY (record out-of-band — this is the trust root for offline verification): $AUDIT_PK"
