# audit-service migrations — forward-only

`audit-service` is **forward-only by policy** (ADR-0044). The `audit_events`
table participates in the chained-hash evidence chain (ADR-0045); a destructive
`DOWN` migration would forge integrity.

Rules (ADR-0044 §Decision):

- **No `*.down.sql` is shipped for the evidence chain.** `0001_init` has an
  `up` file only. The `migrate` subcommand exposes `up | version | force` and
  **not `down`** (see `packages/go/migrate/subcommand.go`), so an operator
  cannot run a rollback against this service even by mistake.
- A future `DOWN` file may exist **only** for structural-only reversals (drop an
  added index, drop a never-written column). It MUST NOT drop, rewrite,
  truncate, or alter `audit_events` or any table in the chain.
- The database also enforces append-only at runtime: a `BEFORE UPDATE OR DELETE`
  trigger on `audit_events` raises, so rows can never be mutated in place.

Migrations run as a deliberate operator action via `task migrate -- audit-service up`,
never on service boot.

> Lint enforcement of this policy is deferred to P1 (ADR-0044 §Verification
> known gap); it is review-enforced at P0.
