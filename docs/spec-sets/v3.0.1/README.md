# Spec-set edition SPEC-2026.1 (legacy designation v3.0.1)

**This is a SPEC-SET edition, not a product release.** It is the blueprint the product is
built *from*; it is never installed, tagged, signed, or shipped. The product release line is
separate and independent — see `CANONICAL_SOURCE_OF_TRUTH.md` §Version namespaces.

SPEC-2026.1 is the canonical development baseline. Status: **APPROVED FOR DEVELOPMENT
BASELINE** per `development_readiness_audit_v3_0_1.md` (2026-05-15).

> **Why two names.** `v3.0.1` was minted before the product had a version line of its own, and
> it collided with it: a reader seeing `v3.0.1` beside product `v0.14.0` reasonably concludes the
> spec set is nine minor versions ahead of the code. It is not — the two count different things.
> `SPEC-2026.1` is the canonical identifier from now on. The **directory and artifact filenames
> keep `v3.0.1`** deliberately: the published ZIP's SHA-256 manifest attests to that name, and
> renaming a released artifact would falsify the record it exists to provide. Editions from
> **SPEC-2026.2** onward use the `SPEC-<year>.<n>` identifier for their directory names too.

## Source of truth

- `CANONICAL_SOURCE_OF_TRUTH.md` (repo root)
- `docs/spec-sets/v3.0.1/development_readiness_audit_v3_0_1.md`
- `docs/spec-sets/v3.0.1/release_notes_v3_0_1.md`
- `docs/spec-sets/v3.0.1/manifest_reference.md`
- `docs/spec-sets/v3.0.1/package_integrity_report_v3.md`
- `docs/spec-sets/v3.0.1/release_manifest_v3.json`
- `references/references_v3.csv`

## Edition assets

Large edition assets are not committed to git history. They live in the sibling workspace
directory `../artifacts/ai-remote-engineering-os/v3.0.1/` (outside this repo, not gitignored
*here* — it is simply not part of this repo). Refresh and integrity-verification procedure:
`../VERSION_CONTROL.md`.

Delivered as a standalone file:
- `../artifacts/ai-remote-engineering-os/v3.0.1/ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip` — 317,581 bytes

Produced, but **only inside that ZIP** — never published beside it, so the SHA-256 manifest
cannot be used to verify them without extracting first:
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf` (7,308 B)
- `requirements_roadmap_traceability_v3.xlsx` (39,852 B)
- `sha256_manifest.txt` (20,231 B)

> Corrected 2026-08-27: this file previously pointed at `artifacts/kits/`, a path that has not
> existed since the May 2026 workspace restructuring. `CANONICAL_SOURCE_OF_TRUTH.md` also listed
> the latter three as *"(when delivered)"*, implying they did not exist; they do, inside the ZIP.
> Both statements were wrong in different directions — verified against the archive 2026-08-27.

## Development rule

Build P0 first. Do not implement P1/P2/P3 without an approved ticket and ADR.
