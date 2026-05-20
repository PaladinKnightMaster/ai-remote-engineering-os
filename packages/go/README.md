# packages/go

Shared Go infrastructure modules under the `go.work` workspace (ADR-0042).
Each module materializes with the ticket that first needs it:

- `auth/`       — PASETO v4 signer / verifier + `CapabilityToken` (ADR-0046).
- `middleware/` — chi-compatible capability-edge middleware (ADR-0043).
- `migrate/`    — `golang-migrate` v4 library-mode runner (ADR-0044).
- `outbox/`     — Postgres transactional outbox writer + relay (ADR-0045).

This directory is intentionally absent from `pnpm-workspace.yaml` — these
are Go modules, not pnpm members.
