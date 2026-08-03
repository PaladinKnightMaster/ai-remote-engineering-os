#!/usr/bin/env bash
# install.sh — offline installer for the AI ReOS application tier (§OI-b, ADR-0051).
#
# Run as root on the TARGET host, from the unpacked bundle directory, AFTER
# postgres 16 + pgcrypto are installed and a cockpit database + role exist (use
# the staged offline companion packages + the offline postgres guide, T-021c).
#
# It deploys the binaries + SPA + config, mints the cluster keys, provisions the
# single bootstrap operator, validates the DB + runs every service's migrations,
# and enables the systemd units.
#
# Provisioning (ADR-0051 OQ-4): interactive prompts by default; set the env vars
# below for an unattended install (the automated WD-1 demo uses that path).
# Idempotent (OI-2): re-running preserves existing cluster keys and re-applies
# migrations as no-ops; the operator is env-defined (no duplicate rows).
#
# Env overrides (any unset value is prompted unless noted):
#   AIREOS_DB_URL                       postgres connection string (required)
#   AIREOS_BOOTSTRAP_OPERATOR_USER      operator username
#   AIREOS_BOOTSTRAP_OPERATOR_PASSWORD  operator password (argon2id-hashed here)
#   AIREOS_SITE_ADDRESS                 Caddy site address (default: localhost)
#   AIREOS_DEPLOYMENT_PROFILE           lan-only | vpn-first | dmz-first (required,
#                                        never defaulted — ADR-0068 §D1/OI-21). Persisted
#                                        in aireos.env. vpn-first/dmz-first REFUSE a
#                                        loopback-only AIREOS_SITE_ADDRESS (OI-22): Caddy
#                                        is the product's only externally-bound process.
#   AIREOS_SKIP_SYSTEMCTL=1             deploy + provision but don't enable units
set -euo pipefail

PREFIX=/opt/aireos
ETC=/etc/aireos
STATE=/var/lib/aireos
ENV_FILE="$ETC/aireos.env"
SVC_USER=aireos
BUNDLE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { printf '\033[1;34m[install]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[install] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# --- deployment-profile helpers (ADR-0068 §D1; OI-21..23) -------------------
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
  for tok in $value; do
    aireos_site_is_loopback "$tok" || return 1
  done
  return 0
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
PERSISTED_PROFILE=""
if [ -f "$ENV_FILE" ]; then
  PERSISTED_PROFILE="$(sed -n 's/^AIREOS_DEPLOYMENT_PROFILE=//p' "$ENV_FILE" | head -n1)"
fi
DEPLOYMENT_PROFILE="${AIREOS_DEPLOYMENT_PROFILE:-}"
if [ -z "$DEPLOYMENT_PROFILE" ]; then
  if [ -n "$PERSISTED_PROFILE" ]; then
    read -r -p "Deployment profile [lan-only|vpn-first|dmz-first] (default: $PERSISTED_PROFILE): " DEPLOYMENT_PROFILE || true
  else
    read -r -p "Deployment profile [lan-only|vpn-first|dmz-first]: " DEPLOYMENT_PROFILE || true
  fi
  # An empty answer takes the persisted value — never a silent default when
  # there is no persisted value either (falls through to the assertion below).
  [ -n "$DEPLOYMENT_PROFILE" ] || DEPLOYMENT_PROFILE="$PERSISTED_PROFILE"
fi
aireos_assert_profile "$DEPLOYMENT_PROFILE" "${AIREOS_SITE_ADDRESS:-}"
log "deployment profile: $DEPLOYMENT_PROFILE"

# --- 1. service user + state dirs -------------------------------------------
if ! id "$SVC_USER" >/dev/null 2>&1; then
  log "create system user $SVC_USER"
  useradd --system --no-create-home --shell /usr/sbin/nologin "$SVC_USER" 2>/dev/null \
    || useradd -r -s /sbin/nologin "$SVC_USER"   # Ubuntu vs RHEL nologin path
fi
install -d -m 755 "$PREFIX/bin" "$PREFIX/web" "$ETC"
install -d -m 750 -o "$SVC_USER" -g "$SVC_USER" "$STATE" "$STATE/sessions" "$STATE/caddy"

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

# --- 4. operator credentials (prompt default, env override; OQ-4) -----------
OP_USER="${AIREOS_BOOTSTRAP_OPERATOR_USER:-}"
OP_HASH="${AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH:-}"
if [ -z "$OP_HASH" ]; then
  [ -n "$OP_USER" ] || read -r -p "Operator username: " OP_USER
  OP_PW="${AIREOS_BOOTSTRAP_OPERATOR_PASSWORD:-}"
  if [ -z "$OP_PW" ]; then read -r -s -p "Operator password: " OP_PW; echo; fi
  [ -n "$OP_USER" ] && [ -n "$OP_PW" ] || die "operator username + password are required"
  OP_HASH="$("$PREFIX/bin/authctl" hash "$OP_PW")"
fi

# --- 5. DB connection string ------------------------------------------------
DB_URL="${AIREOS_DB_URL:-}"
[ -n "$DB_URL" ] || read -r -p "Postgres URL (postgres://user:pass@host:5432/cockpit?sslmode=disable): " DB_URL
[ -n "$DB_URL" ] || die "postgres connection URL required — install postgres 16 + pgcrypto first (see the offline postgres guide)"

# --- 6. write the env files (0600, owned by the service user) ---------------
# aireos.env = shared config for every unit. The resource key is NOT here: it
# lives in connector.env, referenced only by aireos-connector.service (SEC-13 —
# a v0.1.0 aireos.env carrying the key is rewritten without it on upgrade).
log "write $ENV_FILE"
( umask 077; cat > "$ENV_FILE" <<EOF
DATABASE_URL=$DB_URL
AIREOS_PASETO_KEY=$PASETO_KEY
AIREOS_GATEWAY_PUBLIC_KEY=$GW_PK
AIREOS_CAST_DIR=$STATE/sessions
AIREOS_AUDIT_URL=http://127.0.0.1:8081
AIREOS_SITE_ADDRESS=${AIREOS_SITE_ADDRESS:-localhost}
AIREOS_DEPLOYMENT_PROFILE=$DEPLOYMENT_PROFILE
AIREOS_BOOTSTRAP_OPERATOR_USER=$OP_USER
AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH=$OP_HASH
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
  log "enable + start systemd units"
  systemctl daemon-reload
  systemctl enable --now aireos-api.service aireos-audit.service aireos-broker.service \
    aireos-connector.service aireos-policy.service aireos-mcp-gateway.service aireos-caddy.service
fi
log "DONE — cockpit installed (deployment profile: $DEPLOYMENT_PROFILE). Caddy serves https://${AIREOS_SITE_ADDRESS:-localhost} (self-signed local CA; import the CA root to remove browser warnings)."
