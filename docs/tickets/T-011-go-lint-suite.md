---
name: T-011 Go-side lint suite closing ADR-0044/0045/0046 P0 verification gaps + v0.1.0 SEC-8
about: Lint suite enforcing forward-only audit migrations, PASETO isolation, no http.DefaultServeMux, no panic in services, and ADR-0046 forbidden-features absence
title: "T-011: Go-side lint suite (ADR-0044/0045/0046 verification + v0.1.0 SEC-8)"
labels: ["ticket", "P1", "lint", "ci", "release-blocker"]
---

## Ticket ID and link

T-011 — see `docs/tickets/README.md`. Closes the P0 verification gaps in
ADR-0044, ADR-0045, ADR-0046 AND the **SEC-8 release-blocker** in
`docs/evaluation/v0.1.0-release-criteria.md`. Lead lens: **Tech Lead**;
cross-review: **Permission Model Reviewer** (PASETO isolation),
**Offline-Install Engineer** (no host toolchain — golangci-lint via
`go run`).

## Goal

Land five Go-side enforcement mechanisms that fail loud on commits
violating already-decided policy:

1. **PASETO isolation** — `aidanwoods.dev/go-paseto` is importable only
   from `packages/go/auth/...` (ADR-0046 / SEC-8). Enforced via
   golangci-lint `depguard`.
2. **No global HTTP mux** — `http.DefaultServeMux`, `http.Handle`,
   `http.HandleFunc` are forbidden everywhere (ADR-0043 / SEC-8). Enforced
   via golangci-lint `forbidigo`.
3. **No `panic()`** in production code (criteria SEC-8). Tests + tools
   exempt via `issues.exclude-rules`.
4. **Forward-only audit migrations** — destructive DDL (`DROP`,
   `TRUNCATE`, `ALTER … DROP`, `DELETE FROM`, `UPDATE`) against
   `audit_events` is rejected (ADR-0044 / ADR-0045 chain integrity).
   Custom Go tool: `tools/migrate-lint`.
5. **Forbidden-features absence** — no file with a name matching
   `password_reset*`, `signup*`, `oauth_*`, `jwks*` exists anywhere in
   the Go or TS source tree (ADR-0046 forbidden-features list). Custom
   Go tool: `tools/forbidden-features-check`.

## Priority

**P1 → release-blocker.** Filed at P1 before
`docs/evaluation/v0.1.0-release-criteria.md` was Accepted. That document
makes rules (1)-(3) **SEC-8 BLOCKERS** for v0.1.0. The original P1
label stays for ladder continuity; the effective binding is the criteria.

## Scope decisions (settled in consult / during implementation)

- **`http.DefaultServeMux` ban + `panic` ban added to scope** beyond the
  pre-existing ticket. Both are SEC-8 sub-bullets the v0.1.0 criteria
  added; pre-existing T-011 draft didn't cover them.
- **`golangci-lint` via `go run`**, not pre-installed. Taskfile + CI use
  `go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0`
  so no host toolchain install is required (Offline-Install Engineer
  lens; the `gomodcache` volume amortizes cost across CI runs).
- **forbidigo uses `p:` not `pattern:`**. Real bug discovered during
  T-011 verification — `pattern:` is silently ignored, making the rule
  fire on every identifier with the first entry's message. Quiet,
  dangerous misconfiguration. Comment in `.golangci.yaml` flags it for
  future contributors.
- **`unconvert` linter intentionally OFF.** It flags `int(*params.Limit)`
  in oapi-codegen-adjacent handlers where the conversion is meaningful
  for named-int aliases. Re-enable with per-file excludes when the
  handlers are refactored to drop the typed-alias indirection.
- **Migration-lint via regex, not full SQL parser.** golang-migrate
  migrations are operator-authored, not generated; adversarial input
  isn't the threat model. Atlas (held in reserve per ADR-0044) replaces
  this tool the day a structured-AST check becomes load-bearing.
- **Strengthened criteria interpretation**: "no panic in request-path
  code without recovery" → "no panic anywhere outside tests/tools."
  Static analysis can't reliably distinguish request-path code; stricter
  is enforceable and was already de-facto practice on T-007/T-015.
- **errcheck excludes for canonical Close-pattern idioms**: pgx
  `defer tx.Rollback(ctx)`, migrate `defer m.Close()`, websocket
  `defer c.CloseNow()`. All accepted Go style.
- **Pre-existing code-quality fixes made small, in-place**:
  - `services/api/internal/server/server.go` slidingRefresh:
    `_ = setSessionCookies(w, newToken)` makes the best-effort
    intentional ignore explicit.
  - `services/audit-service/internal/audit/event.go`: `recognised` →
    `recognized` (US locale per misspell).

## Files in scope

- **Create:**
  - `.golangci.yaml` — golangci-lint config with depguard (PASETO),
    forbidigo (http.DefaultServeMux / Handle / HandleFunc / panic),
    plus standard hygiene linters (errcheck, govet, staticcheck,
    gosimple, unused, ineffassign, gofmt, misspell).
  - `tools/migrate-lint/go.mod`
  - `tools/migrate-lint/main.go` — SQL regex scanner for destructive
    DDL on protected tables.
  - `tools/migrate-lint/main_test.go` — exit-code contract test
    (clean / violating / real-audit-migrations cases).
  - `tools/migrate-lint/testdata/clean/0001_init.up.sql`
  - `tools/migrate-lint/testdata/violating/9999_break_audit.down.sql`
  - `tools/forbidden-features-check/go.mod`
  - `tools/forbidden-features-check/main.go` — path-existence scanner
    for ADR-0046 forbidden-features filename patterns.
  - `tools/forbidden-features-check/main_test.go` — same three-case
    structure.
  - `tools/forbidden-features-check/testdata/clean/login.go`
  - `tools/forbidden-features-check/testdata/violating/password_reset_handler.go`
  - `tools/forbidden-features-check/testdata/violating/oauth_google.go`
- **Modify:**
  - `go.work` — adds `./tools/migrate-lint` and
    `./tools/forbidden-features-check`.
  - `Taskfile.yml` — `task lint` runs `golangci-lint run ./...` per
    module + the two custom tools.
  - `.github/workflows/ci.yml` — `lint` job extended to install pnpm
    + Node + Task, run `task lint` + the custom-tool self-tests.
  - `services/api/internal/server/server.go` — explicit ignore on
    sliding-refresh cookie write.
  - `services/audit-service/internal/audit/event.go` — `recognised`
    → `recognized`.
  - `docs/tickets/T-011-go-lint-suite.md` — this ticket (replaces
    pre-existing draft).
  - `docs/tickets/README.md` — T-011 marked complete.

## Acceptance criteria

- [x] `task lint` fails when any Go file outside `packages/go/auth/...`
      imports `aidanwoods.dev/go-paseto` (depguard).
- [x] `task lint` fails on any source-level reference to
      `http.DefaultServeMux`, `http.Handle`, or `http.HandleFunc`
      (forbidigo).
- [x] `task lint` fails on `panic(` calls in production code
      (forbidigo; tests + tools exempt).
- [x] `task lint` fails when `services/audit-service/migrations/`
      contains destructive DDL against `audit_events`. Verified by
      `tools/migrate-lint/testdata/violating/`.
- [x] `task lint` passes on the clean fixture AND on the real
      `services/audit-service/migrations/0001_init.up.sql`. Verified by
      `TestMigrateLint_RealRepoMigrations`.
- [x] `task lint` fails if any file matching `password_reset*`,
      `signup*`, `oauth_*`, `jwks*` exists in the Go or TS source tree.
      Verified by `TestForbiddenFeatures_ViolatingFixture`.
- [x] `task lint` passes on the real repo. Verified by
      `TestForbiddenFeatures_RealRepoRoot`.
- [x] `task lint` passes on `main` after this ticket lands (all 8 Go
      modules report clean).
- [x] OpenAPI / schemas — N/A.
- [x] Docs / ADR — N/A (ADR-0001..0047 untouched).
- [x] Unit tests cover valid + invalid + edge — see `_test.go` files
      under each tool.
- [x] Verification command runs clean (see below).

## Out of scope

- **ESLint + meta-framework dep ban (T-012)** — sibling SEC-7 ticket,
  already landed.
- **Atlas SQL linter adoption** — held in reserve per ADR-0044; needs
  a new ADR before adoption.
- **Per-token revocation lists** — ADR-0046 explicitly defers; not a
  lint.
- **Outbox-depth alerting thresholds** — ADR-0045 verification gap,
  owned by a metrics ticket.
- **Auto-fix / `--fix` modes** — lints are detection-only at P0.

## Diff budget

~17 authored files + 2 in-place code fixes + Taskfile/CI/ticket
modifications ≈ 700 LoC. **Exceeds the default 12-file cap; explicitly
approved** per the A→B→C→T-011→C strategy sequence accepted at
f2d82b9.

## ADRs required

None new. Closes ADR-0044 / ADR-0045 / ADR-0046 review-enforced
verification gaps and v0.1.0 release-criteria SEC-8 (release-blocker).

## Verification

Verified in `golang:1.23` on `--network docker-compose_default` with
the docker-compose Postgres running:

```
# Custom Go tools — self-tests (exit-code contract).
(cd tools/migrate-lint && go test ./...)                              # 3/3 pass
(cd tools/forbidden-features-check && go test ./...)                  # 3/3 pass

# Custom Go tools — real-repo runs.
go run ./tools/migrate-lint services/audit-service/migrations         # clean
go run ./tools/forbidden-features-check .                              # clean

# golangci-lint over every workspace module.
go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0
for mod in packages/go/auth packages/go/middleware packages/go/migrate \
           services/api services/audit-service services/session-broker \
           tools/migrate-lint tools/forbidden-features-check; do
  (cd "$mod" && golangci-lint run ./... --config $(pwd)/../../.golangci.yaml)
done                                                                  # all 8 clean

# Sanity — no regressions in the existing Go suite.
go test ./packages/go/... ./services/... ./tools/...                  # all pass
go test -tags integration ./services/audit-service/internal/store/... \
                          ./services/api/internal/store/...           # both pass
```

All green.

## Owner / cross-review

- **Owner:** Tech Lead.
- **Cross-review (required):** Permission Model Reviewer (PASETO
  isolation + forbidden-features), Offline-Install Engineer (no host
  toolchain).
- **Cross-review (optional):** Release Guard.

## Related

- ADR-0043 — chi-based router; the `http.DefaultServeMux` ban is the
  defense-in-depth on the "every service uses an explicit router"
  decision.
- ADR-0044 — forward-only `audit-service` migrations; `migrate-lint`
  IS the §Verification mechanism.
- ADR-0045 — outbox + sync HTTP ingest; chain integrity is what
  forward-only migrations protect.
- ADR-0046 — PASETO + forbidden-features; depguard +
  forbidden-features-check are the §Verification mechanisms.
- `docs/evaluation/v0.1.0-release-criteria.md` — SEC-8 (this ticket).
- T-012 — JS-side lint suite (sibling release-blocker, SEC-7) —
  already landed at d31c154.
