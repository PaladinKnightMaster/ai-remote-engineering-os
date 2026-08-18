# v0.13.0 — Evidence Hardening

**The release that makes our central claim checkable by someone who does not trust us.**

Before this release, the audit chain proved **ordering**, not **authenticity**: an unkeyed SHA-256
chain that `audit-service` both computed and stored. Anyone able to rewrite the store could recompute
it from genesis and it verified clean. v0.13.0 signs the chain, anchors it outside Postgres, and ships
a separate binary that verifies an evidence export **with the public key alone — no database, no
network, no trust in our control plane.**

---

## What shipped

**Audit-chain authenticity (S-28, ADR-0069).** Ed25519-signed chain heads on a bounded cadence, off
the write path. Heads anchored to an append-only file outside the database. A standalone
`aireos-verify` that a third party runs against an export. The chain-signing key lives *alone* in
`/etc/aireos/audit.env` — not the shared environment file — so only an `audit-service` compromise
reaches it.

**Release provenance (S-29, ADR-0070).** Detached Ed25519 signatures over **both** shipped artifacts —
the core bundle and the postgres companion, which is the one that runs as root. CycloneDX 1.6 SBOMs
whose coverage is checked against what is actually in the artifact, not merely present. Verification
is a copy-pasteable `openssl` command, so the verifier is not ours to distribute.

**Reproducibility, measured and published either way (OI-35).** The bundle reproduces byte-for-byte
from the same commit — confirmed twice, including with the Go build cache **wiped between builds**, so
the result is not a caching artifact.

**Disclosure (S-23/24/27/30).** A threat model with an OWASP mapping, a containment boundary, a
session-revocation posture, and a nine-row residual register — every claim carrying a file-path
citation checked at authoring.

**Enforcement-of-enforcement (S-25) and the quickstart as a gate (S-26).** Tests that assert our
guardrails match real code, and an automated run of the published quickstart from the built bundle on
a fresh host.

**Resource lifecycle.** `CreateResource` now chains an audit event; the unused `PUT /v1/resources/{id}`
was removed.

---

## What this release explicitly does **not** claim

Stated plainly, because a buyer will find these out and it is better said by us.

- **No SLSA level, at any tier.** This repository has no CI and no remote, so a hosted-builder
  attestation is *unavailable* — not deferred. The honest description is **build-host attested**. The
  signature proves an artifact came from someone holding the release key and has not been altered
  since. It does not prove the build host was clean or the toolchain unmodified.
- **No external witness.** Chain heads are signed *in-service*. An RCE on `audit-service` itself can
  still sign forged heads. The signer and the signed sit inside one trust boundary. A transparency
  log or external witness is what closes this, and it is scoped to v0.14.
- **No containment enforcement.** Our controls hold for actions traversing the gateway. **A process
  spawned by an approved `remote.exec` is not contained, and its own network activity is ungoverned.**
  Enforcement lands at v0.14 under its own ADR and WD-12.
- **No generalized session revocation.** Operator sessions are stateless bearer tokens with an 8-hour
  TTL; `logout` clears the cookie but does not invalidate the token. The paired tier is the product's
  only revocable session — and even that revoke is best-effort, falling back to row expiry if the
  database is unavailable. The fix is B-032 at v1.1.
- **No segregation of duties.** One operator account. Whoever launches a governed action can also
  approve it.

Full detail and the residual register: `docs/security/threat-model.md`.

---

## Corrections of record

**Our OWASP ASI08 mapping was wrong, twice-committed.** We had mapped the audit-chain work to ASI08
believing that category is "Repudiation and Untraceability". **ASI08 is *Cascading Failures*.**
"Repudiation and Untraceability" is **T8** in a separate OWASP taxonomy. The corrected mapping and our
genuine ASI08 posture (partial — no blast-radius limiter across individually-approved steps) are in
the threat model §1.1. The error survived a war room and an ADR because every reader inherited it; it
was caught only by checking the external source at authoring time.

**Positioning.** Microsoft's agent-governance-toolkit independently arrived at roughly five of our
seven controls, *including* the credential-broker property we had treated as differentiating. Our
genuinely unmatched control is the **air-gap posture**.

---

## Install-wiring diff — enumerated, not claimed zero (OI-40)

The zero-diff property that held from v0.9 through v0.11 **does not hold here.** v0.13.0 adds exactly
two things to `install.sh`:

1. **One keypair** — the audit chain-head signing key. The secret goes to a **new** `/etc/aireos/audit.env`
   (mode 0600, `aireos:aireos`), never the shared `aireos.env`; the public half goes to `aireos.env`
   and is echoed at install for out-of-band recording.
2. **One anchor path** — `$STATE/audit`, with `ReadWritePaths=/var/lib/aireos` on the audit unit
   (following the existing broker precedent rather than introducing a second mechanism).

**Idempotence is preserved (OI-41)** and tested: re-running `install.sh` reuses the existing key rather
than regenerating it, and never truncates the anchor. Regeneration would silently orphan every
historical signature — the chain would still verify structurally while every signature became
uncheckable forever. A partial-state recovery path (secret present, public half missing) **derives**
the public key from the secret rather than minting a fresh pair.

---

## Upgrading

Migrations are additive. `audit-service` **fails to boot without a signing key** — this is deliberate:
the alternative was a degraded unsigned mode, and a release whose central claim is evidence integrity
should not offer one. `install.sh` provisions the key, so a normal upgrade path is unaffected.

---

## Verification

Operators should verify before extracting. See `verification_runbook.md` Step 4 for the full
third-party walkthrough, and `docs/devops/offline-install-v3.md` for key custody.

The published SHA-256 fingerprint of the release public key accompanies these notes; artifact hashes
are published in a sidecar rather than embedded here, because any commit to this file would change the
artifacts it describes.
