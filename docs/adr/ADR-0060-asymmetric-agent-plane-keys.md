# ADR-0060: Asymmetric per-workload keys on the agent-facing planes — removing the cluster key from `services/mcp-gateway`

## Status

**Accepted** — 2026-06-21 (ratified in the T-045 war-room consult). Part of
the v0.4.0 "Hardened Agent Foundation" release
(`docs/evaluation/v0.4.0-release-criteria.md`). Binding spec for the v0.4.0 key
tickets (T-045..T-050, T-053).

**Builds on / conforms to:** ADR-0046 (capability tokens + tiered TTLs; this ADR
**promotes its named P1 trigger** — key separation when the blast radius justifies
it), ADR-0057/0058/0059 (the invocation / broker-exec / approval chain — **audit
and governance unchanged**; only token signing changes), ADR-0050 (the operator
cookie plane stays on `services/api`, v4-local). **Refines ADR-0056 §7** — which
recorded the gateway holding the cluster PASETO key "to verify agent tokens and
mint the service tokens it uses to call policy/broker"; v0.4.0 removes that key
from the gateway. **No ADR divergence (G2-clean):** a *new* ADR refining a prior
decision (the ADR-0049-refines-0048 pattern); **no Accepted ADR is edited.**

## Context

v0.3.0 made `services/mcp-gateway` the first **agent-facing** service, and gave it
the symmetric **cluster** key `AIREOS_PASETO_KEY`. As built (`main.go`), the
gateway holds that one key for **both** inbound verification (`capabilityVerifier`
— the `agent-tool` MCP plane + the `service-service` internal resolve plane) **and**
outbound minting (`serviceSigner` → `IssueService("mcp-gateway", …)` to
policy-service, session-broker, and audit-service).

Under PASETO **v4 local**, mint and verify are the **same** capability — the key
that verifies is the key that signs. So RCE on the gateway yields
`AIREOS_PASETO_KEY` and the attacker can mint a token for **any** edge and **any**
audience: an `agent-tool` token, an operator `human-agent` token, or — the worst
case — a `service-service` token of audience `connector-service`, which leases and
**decrypts any target's credentials**. The agent-facing service therefore carries
the blast radius of the entire cluster, including the crown-jewel credential
custody that v0.2.0 deliberately isolated into connector-service.

This is precisely the P1 trigger ADR-0046 reserved ("key separation when the blast
radius justifies it"); the gateway going agent-facing fired it. Symmetric keys
offer **no partial isolation** — any key that can mint a token a peer accepts *is*
the cluster key — so reducing the gateway's blast radius at all requires removing
the symmetric key from it **entirely**, which forces asymmetric signing on every
edge the gateway touches (inbound verify via a public key, outbound mint via the
gateway's own private key).

## Decision

1. **PASETO v4 public (Ed25519) on the gateway-touching edges only.** The edges
   that migrate to asymmetric:
   - agent → gateway (`agent-tool`) — **api signs, gateway verifies**
   - api → gateway (internal `service-service` resolve/list) — **api signs, gateway verifies**
   - gateway → policy `Decide` (`service-service`) — **gateway signs, policy verifies**
   - gateway → broker exec (`service-service`, audience `session-broker`) — **gateway signs, broker verifies**
   - gateway → audit ingest (`service-service`, audience `audit-service`) — **gateway signs, audit verifies**

   Every **non-gateway** plane stays **v4 local**: the operator cookie
   (operator → api), broker → connector lease, api → policy, api → broker, and the
   other outbox relays. They are not in the gateway's blast radius; converting them
   is out of scope (deferred).

2. **One api agent-plane keypair.** api gains a **single** Ed25519 keypair whose
   **private** key signs **both** the `agent-tool` token it mints
   (`POST /v1/agent-tokens`, as-built) **and** the `service-service` token it
   presents on the gateway's internal resolve plane. The gateway holds the matching
   **one** api **public** key and verifies all inbound traffic with it. api keeps
   the cluster symmetric key for its other planes (operator cookies, api → policy,
   api → broker) — api is dual-key.

3. **One gateway keypair; the gateway holds no cluster key.** The gateway gains its
   own Ed25519 keypair; its **private** key signs every outbound `service-service`
   call (→ policy, broker, audit). policy / broker / audit hold the gateway's
   **public** key to verify those calls. **The gateway boots with
   `AIREOS_PASETO_KEY` unset** and holds only `{gateway private key, api public
   key}`. An attacker who takes the gateway can, at most, impersonate the gateway to
   policy/broker/audit (its legitimate reach) — it **cannot** mint an `agent-tool`,
   `human-agent`, or `connector-service` token (SEC-20/21).

4. **Dual-mode verifier in `packages/go/auth`.** A verifier dispatches on the
   PASETO token type (`v4.local.*` vs `v4.public.*`): local → the cluster symmetric
   key; public → the one registered public key for that plane. It enforces audience
   + edge identically to the symmetric verifier and returns the same typed
   `Capability`, so `RequireCapability` is unchanged. In practice each verifier
   holds **the symmetric key plus at most one public key** — policy and audit are
   dual-mode (they have both symmetric callers, e.g. api, and the asymmetric
   gateway); the gateway's broker-exec verifier is **pure asymmetric** (the gateway
   is its only caller) and is additionally subject-pinned to `mcp-gateway` (R5-ext,
   criteria PIN-1). `packages/go/auth` stays the only package importing the PASETO
   library (the depguard rule).

5. **No registry, no rotation tooling, no JWKS at v0.4.0.** Two pinned keypairs,
   not a per-workload registry — the asymmetric *shape* is built to grow to many
   workloads (Workstreams) but that registry is **not** built now (the gateway
   keeps its single bootstrap agent identity from v0.3.0). Key rotation is
   **redeploy-and-redistribute** (regenerate a keypair, push the new public key to
   verifiers, restart); the 5-min `agent` TTL remains the short-lived-token
   revocation mechanism.

6. **Provisioning (OI-11).** `install.sh` generates the two keypairs, writes each
   private key to its owning service's env file and each public key to the
   verifying services' env files, and **removes `AIREOS_PASETO_KEY` from the
   gateway's env**. Idempotent re-run preserves existing keys; a v0.3.0-layout
   upgrade relocates/rotates deterministically. No SaaS dependency.

7. **No new secret-custody surface.** The gateway still holds **no**
   `AIREOS_RESOURCE_SECRET_KEY` and no decrypt path (SEC-17 unchanged); the SEC-13
   grep gate still names connector-service only. (The separate gateway-local
   command-encryption key — F3 — is decided at the T-051 ticket consult as an
   extension of ADR-0059, not here.)

## Consequences

- The agent surface's blast radius is **bounded and provable**: WD-4's negative
  test asserts the keyless gateway cannot mint a foreign-edge token (SEC-21).
- **api becomes dual-key** (cluster symmetric + the agent-plane asymmetric private)
  and gains an Ed25519 signer for the gateway plane; the minting capability for the
  agent surface moves off the agent-facing service onto api (operator control
  plane, cookie-gated, not agent-reachable).
- **policy and audit verifiers become dual-mode**; the broker exec verifier becomes
  pure-asymmetric + subject-pinned. The change is transport/auth-internal — **no
  OpenAPI surface change, no audit-event change** (VER-12, AUD-12).
- **Zero new third-party dependency** — `aidanwoods.dev/go-paseto` already
  implements v4 public; `task licenses` is unaffected, the offline posture holds.
- The asymmetric primitive + dual-mode verifier are the foundation a later
  per-workload registry (Workstreams) extends — built once, here.

## Deferred (recorded)

- **Converting the non-gateway planes to asymmetric** (operator cookie, broker →
  connector, api → policy/broker) — trigger: a second agent-facing surface, or a
  decision to retire the cluster symmetric key entirely.
- **A full per-workload key registry + key-rotation tooling / JWKS** — trigger:
  Workstreams (many concurrent workloads) or enterprise federation (the ADR-0046
  Zitadel reserve).
- **F3 command at-rest encryption** — its own ticket consult (T-051), extends
  ADR-0059.
