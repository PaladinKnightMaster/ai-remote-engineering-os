---
name: T-020b Ticket
about: SSH e2e hardening (2/2) — live docker-compose end-to-end harness
title: "T-020b: live SSH cockpit e2e harness + audit-chain proof (v0.1.0)"
labels: ["ticket", "test", "bug"]
---

## Ticket ID and link

T-020b — second half of T-020 (SSH e2e hardening). No B-NNN counterpart. The
live cross-boundary proof for the SSH pillar; the G1 gate of
`ai/war-room/activation-matrix.overlay.md`. Pairs with **T-020a** (the
close-coordination/reconnect fixes) and closes **ADR-0045 §Verification item 3**
(the live producer→audit-chain proof the slice still owed). Surfaced + fixed one
release-blocking auth bug, ratified in **ADR-0050**.

## Goal

Stand up the full cockpit path on real wires — Postgres + `services/api` +
`services/session-broker` + `services/audit-service` + a real openssh target —
and run a live walkthrough that asserts the audit chain end to end with NO mock
or fake at any boundary:

> login (cookie) → create session (api) → open broker WS (cookie auth) → type a
> command → read its output back over the live PTY → end → assert the LIVE chain
> (via `audit-service GET /v1/events`, delivered through the REAL outbox relay)
> has `session.open` + `terminal.input.line` + `session.close` with the cast SHA
> anchored in the close event; verify the on-disk cast file's SHA256 matches.

## Priority

P0 — release-gating. Closes the last live-verification gap before §OI / §11a.

## Files in scope

- Create: `infra/docker-compose/docker-compose.e2e.yml` (postgres+api+broker+audit+sshd+seed+tester)
- Create: `infra/docker-compose/e2e/Dockerfile.sshd` (alpine openssh test target)
- Create: `infra/docker-compose/e2e/sshd-entrypoint.sh` (runtime key install + sshd)
- Create: `tests/e2e/go.mod`, `tests/e2e/go.sum` (new workspace module)
- Create: `tests/e2e/cmd/seed/main.go` (runtime SSH keygen + resource seed)
- Create: `tests/e2e/e2e_test.go` (`TestSSHCockpitEndToEnd`, tag `e2e`)
- Modify: `go.work` (use `./tests/e2e`)
- Modify: `Taskfile.yml` (`task e2e`)
- Modify: `services/session-broker/main.go` (WS verifier audience fix; ADR-0050 §2)
- Create: `docs/adr/ADR-0050-cockpit-ws-data-plane-contract.md`
- Create: `docs/tickets/T-020b-live-e2e-harness.md` (this file)
- Modify: `docs/tickets/README.md`

13 files. Over the ≤12 soft ceiling by one (the conventional README status flip);
the 12th substantive file is the ADR-0050 broker fix the live proof forced — see
Diff budget.

## What the harness proves (and how it stays real, per G1)

- **Real PASETO across the cluster** (one `AIREOS_PASETO_KEY`), NOT
  `*_DEV_INSECURE_AUTH` — an allow-all verifier would be a mock boundary, not a
  proof. A **separate** `AIREOS_RESOURCE_SECRET_KEY` unwraps the pgcrypto target
  credential (authority separation, ADR-0048 §7).
- **A real sshd** (alpine openssh) stands in for an operator-provisioned target
  (ADR-0048 §7); the broker dials it with the pinned host key + the decrypted
  client key. The command's output is read back over the PTY — the SSH
  round-trip is exercised, not stubbed.
- **The real outbox relay** delivers the chain events; assertions read them via
  `audit-service GET /v1/events` (stronger than a DB peek; conforms ADR-0045).
- **Programmatic assertions** (closes the spike's documented gap #5): the
  `terminal.input.line` event's `evidencePacketId` must equal
  `sha256:`+`sha256("echo <marker>")`, and the on-disk cast SHA must equal the
  `session.close` anchor.

## Secrets / keys provisioning (no secrets in git)

A seed one-shot (`tests/e2e/cmd/seed`) generates the SSH host + client ed25519
keypairs at runtime, writes the host key + the authorized client key to a shared
volume for the sshd, and inserts the ssh resource (labels `ssh_host/ssh_port/
ssh_user/ssh_host_key` + pgp_sym_encrypt'd `{private_key}`) mirroring the
`store_integration_test.go` seed. It is idempotent (skips if keys exist) so a
re-run against a live stack cannot desync the sshd's loaded host key from the
DB's pinned key. The only baked secret is a throwaway operator argon2id hash
(password `e2e-operator-pw`).

## Bug found + fixed (the e2e gate doing its job)

**Release-blocker: the cockpit WS 401'd every operator under real PASETO.** The
operator cookie carries audience `api` (the only operator-token mint), but the
broker's WS verifier required audience `session-broker` — unsatisfiable by any
operator token. Masked entirely by `BROKER_DEV_INSECURE_AUTH=1` in every prior
broker test; the first real-PASETO upgrade (this harness) returned
`401 invalid capability token`. **Fix:** broker verifies audience `api` (the
operator's home audience, which ADR-0048 §4 says flows through unchanged).
Ratified in **ADR-0050 §2**. (ADR-0050 §1 also reconciles the shipped WS path
`/v1/sessions/{id}/channel` with ADR-0048 §1's `…/stream`.)

## Acceptance criteria

- [x] `task e2e` (cold `down -v` → seed → run → down) brings up the full cluster
      and `TestSSHCockpitEndToEnd` passes.
- [x] WS upgrade succeeds with the operator `aireos_session` cookie at
      `/v1/sessions/{id}/channel` under real PASETO (regression guard for the
      ADR-0050 §2 fix — failed with 401 before).
- [x] Live `GET /v1/events` shows exactly one `session.open` (`opened`), one
      `terminal.input.line` (`input.line`, `evidencePacketId = sha256:<line>`),
      and one `session.close` (`closed:operator`) anchoring the cast SHA.
- [x] The on-disk `<id>.cast` SHA256 equals the chain-anchored SHA.
- [x] `go test ./services/session-broker/...` green after the audience change.

### Violating vs. passing fixtures

- **WS auth — violating:** broker verifier audience `session-broker` → every
  operator upgrade 401s (`invalid capability token`). **Passing:** audience
  `api` → upgrade succeeds, chain completes.
- **Seed re-run — violating:** regenerating keys on a warm stack → host key
  mismatch on dial. **Passing:** idempotent seed reuses the volume keys.

## Out of scope

- Offline install of the harness images / xterm pnpm deps → **§OI**.
- Real per-service container images (the harness runs services via `go run`; the
  single-static-binary deploy artifact is ADR-0041 / §OI, not this test rig).
- Adding `tests/e2e` to the `task lint` module loop (test-only module; gofmt+vet
  clean) — left out to bound scope.

## Diff budget

≤12 files / ≤500 LoC. **This ticket exceeds both** — 13 files (12 substantive +
the README status flip) and ~590 authored LoC (excl. generated `go.sum` + the
two docs). Drivers: the live debugging forced an approved cross-service auth fix
(`services/session-broker/main.go` + ADR-0050) plus diagnostics (401-body
surfacing) and seed idempotency that the harness genuinely needs. Flagged to the
CEO; accepted as a single coherent proof rather than a retroactive a/b split of
green, cohesive work.

## ADRs required

- **ADR-0050** (Accepted) — cockpit WS data-plane contract: the `/channel` path
  (refines ADR-0048 §1) + operator-`api`-audience cookie auth (refines ADR-0048
  §4 / ADR-0046), both surfaced by this live proof.

## Verification

```sh
# From infra/docker-compose; on a Windows host prefix with MSYS_NO_PATHCONV=1.
task e2e
# or:
docker compose -f docker-compose.e2e.yml build
docker compose -f docker-compose.e2e.yml run --rm tester
docker compose -f docker-compose.e2e.yml down -v
```

Expected: `--- PASS: TestSSHCockpitEndToEnd` with log lines for login, session
create, command execution (marker observed in PTY output), and the chain +
cast-SHA assertion. (Confirmed 2026-05-31, cold run, Docker / golang:1.23 +
alpine:3.20 + postgres:16.)
