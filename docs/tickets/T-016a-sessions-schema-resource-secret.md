---
name: T-016a Ticket
about: SSH end-to-end vertical slice (a) — persistence + secrets half
title: "T-016a: sessions + resource-secret migrations + pgcrypto provisioning"
labels: ["ticket"]
---

## Ticket ID and link

T-016a — first half of the SSH end-to-end vertical slice (a) per
ADR-0048 §Hand-off. No B-NNN counterpart (cockpit-feature ticket against
ADR-0048, not a frozen v3.0.1 backlog row). Sibling: **T-016b** (the
`/v1/sessions` REST control plane) depends on the `sessions` table this
ticket creates.

## Goal

Stand up the persistence + secrets foundation for cockpit sessions:
the `sessions` table, the pgcrypto-encrypted `resources.connection_secret`
column, and the operator-side resource-secret provisioning CLI. No HTTP
surface, no audit emission (both are out of scope — see below).

## Priority

P0 — v0.1.0 wedge (ADR-0048, v0.1.0 release-criteria §SEC-12). ADR-0048
is Accepted; no new ADR required.

## Files in scope

- Create: `services/api/migrations/0002_sessions.up.sql`
- Create: `services/api/migrations/0002_sessions.down.sql`
- Create: `services/api/migrations/0003_resource_secret.up.sql`
- Create: `services/api/migrations/0003_resource_secret.down.sql`
- Create: `services/api/internal/secret/secret.go`
- Test:   `services/api/internal/secret/secret_integration_test.go`
- Modify: `services/api/main.go` (`resources genkey|set-secret` subcommand + key-separation guard)
- Modify: `Taskfile.yml` (`task resources`)
- Create: `docs/tickets/T-016a-sessions-schema-resource-secret.md` (this file)

9 files. Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (war-room consult, 2026-05-28)

- **`sessions.resource_id` is `UUID`, not `TEXT`.** ADR-0048 §3's
  illustrative SQL types it TEXT, but `resources.id` is UUID
  (`0001_init.up.sql`); Postgres rejects a TEXT→UUID foreign key. The
  real schema wins. `operator_id` stays TEXT (bearer subject is a string).
- **Two migrations, not one** (per-feature-per-migration, ADR-0044 norm):
  `0002_sessions` creates the table; `0003_resource_secret` alters
  `resources`. Independent rollback; both have `.up`/`.down` (services/api
  is mutable, not forward-only).
- **Secret blob shape (settles the slice-b read contract):**
  `connection_secret` BYTEA holds `pgp_sym_encrypt(json{private_key,
  passphrase}, AIREOS_RESOURCE_SECRET_KEY)`. Non-secret connection
  coordinates (host, port, login user) live in `resources.labels`.
- **Provisioning CLI is a `services/api` subcommand**, not an `authctl`
  command: it needs DB access + the key, unlike the stateless authctl
  (`genkey`/`hash`). `genkey` is offline (no DB); `set-secret` needs both
  `API_DATABASE_URL` and `AIREOS_RESOURCE_SECRET_KEY`. Key material is
  supplied via `--key-file` (not stdin/argv) to keep private keys out of
  shell history and `ps`.
- **Key separation enforced (defense-in-depth, §SEC-12):** services/api
  fails loud if `AIREOS_RESOURCE_SECRET_KEY == AIREOS_PASETO_KEY` — at
  startup (only when the resource key is present) and in `set-secret`.
- **Audit-silent.** No `session.open`/lifecycle events here: no
  `packages/go/outbox/` exists yet, and services/api emits no audit events
  today. Lifecycle audit is emitted by `services/session-broker` (slice b),
  which builds the first outbox and holds the cast-file SHA256. Consistent
  with today's audit-silent resource CRUD.

## Acceptance criteria

- [x] `0002_sessions` + `0003_resource_secret` apply cleanly via
      `go run ./services/api migrate up` on a DB already at `0001`.
- [x] `connection_secret` round-trips through `secret.Set`/`secret.Get`
      (pgp_sym_encrypt/decrypt); stored bytes contain no plaintext marker.
- [x] Wrong decrypt key surfaces an error (no silent garbage).
- [x] `api resources genkey` prints a 64-char hex key offline (no DB).
- [x] `api resources set-secret <unknown-id>` returns ErrNotFound.
- [x] Key-separation guard fails loud (exit 1) when the two keys collide,
      *before* any DB connection.
- [x] OpenAPI / schemas: **n/a** for T-016a (the wire surface is T-016b).
- [x] ADR updated if applicable: **n/a** (ADR-0048 already Accepted).
- [x] Unit/integration tests cover valid + invalid + edge cases.
- [x] `golangci-lint` (T-011 config) clean on `services/api`.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **FK type — violating:** `resource_id TEXT NOT NULL REFERENCES
  resources(id)` → migration fails (`foreign key constraint ... cannot be
  implemented`, TEXT vs UUID). **Passing:** `resource_id UUID` → applies.
- **Key separation — violating:** `AIREOS_RESOURCE_SECRET_KEY ==
  AIREOS_PASETO_KEY` → `api serve` / `set-secret` exit 1 with the
  authority-separation error. **Passing:** distinct keys → proceeds.
- **At-rest encryption — violating:** a plaintext column would let the
  `TestSecretStoredCiphertextNotPlaintext` marker appear in
  `connection_secret`. **Passing:** pgcrypto ciphertext contains no marker.

## Out of scope

- `/v1/sessions` REST CRUD, OpenAPI fragment, oapi-codegen tag → **T-016b**.
- Session-broker WS data plane, host-key pinning, decryption-at-session-open,
  per-input-line audit, cast files → slices (b)/(d).
- `packages/go/outbox/` + any `session.*` audit event → slice (b).
- Key rotation UX, `install.sh` wiring → release-prep / devops ticket.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 9 files, well under.

## ADRs required

- None new. Implements ADR-0048 §3 (sessions table) + §7 (pgcrypto target
  credentials); satisfies v0.1.0 release-criteria §SEC-12.

## Verification

```sh
# build + vet + lint (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/src" -w /src/services/api golang:1.23 \
  sh -c "cd /src && go build ./services/api/... && go vet ./services/api/..."

# migrations + integration round-trip (MVP Postgres on docker-compose_default)
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
docker run --rm --network docker-compose_default -v "$PWD:/src" -w /src \
  -e API_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  -e API_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 sh -c \
  "go run ./services/api migrate up && go test -tags integration ./services/api/internal/secret/... ./services/api/internal/store/..."
```

Expected: migrate up succeeds; `ok aireos.local/services/api/internal/secret`
and `.../store`; `genkey` prints 64 hex chars; key collision exits 1.
(All confirmed 2026-05-28 on Docker 29.1.3 / golang:1.23.)
