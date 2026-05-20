---
name: T-009 OpenAPI contract scaffold (shared components, security scheme, error/pagination envelopes)
about: Promote openapi.v3.fragment.yaml into the canonical contract scaffold every service / frontend ticket generates from
title: "T-009: OpenAPI contract scaffold (shared components + security + envelopes)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-009 — see `docs/tickets/README.md`. Consumes the T-003 domain
schemas; prerequisite for the codegen wiring in the first service ticket
(T-004, per ADR-0043 §Hand-off) and the first frontend ticket (T-007,
per ADR-0047). Activates **Tech Lead** (contract shape / codegen
boundary), **Frontend Lead** (TS-client consumability), and the
**Offline-Install Engineer** (tooling supply chain) lenses.

## Goal

Promote `docs/api-contracts/openapi.v3.fragment.yaml` from a paths-only
skeleton into the canonical P0 **contract scaffold**: a security scheme,
the P0 domain components (mirrored from the authoritative JSON Schemas in
`packages/shared-schemas/`), and the cross-cutting `Error` / pagination
envelopes that every per-service ticket references. No service paths and
no code generation land here — those belong to the consumer tickets.

## Priority

P0. Bootstrap; the shared contract surface T-004..T-007 generate from.

## Scope decisions (settled in consult)

- **Contract only, no codegen** (ADR-0043 §Hand-off assigns `oapi-codegen`
  wiring to the first service ticket; ADR-0047 assigns `openapi-typescript`
  to the first frontend ticket). T-009 touches neither `task codegen`,
  `oapi-codegen.yaml`, nor `packages/shared-types/`.
- **No P0 service paths.** Each service ticket contributes its own paths
  referencing these shared components — audit ingest **and** read → T-004
  (the append-only chain semantics are inseparable from its API surface);
  resource CRUD → T-005; session lifecycle + control + WS → T-006. This
  keeps one consistent boundary (no "declare half an API" asymmetry).
- **Inline-mirror, not external `$ref`.** The three P0 entities are
  mirrored into `components/schemas` so `oapi-codegen` /
  `openapi-typescript` resolve them without uneven external-file `$ref`
  support. The JSON Schemas in `packages/shared-schemas/` remain the
  human source-of-record; de-dup via a bundling step is a later
  improvement.
- **P0 surface only.** Resource, Session, AuditEvent + `Error` +
  `PageMeta` / pagination params. The other 20 schemas are not pulled in.
- **Validation deferred to T-010.** No validator, no first npm
  devDependency, no root `package.json` here — T-010 (CI checks + schema
  validation) owns that and the lockfile precedent together.

## Files in scope

- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — add `servers`,
    `security`, and `components` (`securitySchemes.bearerAuth` per
    ADR-0046; `schemas` Resource / Session / AuditEvent / Error /
    PageMeta; `parameters` LimitParam / CursorParam; `responses`
    BadRequest / Unauthorized / Forbidden / NotFound). Existing P1 path
    stubs left untouched. Header documents the `operationId` convention.
  - `docs/tickets/README.md` — mark T-009 *complete* with a link.
  - `docs/tickets/T-009-openapi-contract.md` — this ticket file.

- **Test:** none here. Contract + schema validation is wired by T-010.

## Field provenance

`Resource` and `Session` mirror `docs/api-contracts/openapi/openapi.yaml`
`components/schemas` (verbatim). `AuditEvent` mirrors
`packages/shared-schemas/audit-event.schema.json` (T-003), including
`additionalProperties: false`, the seven capability edges + `service-service`
(ADR-0046), and the chain-authority-assigned `prevHash` / `chainHash` /
`idempotencyKey` (ADR-0045). `bearerAuth` is the PASETO v4 capability
token from ADR-0046 (kept generic — no `bearerFormat: JWT`).

## Acceptance criteria

- [ ] `openapi.v3.fragment.yaml` parses as valid OpenAPI 3.1.0.
- [ ] `components/schemas` defines Resource, Session, AuditEvent, Error,
      and PageMeta; the P0 entity mirrors match their
      `packages/shared-schemas/*.schema.json` source field-for-field.
- [ ] `components/securitySchemes.bearerAuth` exists and a top-level
      `security` applies it; no `bearerFormat: JWT` (token is opaque,
      ADR-0046).
- [ ] Shared `responses` (BadRequest/Unauthorized/Forbidden/NotFound)
      and pagination `parameters` (LimitParam/CursorParam) exist for
      service tickets to `$ref`.
- [ ] No `paths` for P0 services are added; the existing P1 stubs are
      unchanged.
- [ ] No codegen artifacts: `task codegen`, `oapi-codegen.yaml`,
      `packages/shared-types/` are untouched / absent.
- [ ] All internal `$ref`s resolve to defined components.
- [ ] OpenAPI / schemas updated if applicable — **done** (contract).
- [ ] Docs / ADR updated if applicable — **N/A** (ADR-0001..ADR-0047
      untouched).
- [ ] Unit tests cover valid + invalid + edge cases — **N/A** here;
      contract + schema validation lands in T-010.
- [ ] Verification command runs clean (see *Verification*).

## Out of scope

- `task codegen`, `oapi-codegen.yaml`, generated Go chi-server stubs —
  first service ticket (T-004) per ADR-0043 §Hand-off.
- `openapi-typescript` config, `packages/shared-types/`, the
  `openapi-fetch` client wrapper + 401 chokepoint — first frontend
  ticket (T-007) per ADR-0047.
- All P0 service `paths` and their request/response bodies — the owning
  service tickets (T-004 audit, T-005 resources, T-006 sessions),
  including the `POST /v1/events` ingest DTO whose chain semantics are
  T-004's (boundary set in T-003).
- A JSON-Schema / OpenAPI validator + CI step + the first npm
  devDependency / root `package.json` — **T-010**.
- Reconciling / retiring the legacy `docs/api-contracts/openapi/openapi.yaml`
  and `openapi.v2.yaml` (field source only) — a doc-hygiene ticket.
- Resolving the `Session` vs `RemoteSession` naming (deferred to whichever
  ticket assembles `components/schemas` + wires `task codegen`); the
  component is named `Session` here to match the entity vocabulary, but
  the generated-symbol decision still rides with the codegen ticket.

## Diff budget

≤12 files / ≤500 LoC. Actual: 3 files, ≈150 LoC.

## ADRs required

None new. Consumes ADR-0041 (codegen-not-runtime), ADR-0043 (oapi-codegen
boundary), ADR-0046 (PASETO bearer), ADR-0047 (TS client boundary). All
Accepted.

## Verification

POSIX shell with a YAML/OpenAPI parser available:

```
python -c "import yaml,sys; d=yaml.safe_load(open('docs/api-contracts/openapi.v3.fragment.yaml')); \
assert d['openapi']=='3.1.0'; \
assert set(['Resource','Session','AuditEvent','Error','PageMeta']) <= set(d['components']['schemas']); \
assert 'bearerAuth' in d['components']['securitySchemes']; \
print('ok')"
```

Full OpenAPI lint (`redocly lint` / `spectral lint`) and the JSON-Schema
fixture validation run under T-010 once its validator lands.
