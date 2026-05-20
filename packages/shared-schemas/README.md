# shared-schemas

JSON Schema (draft 2020-12) domain types for AI Remote Engineering OS
v3.0.1. These files are the authoritative field source; the OpenAPI
contract and generated Go/TS types consume them (ADR-0041), wired by
T-009 — there is no shared runtime type package.

The five core entities (`organization`, `user`, `resource`, `session`,
`audit-event`) were landed by T-003; the remaining schemas predate it.
Fixtures for the core entities live in `test/fixtures/`.

