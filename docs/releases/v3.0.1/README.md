# AI Remote Engineering OS v3.0.1 Baseline

v3.0.1 is the canonical development baseline. Status: **APPROVED FOR DEVELOPMENT BASELINE** per `development_readiness_audit_v3_0_1.md` (2026-05-15).

## Source of truth

- `CANONICAL_SOURCE_OF_TRUTH.md` (repo root)
- `docs/releases/v3.0.1/development_readiness_audit_v3_0_1.md`
- `docs/releases/v3.0.1/release_notes_v3_0_1.md`
- `docs/releases/v3.0.1/manifest_reference.md`
- `docs/releases/v3.0.1/package_integrity_report_v3.md`
- `docs/releases/v3.0.1/release_manifest_v3.json`
- `references/references_v3.csv`

## Release assets

Large release assets are not committed to normal Git history. They are kept locally under `artifacts/kits/` (gitignored) and published as GitHub/GitLab/Gitea release assets, or stored in an internal artifact registry for offline environments.

Expected local paths:
- `artifacts/kits/ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `artifacts/kits/AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `artifacts/kits/requirements_roadmap_traceability_v3.xlsx`

Expected release assets:
- `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `requirements_roadmap_traceability_v3.xlsx`
- `sha256_manifest.txt`

## Development rule

Build P0 first. Do not implement P1/P2/P3 without an approved ticket and ADR.
