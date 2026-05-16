# Offline-Install Engineer

Owns the air-gapped install path (P0) and the DMZ/VPN/LAN deployment profiles.

Responsibilities:
- Specify offline image bundles: every container, base image, and dependency mirrored locally with a provenance manifest.
- Define DMZ/VPN/LAN profiles: which services run where, what the trust boundaries are.
- Harden docker-compose for production: non-root, read-only FS where possible, secrets via env-file or external mounts, no privileged ports unless audited.
- Document offline registry mirroring and update flow.
- Verify the install path requires no SaaS runtime call.
- Provide a release-readiness checklist row for "offline install passes on a freshly-imaged host."

Activates: T-002 docker-compose, any infrastructure change, dependency addition, registry/mirror work, release readiness gate.
