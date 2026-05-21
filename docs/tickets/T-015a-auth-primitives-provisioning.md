---
name: T-015a Operator auth primitives + provisioning (argon2id, bootstrap account, authctl)
about: The non-HTTP half of operator login — argon2id password hashing, the single bootstrap operator account, and the authctl provisioning CLI
title: "T-015a: Operator auth primitives + provisioning (argon2id + authctl)"
labels: ["ticket", "P0"]
---

## Ticket ID and link

T-015a — see `docs/tickets/README.md`. The first half of the operator-login work
deferred by T-008 (ADR-0046 §Hand-off: "the first auth-touching
service-implementation ticket should land operator login"). This half adds the
crypto + account + provisioning primitives; the `/v1/auth/login` + `/v1/auth/me`
HTTP surface is **T-015b**. Lead lens: **Permission Model Reviewer**.

## Goal

Land argon2id password hashing in `packages/go/auth`, the single bootstrap
operator account (env-provisioned) in `services/api`, and an `authctl` CLI that
generates the cluster key + password hashes operators set as env vars.

## Priority

P0. Unblocks T-015b (login issues a token after `account.Authenticate`).

## Scope decisions (settled in consult)

- **Split T-015a / T-015b** to keep each half within the diff budget; this half
  has no HTTP surface.
- **argon2id in `packages/go/auth`** (`HashPassword`/`VerifyPassword`, PHC
  format, constant-time compare) — the crypto module is the cohesive home
  alongside PASETO.
- **Single bootstrap operator, env-provisioned** (`AIREOS_BOOTSTRAP_OPERATOR_USER`
  + `..._PASSWORD_HASH`), per ADR-0046 (no signup, no user CRUD). Package named
  `account` (ADR suggested `internal/auth/`; renamed to avoid confusion with the
  imported `packages/go/auth`).
- **`authctl` provisioning CLI** (`genkey`, `hash <password>`) wired as
  `task auth -- <cmd>`. A full `install.sh` (ADR-0046) is deferred to a devops
  ticket; `authctl` is the primitive it would call. `task auth rotate-key` lands
  with T-015b / devops.
- **Uniform auth timing**: `Authenticate` always verifies the password hash even
  on a username miss (no user enumeration via timing).

## Files in scope

- **Create:**
  - `packages/go/auth/password.go` — `HashPassword` / `VerifyPassword` (argon2id, PHC).
  - `packages/go/auth/password_test.go` — round-trip, wrong-password, salted, malformed.
  - `packages/go/auth/cmd/authctl/main.go` — `genkey` / `hash <password>`.
  - `services/api/internal/account/account.go` — bootstrap `Operator`, `FromEnv`, `Authenticate`.
  - `services/api/internal/account/account_test.go` — authenticate success / wrong pass / wrong user.
  - `docs/tickets/T-015a-auth-primitives-provisioning.md` — this ticket.
- **Modify:**
  - `Taskfile.yml` — `auth` target → `authctl` (replaces the placeholder comment).
  - `docs/tickets/README.md` — add the T-015a row.

## Acceptance criteria

- [ ] `HashPassword` returns a salted `$argon2id$v=19$...` PHC string; two hashes
      of the same password differ.
- [ ] `VerifyPassword` returns nil on match, `ErrPasswordMismatch` on mismatch,
      and an error on a malformed hash (constant-time compare).
- [ ] `account.Authenticate` returns `operator:<user>` on valid creds; rejects
      wrong password and wrong username.
- [ ] `task auth -- genkey` prints a 64-hex-char cluster key; `task auth -- hash
      <pw>` prints an argon2id PHC string.
- [ ] OpenAPI / schemas — **N/A** (HTTP surface is T-015b).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — done.
- [ ] Verification command runs clean (see below).

## Out of scope

- `/v1/auth/login` + `/v1/auth/me` handlers, Signer wiring, mixed public/gated
  routing, codegen — **T-015b**.
- A full `install.sh` script + `task auth rotate-key` — devops / T-015b.
- Multi-operator user store, signup, password reset, OAuth — forbidden at P0
  (ADR-0046; would trigger Zitadel).

## Diff budget

Split half of T-015. Actual: 6 created + 2 modified = 8 files, ≈260 LoC.

## ADRs required

None new. Implements ADR-0046 (argon2id bootstrap operator, in-process auth).

## Verification

Verified in a `golang:1.23` container:

```
go build ./packages/go/auth/... ./services/api/...      # clean
go vet   ./packages/go/auth/... ./services/api/...       # clean
go test  ./packages/go/auth/... ./services/api/internal/account/...   # pass
go run ./packages/go/auth/cmd/authctl genkey             # 64-hex key
go run ./packages/go/auth/cmd/authctl hash s3cret        # $argon2id$v=19$m=65536,t=3,p=2$...
```

All **pass**: password round-trip / mismatch / salted / malformed, account
authenticate (success + wrong pass + wrong user), and the authctl CLI.
