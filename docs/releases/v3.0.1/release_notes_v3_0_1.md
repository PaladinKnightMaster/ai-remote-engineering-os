# Release Notes — v3.0.1

Date: 2026-05-15
Status: APPROVED FOR DEVELOPMENT BASELINE

## What changed vs v3.0.0

v3.0.1 is a release-hygiene patch on top of v3.0.0:

1. SHA256 manifest regenerated. The v3.0.0 manifest had stale checksums for `manifest.txt` and `sha256_manifest.txt`.
2. The earlier `AI_Agentic_Architecture_Project_Markdown_Package.zip` is now explicitly archived inside the v3 ZIP under `99-archive/`.

No architecture changes. No content loss versus v2.2.

## What v3.0.0 introduced (recap)

- Workstream + Harness + Memory upgrade.
- P0/P1/P2/P3 priority ladder.
- Capability-edge permission model.
- Typed MCP Gateway design.
- 40 ADRs.
- 18 domain events.
- 8 JSON schemas.
- Docker Compose MVP skeleton.
- 40+ tracked references with adoption decisions.

## Canon

Use this v3.0.1 package as the development baseline. Earlier versions (v1, v2, v2.2, v3.0.0) are retained under `docs/deep-dive/` for historical traceability only.
