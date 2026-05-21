# packages/go

Shared Go infrastructure modules under the `go.work` workspace (ADR-0042).
Each module materializes with the ticket that first needs it:

- `auth/`       — PASETO v4 signer / verifier + `CapabilityToken` (ADR-0046). *Pending — T-008.*
- `middleware/` — chi-compatible capability-edge middleware (ADR-0043). *Landed — T-004a.* Exports `RequireCapability` (a `func(http.Handler) http.Handler`) and the `Verifier` port; the concrete PASETO verifier arrives with `auth/` (T-008).
- `migrate/`    — `golang-migrate` v4 library-mode runner (ADR-0044). *Landed — T-004a.* Exports `Runner` + `RunSubcommand` (`<service> migrate up|version|force`).
- `outbox/`     — Postgres transactional outbox writer + relay (ADR-0045). *Pending — first producer ticket.*

Module-path prefix: `aireos.local/packages/go/<name>` (see `go.work`).

This directory is intentionally absent from `pnpm-workspace.yaml` — these
are Go modules, not pnpm members.
