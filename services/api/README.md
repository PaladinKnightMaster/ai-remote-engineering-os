# api

Operator-facing gateway (P0). chi on net/http, oapi-codegen chi-server stubs
(ADR-0043), `golang-migrate` (ADR-0044), capability-edge middleware + PASETO
auth (ADR-0043/0046). Module: `aireos.local/services/api`.

## Surface

- **Resource registry** (`/v1/resources` CRUD) — T-005a lands the store +
  migration; T-005b wires the HTTP surface + codegen.
- **Operator login** (`/v1/auth/login`, `/v1/auth/me`), signing-key custody, and
  `task auth rotate-key` — deferred to the auth ticket (ADR-0046 §Hand-off).

## Run

```
export DATABASE_URL=postgres://cockpit:change-me@localhost:5432/cockpit?sslmode=disable
go run ./services/api migrate up      # apply 0001_init (resources table)
go run ./services/api serve           # :8080, /healthz
```

`resources` is a standard mutable table with up/down migrations (services/api is
not forward-only; only audit-service is, ADR-0044).
