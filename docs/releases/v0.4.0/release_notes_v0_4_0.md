# AI Remote Engineering OS — v0.4.0 release notes

**Status: release-candidate — pending the CI/CEO WD-4 run + tag.** The tag awaits a
from-HEAD offline-bundle rebuild + `task wd1` (systemd-in-Docker) on a build host
with native Go, the full `task release-check`, the CEO §human WD-4 walkthrough, and
explicit sign-off (local annotated tag only, no remote). v0.4.0 — **"Hardened Agent
Foundation"** — is a **security** release on the v0.3.0 Typed MCP Gateway (per
`docs/evaluation/v0.4.0-release-criteria.md`; archival copy beside this file;
machine-readable coverage in `release_manifest_v0_4_0.json`).

## Why (the blast radius v0.3.0 left open)

v0.3.0 made `services/mcp-gateway` the first **agent-facing** service and gave it
the one symmetric cluster key (`AIREOS_PASETO_KEY`). Under PASETO v4 *local* the same
key signs and verifies — so RCE on the agent surface meant *mint any token for any
edge*, including a `connector-service` lease that decrypts **any** target credential.
That is total-cluster blast radius on the most exposed service. v0.4.0 closes it
(the ADR-0046 P1 key-separation trigger, fired).

## What landed (the hardening)

- **Per-workload asymmetric keys (R1; T-045…T-049, ADR-0060).** Every edge the
  gateway touches moved to PASETO **v4 public** (Ed25519). `services/api` signs the
  `agent-tool` token + its gateway-bound `service-service` token with a single
  agent-plane private key; the gateway verifies inbound with api's **public** key
  and signs its outbound (→ policy / broker / audit) with its **own** key. A
  **dual-mode verifier** (`packages/go/auth`) lets policy / audit / broker-exec
  accept either the cluster `v4.local` token (api + other symmetric callers) or the
  gateway's `v4.public` token. **The gateway now boots with NO `AIREOS_PASETO_KEY`
  (SEC-20)** — it can mint nothing. Non-gateway planes stay symmetric (scoped,
  zero-downtime migration via the dual-mode verifier).
- **S2S subject-pins (R5-ext; T-050).** The broker `/v1/exec` handler pins the
  caller to `mcp-gateway`; the connector lease handler pins to `session-broker` —
  defense in depth over the `service-service` edge.
- **Command at-rest encryption (F3; T-051, extends ADR-0059).** The persisted
  invocation command is AES-256-GCM encrypted with a **gateway-local** key (distinct
  from the cluster + resource keys), decrypted only on the operator-only read path.
  Application-level (not pgcrypto) — the key never reaches Postgres and the SEC-13
  grep gate (connector-only `pgp_sym_decrypt`) stays pristine.
- **SEC-21 blast-radius proof (T-052).** A live negative test forges a
  `connector-service` lease token (and an api token) with the gateway's *own* key
  and asserts both are rejected (401) — the keyless gateway can reach no foreign
  edge. The installed `run-wd1.sh` adds the structural analogue (gateway env carries
  no cluster key).
- **Keyless installed stack (OI-11; T-053a).** `install.sh` provisions the two
  keypairs + the F3 key (`authctl genkeypair` / `genaeskey`), into per-holder env
  files (`api.env`, `gateway.env`); the gateway systemd unit loads `gateway.env`
  (not `aireos.env`) so it never sees the cluster key. Idempotent re-run; in-place
  v0.3.0 → v0.4.0 upgrade.

## Cumulative security audit (done, clean)

A holistic red-team over T-045…T-052 ran before closure (the pre-tag review for the
security substance). **No exploitable bypass, no code fix needed.** Confirmed: the
blast-radius reduction is *structural* (`AIREOS_GATEWAY_PUBLIC_KEY` is held by
exactly policy/broker/audit — never connector/api); the SEC-13 gate passes (F3
introduced no `pgp_sym_decrypt`); **zero new dependencies** since v0.3.0; no test was
weakened to pass.

## Release gates

`task release-check` is the machine gate (lint / sec13-gate / licenses / test /
integration / typecheck / web build / codegen drift / e2e / smoke). The e2e leg now
includes the **SEC-21 negative** + the full chain driven by a keyless gateway.
Remaining before the tag (CI/CEO-env — the build host needs native Go to cross-
compile the bundle): the from-HEAD `task offline-bundle` + `task wd1` (installed
WD-4), full `task release-check`, the CEO §human WD-4, and the tag decision.

## Known limits / deferred (with triggers)

- **Converting the non-gateway planes to asymmetric** (operator cookie, broker →
  connector, api → policy/broker) → v0.5.0+ (trigger: a second agent-facing surface,
  or retiring the cluster key entirely). v0.4.0 de-keys only the agent surface.
- **A full per-workload key registry + rotation tooling / JWKS** → v0.5.0+ (trigger:
  Workstreams / many concurrent workloads, or enterprise federation). v0.4.0 ships
  two pinned keypairs; rotation = redeploy-and-redistribute.
- **Persisted-command GC** beyond the reaper → deferred (F3 encrypts; the reaper
  bounds parked-state retention). Operational note: losing
  `AIREOS_GATEWAY_COMMAND_KEY` makes parked commands unreadable (errors, not crashes;
  bounded by the reaper / ephemeral rows).
- **Workstreams, Memory** → next P1 modules (consume this gateway, now on an isolated
  key foundation).
- **TR-2** (external MCP-server proxying + untrusted-server isolation); **full
  per-edge rate-limiting + quotas; SSH connection pooling** → unchanged v0.3.0
  deferrals.
- **Platform:** Linux server (Ubuntu/Debian + RHEL-family, amd64); Windows = browser
  client.

## Verify the release

```sh
task offline-bundle         # fresh bundle from HEAD (BUNDLE_VERSION=0.4.0)
task release-check          # all must-pass gates incl. sec13-gate + e2e (SEC-21) + smoke
task wd1                    # installed WD-4: keyless gateway drives the chain + SEC-20 check
sha256sum -c SHA256_MANIFEST.txt   # on the unpacked bundle (OI-9)
```
