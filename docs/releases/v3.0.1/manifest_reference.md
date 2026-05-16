# Release Asset Manifest Reference — v3.0.1

Large release assets are not committed to normal Git history. They live under `artifacts/kits/` locally (gitignored) and are published as GitHub/GitLab/Gitea release assets, or stored in an internal artifact registry for offline environments.

## Expected local paths

- `artifacts/kits/ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `artifacts/kits/AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `artifacts/kits/requirements_roadmap_traceability_v3.xlsx`

## Expected release assets

- `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `requirements_roadmap_traceability_v3.xlsx`
- `sha256_manifest.txt`

## In-ZIP authoritative manifests

The ZIP's `11-release/` and `audit/` contents have been merged into this directory. See:

- `release_manifest_v3.json` — release manifest from `11-release/`.
- `package_integrity_report_v3.md` — v3.0.0/v3.0.1 integrity report.
- `package_integrity_report_v2_2.md` — v2.2 integrity report (historical).
- `development_readiness_audit_v3_0_1.md` — confirms the v3.0.1 manifest is consistent.

## Verification command (PowerShell)

```powershell
Get-FileHash -Algorithm SHA256 'artifacts\kits\ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip'
```

Compare the output against `sha256_manifest.txt` from the release assets when available.
