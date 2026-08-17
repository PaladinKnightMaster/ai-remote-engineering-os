# ADR-0070: Release provenance — signed offline artifacts, SBOM, and an honestly-scoped reproducibility posture

## Status

**Accepted** — 2026-08-16 (T-128, the v0.13.0 S-29 consult; nine decision points D1–D9 signed off on
the war-room recommendation after a pre-authoring audit that found **two gaps neither the war room nor
the release criteria had recorded**). Author/decider: CEO (war-room). Binding spec for T-128–T-130;
code is authored just-in-time per ticket from here.

**G2: clean.** No Accepted ADR is contradicted or edited. This ADR governs artifacts and their
distribution; ADR-0051 (offline installer) and ADR-0052 (per-OS postgres companions) are extended by
one new file each and are otherwise untouched.

---

## Context

For an air-gapped product **the offline artifacts are the trust root.** An operator on a disconnected
network has no upstream to compare against: whatever tarball reaches them *is* the software. The
post-Beta war room recorded this as the third of its three self-findings — *zero supply-chain
attestation, and our release gates are functional (size, version), not provenance.*

### What already exists, and what it is worth

The build is not careless. `infra/offline/build-bundle.sh` already:

- builds **every** Go binary with `-trimpath -ldflags "-s -w"` (services, `authctl`, `aireos-verify`);
- verifies the downloaded Caddy against a **pinned SHA-512** and refuses a mismatch;
- writes a **`SHA256_MANIFEST.txt`** covering every staged file, and self-checks it before packing.

So the gap is not hygiene. **The gap is authenticity**, and it is the same shape ADR-0069 found in the
audit chain: a self-computed integrity structure that the producer both creates and stores. Anyone who
can rewrite the bundle can recompute `SHA256_MANIFEST.txt` and it verifies clean.

**`SHA256_MANIFEST.txt` is to the bundle what `chainHash_N` is to the chain** — a commitment to
everything under it. That parallel is deliberate and this ADR leans on it.

### Two gaps the pre-authoring audit found (recorded because neither was known)

**1. The postgres companion has NO integrity structure at all.** `build-postgres-companions.sh`
produces no manifest, no checksum, nothing — while the artifact is ~87MB, ships a complete package
repository, and runs `install-postgres.sh` **as root** on the target host. The core bundle at least has
a manifest. **The weaker-protected artifact is the one that executes as root.** The war room's phrase
"the offline bundle is the trust root" turns out to describe *two* artifacts, not one.

**2. Nothing verifies anything at install time.** `install.sh` trusts whatever tarball it was handed.
The existing manifest self-check runs at **build** time — it reassures *us* and proves nothing to an
*operator*. Today there is a check where it is comfortable and none where it would matter.

Gap 2 also settles a design question: `install.sh` runs *from inside* the extracted bundle, so it can
never verify the archive it came from. **Verification must be a separate operator step, before
extraction.**

---

## Decision

### D1. A detached Ed25519 signature over each shipped tarball — **both** of them

Every release publishes, alongside each artifact, a detached signature:

```
aireos-offline-<version>.tar.gz            + .sig
aireos-postgres-<ostag>.tar.gz             + .sig
```

**Signing the tarball, not the manifest**, so an operator verifies **before extracting**. You do not
unpack untrusted content in order to decide whether to trust it. The `SHA256_MANIFEST.txt` inside then
provides per-file granularity *after* extraction, and its self-check already works — two levels, one
of which already existed.

**The companion is signed too, and gains its own `SHA256_MANIFEST.txt`** for parity. Signing the
artifact that already had a manifest while leaving unsigned the one with no integrity structure —
which is also the one that runs as root — would be indefensible the moment anyone looked, and S-21's
capstone would have to disclose it.

### D2. Verification is `openssl`, so the verifier needs no distribution

The published verification step is a copy-pasteable command against a tool the operator already has:

```
openssl pkeyutl -verify -pubin -inkey aireos-release.pub \
  -rawin -in aireos-offline-<version>.tar.gz -sigfile aireos-offline-<version>.tar.gz.sig
```

OpenSSL is present on effectively every Linux host and has verified Ed25519 since 1.1.1. **This
dissolves the circularity problem rather than managing it**: SEC-44's rule — a trust root must not
ship only inside the artifact it validates — is satisfied because the *verifier* is not ours to
distribute at all. Only the **public key** needs out-of-band distribution.

We deliberately do **not** ship a bespoke bundle-verifier binary. `aireos-verify` (ADR-0069 D6) is a
different tool for a different artifact; conflating them would put an evidence verifier on the
install path and invite the assumption that one implies the other.

### D3. Key custody — a maintainer-held offline key, and an honest account of what it proves

The release keypair is **held by the maintainer, never in the repository, never on a build host that
runs untrusted code**, and is **distinct from every key `install.sh` mints** (ADR-0060's per-workload
keys, ADR-0069's chain-head key). Those are per-install and machine-generated; this one is per-project
and human-held. They must never be confused, and no tooling should be able to substitute one for the
other.

**What the signature proves:** this artifact was produced by someone holding the release key, and has
not been altered since.

**What it does NOT prove, stated as plainly as ADR-0069 states its residuals:**

- **not** that the build was clean, or that the build host was uncompromised;
- **not** that the artifact corresponds to any particular source revision;
- **not** that the toolchain or dependencies were unmodified;
- **no SLSA level is claimed, at all.** This repository has no CI and no remote (its own standing
  convention), so a hosted-builder attestation is unavailable — not deferred, unavailable. The
  honest description is **build-host attested**, and the release notes say exactly that rather than
  implying a level by silence.

### D4. Verification happens at install time, by the operator, before extraction

The quickstart and the verification runbook lead with the `openssl` command from D2, positioned
**before** the extract step. `install.sh` is not modified to self-verify — it cannot meaningfully do
so (Context, gap 2), and a check that runs after the untrusted code is already unpacked is theatre.

The public key's **fingerprint is published in the release notes** and printed by the release process,
so an operator who obtained the key by a second channel can compare.

### D5. SBOM — CycloneDX 1.6 JSON, with its scope stated rather than assumed

One SBOM per shipped artifact, in **CycloneDX 1.6 JSON**, generated at build time and included in the
bundle *and* published alongside it (an SBOM only readable after extraction is less useful to the
person deciding whether to extract).

**Scope is explicit, because a partial SBOM presented as complete is worse than none:**

| Component class | In scope | How |
|---|---|---|
| Go modules (all services + `authctl` + `aireos-verify`) | **yes** | generated from the module graph |
| Node / pnpm dependencies of `apps/web` | **yes** | generated from the lockfile |
| **Caddy** (downloaded binary) | **yes** | recorded as a component with its pinned version + SHA-512 |
| **Postgres packages** in the companion | **yes** | the companion's own SBOM, from its package repo |
| The Go toolchain itself | **no** — recorded as build metadata, not a component |

The existing `task licenses` (`go-licenses`) is **retained, not replaced** — it answers a licensing
question, the SBOM answers a composition question, and collapsing them would lose one.

### D6. Reproducibility — normalize the archive first, then measure

The tar invocation today passes no `--sort`, no `--mtime`, no `--owner`/`--group`. **Two builds
therefore cannot produce identical bytes regardless of whether the contents are deterministic**, so
measuring as-is would burn the measurement on a known packaging artifact and leave the real question
unasked.

Normalize first — `--sort=name`, a fixed `--mtime`, `--owner=0 --group=0 --numeric-owner` — then build
twice and diff, to answer the question that matters: **are the contents reproducible?** Go with
`-trimpath` largely is; the pnpm SPA build may not be.

### D7. Non-reproducibility is a finding, not a blocker

Per **OI-35**, which the Accepted criteria deliberately wrote as measure-don't-block: the result is
**published either way**. A release that says "our bundle does not currently reproduce, here is what
differs" is more useful — and more honest — than one that quietly never asked.

This is also why **T-130 is the narrowed fallback** if S-29 proves expensive: the signature (D1–D4)
and the SBOM (D5) carry most of the value and are mechanical; the reproducibility *measurement* is the
leg with genuine unknowns, and the criteria already class its negative outcome as acceptable.

### D8. Key rotation is additive-forever

Release public keys are **published cumulatively and never retired** — an operator verifying an older
artifact needs the key it was signed under. Rotation adds a key; it does not invalidate history. Same
rule as ADR-0069 D5, for the same reason: retiring a key retroactively unverifies everything it signed.

### D9. `release-check` gains a provenance gate

`task release-check` fails if a signature or an SBOM is absent, or if SBOM coverage omits a shipped
component. Rationale: **a provenance gate that cannot fail is the S-25 pattern** — and this release
exists partly to hunt that pattern. The gate must be demonstrated failing on a missing artifact, per
VER-16.

---

## Security posture

**What this closes.** An attacker who substitutes or modifies either shipped artifact produces a file
whose signature no longer verifies under the published key. An operator on a disconnected network can
detect that with a tool already on their machine, before executing anything.

**What it does not close, all stated in the T-134 threat model:**

1. **A compromised build host.** The signature attests the artifact, not the process. An attacker who
   owns the machine that builds *and* signs produces a validly-signed malicious bundle. Closing this
   needs a trusted builder, which needs a CI, which this project does not have.
2. **Key compromise.** A leaked release key forges any artifact. Mitigation is custody (D3) and the
   additive key list (D8), not cryptography.
3. **First-contact trust.** An operator receiving both the artifact and the key from the same channel
   for the first time has verified internal consistency, not provenance. The fingerprint in the
   release notes exists so a second channel is *possible*; we cannot force one.

**Note on relative weight.** Residual 1 is the same shape as ADR-0069's audit-service-RCE residual —
in both cases the signer and the thing being signed sit inside one trust boundary. That is the
honest through-line of both ADRs, and it is what a containment layer (v0.14) and an external witness
would begin to address.

---

## Consequences

- Two new published files per release (`.sig` per artifact), plus one SBOM per artifact.
- The companion build gains a `SHA256_MANIFEST.txt`, reaching parity with the core bundle.
- The tar invocation changes (D6) — a one-line-ish normalization that also makes future diffs legible.
- `release-check` gains a gate that can fail (D9).
- A **human step enters the release process**: someone must hold a key and sign. That is a real
  operational cost and the reason it is stated rather than automated away.
- The release notes acquire a permanent, honest non-claim: **no SLSA level**.
- Verification instructions become part of the quickstart, which **S-26's gate then exercises** — so
  the instructions are executed, not merely written.

---

## Relation to prior ADRs

| ADR | Relation |
|---|---|
| **ADR-0069 — audit chain authenticity** | **The direct parallel, and deliberately so.** There: a self-computed hash chain gains a signature. Here: a self-computed file manifest gains one. Both find the same shape — the producer computing and storing its own integrity structure — and both answer it with a key the reader can check independently. Keys are **separate** (D3): per-install and machine-minted there, per-project and human-held here. |
| **ADR-0051 — offline installer** | Extended by the D4 pre-extraction verification step in the documented flow. `install.sh` itself is **unmodified**; it cannot verify the archive it runs from. |
| **ADR-0052 — per-OS postgres companions** | Extended: the companion gains a manifest and a signature (D1). Its per-OS/per-artifact shape is unchanged. |
| ADR-0060 / ADR-0044 / ADR-0053 | Unaffected. No per-workload key, migration, or credential path changes. |

---

## Deferred (recorded)

| Item | Trigger |
|---|---|
| **A trusted builder / hosted attestation (any SLSA level)** | A CI and a remote. Both are currently declined by standing convention; revisit if that changes. |
| **External witness / transparency log for releases** | Out of scope for an air-gapped product by doctrine; would need its own ADR and a no-SaaS story. Pairs naturally with ADR-0069's external-witness successor. |
| **Signing individual binaries inside the bundle** | The archive signature plus the per-file manifest covers the contents. Revisit if binaries are ever distributed separately from the bundle. |
| **Reproducible builds as a release blocker** | Deliberately not adopted (D7 / OI-35). Revisit when a measurement shows the contents already reproduce. |
| **Key-compromise / revocation procedure** | No key exists to compromise yet. Needed before any external distribution at scale. |

---

## Ratification (resolved 2026-08-16)

Nine decision points, signed off on the war-room recommendation. **A pre-authoring audit ran before
this document was written and changed its scope** — recorded here because the corrections, not the
original proposal, are what is Accepted:

1. **The postgres companion has no integrity structure at all** — not even the manifest the core
   bundle has — while being the artifact that executes as root. The proposal covered one artifact;
   the decision covers **both** (D1).
2. **Nothing verifies anything at install time.** The existing manifest self-check runs at build time
   and proves nothing to an operator. This moved verification into the documented **operator**
   flow, before extraction (D4).
3. **The tarball is definitionally non-reproducible today** (no `--sort`/`--mtime`/`--owner`), so the
   reproducibility measurement would have been vacuous. Normalize first, then measure (D6).

Neither of the first two was recorded by the war room or the release criteria. Both were found by
reading `build-bundle.sh`, `build-postgres-companions.sh` and `install.sh` rather than by re-reading
the plan — the same method, and the same result, as ADR-0069's pre-authoring audit.
