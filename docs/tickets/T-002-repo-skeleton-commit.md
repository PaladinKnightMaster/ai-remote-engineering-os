---
name: T-002 Repo skeleton commit (go.work, pnpm-workspace.yaml, .npmrc, Taskfile.yml, MVP compose)
about: Bootstrap scaffolding for ADR-0041..ADR-0047 — the workspace files every per-service / per-package ticket depends on
title: "T-002: Repo skeleton commit (go.work + pnpm-workspace + Taskfile + Postgres-only compose)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-002 — see `docs/tickets/README.md`. Executes the §Hand-off notes of
ADR-0042 (canonical driver), ADR-0043, ADR-0044, ADR-0045, ADR-0046,
ADR-0047. Activates the **Offline-Install Engineer** specialist per
their charter in `ai/roles/specialists/offline-install-engineer.md`.

## Goal

Land the workspace-level scaffolding (`go.work`, `pnpm-workspace.yaml`,
`.npmrc`, `Taskfile.yml`, and a runnable Postgres-only docker-compose)
that every subsequent per-service ticket (T-004..T-009), per-package
ticket (`packages/go/{auth,middleware,migrate,outbox}/`), and dormant
lint ticket (T-011, T-012) depends on.

## Priority

P0. Bootstrap; unblocks the entire downstream T-NNN ladder.

## Files in scope

- **Create:**
  - `go.work` — Go 1.23 workspace, minimal-valid form (no `use`
    directives yet); header comment lists future `use` paths and the
    ticket / ADR that activates each.
  - `pnpm-workspace.yaml` — `apps/*` + `packages/*` enumerated;
    `packages/go/*` explicitly excluded (those are Go modules).
  - `.npmrc` — restricted-network offline posture per ADR-0042
    §Decision + Offline-Install Engineer charter
    (`package-import-method=hardlink`, `prefer-offline=true`,
    `node-linker=isolated`, `strict-peer-dependencies=true`,
    `auto-install-peers=false`, `frozen-lockfile=true`,
    `engine-strict=true`, pinned registry).
  - `Taskfile.yml` — four canonical ADR-0042 entry points (`lint`,
    `test`, `build`, `offline-bundle`) stubbed to exit 0 with
    "not-yet-implemented" messages so dormant T-011 / T-012 extension
    hooks remain valid; placeholder comments name the follow-up
    targets (`codegen`, `migrate`, `auth rotate-key`, `outbox`).
  - `packages/go/.gitkeep` + `packages/go/README.md` — parent
    directory marker so future `go.work` `use` paths reference a real
    prefix; README names the four shared infra modules + their ADRs.
  - `docs/tickets/T-002-repo-skeleton-commit.md` — this ticket file.

- **Modify:**
  - `infra/docker-compose/docker-compose.mvp.yml` — replace the pre-ADR
    shape (Keycloak, Redis, MinIO, undefined `cockpit-api` /
    `cockpit-web` images) with a Postgres-only ADR-aligned MVP. Comment
    block at file end names every future service and the ADR /
    deep-dive that gates it.
  - `infra/docker-compose/env.example` — slim to Postgres-only vars
    matching the new compose; drop `REDIS_URL`, `OIDC_*`, `MINIO_*`,
    `SESSION_TOKEN_TTL_SECONDS`, `PAIRING_TTL_SECONDS`.
  - `docs/tickets/README.md` — mark T-002 *complete* with link to this
    ticket file; note the backlog-row drift on the title.
  - `.gitignore` — replace the "Language-specific ignores deferred"
    placeholder with Go + Node + Task ignores now that the toolchains
    are committed.

- **Test:** N/A. T-002 lands configuration; the Taskfile stubs print
  and exit 0. Per-service and lint tickets land the executable test
  surfaces.

## Acceptance criteria

- [x] `go.work` exists at the repo root and is a valid Go workspace
      file (`go env GOWORK` resolves to it; `go work sync` is a no-op
      because there are no `use` entries yet).
- [x] `pnpm-workspace.yaml` exists and enumerates `apps/*` +
      `packages/*` while excluding `packages/go/*`.
- [x] `.npmrc` exists with the offline-install posture listed under
      *Files in scope*.
- [x] `Taskfile.yml` exists with `lint`, `test`, `build`, and
      `offline-bundle` targets. Each prints a "not-yet-implemented"
      banner and exits 0. T-011 §Acceptance and T-012 §Acceptance
      "passes on `main` after this ticket lands" criterion holds.
- [x] `infra/docker-compose/docker-compose.mvp.yml` contains only a
      `postgres` service (no `redis`, no `keycloak`, no `minio`, no
      undefined `cockpit-*` images). The file ends with a comment
      block naming every future service and its gating ADR /
      deep-dive.
- [x] `infra/docker-compose/env.example` matches the new compose; no
      stale vars.
- [x] `packages/go/` exists with a `.gitkeep` and a README naming the
      four shared infra modules and the ADRs that introduce them.
- [x] `docs/tickets/T-002-repo-skeleton-commit.md` exists in the
      canonical ticket format.
- [x] `docs/tickets/README.md` shows T-002 as *complete* with a link.
- [x] `.gitignore` covers Go (`/bin/`, `*.test`, `*.test.exe`,
      `*.out`) and Node (`/node_modules/`, `dist/`, `.pnpm-store/`,
      `.vite/`) and Task (`.task/`).
- [x] OpenAPI / schemas updated if applicable — **N/A** (no contract
      changes; T-009 lands the OpenAPI fragment).
- [x] Docs / ADR updated if applicable — **N/A** per the no-edit-
      existing-ADRs rule (ADR-0001..ADR-0047 untouched).
- [x] Unit tests cover valid + invalid + edge cases — **N/A** (no
      executable surface; T-011 / T-012 land the lint test surfaces).
- [x] Verification command runs clean (see *Verification* below).

## Out of scope

- `packages/go/{auth,middleware,migrate,outbox}/` source — per-package /
  per-service tickets per ADR-0043..0046 hand-off notes.
- `services/*/main.go`, `apps/*/src/`,
  `packages/{ui,shared-types,config}/src/` — per-service / per-frontend
  tickets.
- `oapi-codegen.yaml`, `openapi-typescript` config — first service
  ticket (ADR-0043) and first frontend ticket (ADR-0047).
- `.golangci.yaml`, `eslint.config.ts` — T-011 / T-012.
- `.github/workflows/ci.yml` — T-010.
- `install.sh`, signing-key material, bootstrap operator argon2id
  provisioning — first auth-touching service ticket per ADR-0046
  §Hand-off.
- Caddy / Traefik reverse proxy, Guacamole, MeshCentral, MinIO —
  feature tickets that need them; reference deploy in
  `docs/deep-dive/09-offline-self-hosting-deployment.md`.
- Refreshing the "Stack TBD per ADR-0002/0003" line in the five
  `services/*/README.md` placeholders — per-service tickets do that
  when they land real code.

## Diff budget

≤12 files / ≤500 LoC. Actual: 11 files, ≈300 LoC.

## ADRs required

None new. Executes the §Hand-off notes of ADR-0041..ADR-0047 (all
already Accepted).

## Verification

POSIX shell:

```
test -f go.work
test -f pnpm-workspace.yaml
test -f .npmrc
test -f Taskfile.yml
test -f packages/go/README.md
grep -q "postgres:16" infra/docker-compose/docker-compose.mvp.yml
! grep -Eq "keycloak|redis:|minio|cockpit-api|cockpit-web" infra/docker-compose/docker-compose.mvp.yml
```

If `task` is on the PATH:

```
task lint && task test && task build && task offline-bundle
```

…all four exit 0 with "not-yet-implemented" banners (T-011 / T-012
extend them later; the per-service tickets extend the rest).

If the Go toolchain is on the PATH:

```
go env GOWORK   # prints absolute path to go.work
go work sync    # no-op (no `use` entries yet)
```

## Notes on backlog-row drift

The B-NNN backlog row text in `docs/requirements/backlog_v3.csv` and
the §Reference block in `.github/ISSUE_TEMPLATE/ticket.md` both still
read *"Postgres + Redis/NATS + API + web"* for T-002. That text
predates ADR-0045 (no broker at P0) and ADR-0047 (no Node runtime in
production). The actual T-002 compose is **Postgres only**; placeholder
slots for `services/api` etc. are documented but not yet runnable.

`backlog_v3.csv` is not edited per the no-edit-frozen-CSVs rule. The
`.github/ISSUE_TEMPLATE/ticket.md` §Reference block is flagged for a
separate hygiene ticket and intentionally untouched here.
