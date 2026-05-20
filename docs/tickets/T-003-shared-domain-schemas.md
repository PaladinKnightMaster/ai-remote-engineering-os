---
name: T-003 Shared domain types/schemas (Organization, User, Resource, Session, AuditEvent)
about: The five core-entity JSON Schemas every per-service ticket (T-004..T-009) consumes
title: "T-003: Shared domain schemas (Organization, User, Resource, Session, AuditEvent)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-003 — see `docs/tickets/README.md`. Next bootstrap-ladder ticket after
T-002; prerequisite for T-004..T-009. Activates the **Audit / Evidence
Reviewer** (AuditEvent) and **Permission Model Reviewer** (User /
Session / AuditEvent edge typing) specialists per their charters in
`ai/roles/specialists/`.

## Goal

Land the five core-entity domain schemas as JSON Schema files under
`packages/shared-schemas/`, matching the conventions of the existing 18
files, so the per-service tickets (T-004..T-009) and the OpenAPI-contract
ticket (T-009) have a single authoritative field source per ADR-0041
("types flow through OpenAPI + JSON-schema codegen, not a shared
runtime").

## Priority

P0. Bootstrap; unblocks T-004..T-009.

## Files in scope

- **Create:**
  - `packages/shared-schemas/organization.schema.json`
  - `packages/shared-schemas/user.schema.json`
  - `packages/shared-schemas/resource.schema.json`
  - `packages/shared-schemas/session.schema.json`
  - `packages/shared-schemas/audit-event.schema.json`
  - `packages/shared-schemas/test/fixtures/valid.json` — one passing
    instance per schema, keyed by schema title.
  - `packages/shared-schemas/test/fixtures/invalid.json` — one failing
    instance per schema (one violated rule each; see *Verification*).
  - `docs/tickets/T-003-shared-domain-schemas.md` — this ticket file.

- **Modify:**
  - `docs/tickets/README.md` — mark T-003 *complete* with a link.
  - `packages/shared-schemas/README.md` — replace the stale
    "populated by Task 9" line with the current state.

- **Test:** the two fixture files above. No validator is wired in this
  repo yet (that is T-010); the fixtures are pure instances a T-010
  validator iterates. Manually confirmed against draft 2020-12.

## Field provenance (no invented fields)

| Schema | Source |
|---|---|
| Organization | `docs/deep-dive/11-data-model.md` ("tenant boundary"); kept to `id`/`name`/`createdAt` per ADR-0046 single-operator posture |
| User | data-model ("Human account, local auth or external IdP") + ER `Organization ⟶ User` + ADR-0046; `authMode` enum from the data-model wording |
| Resource | `docs/api-contracts/openapi/openapi.yaml` `components/schemas/Resource` (verbatim fields + `type` enum) |
| Session | `openapi.yaml` `components/schemas/Session`; `status` enum = data-model `RemoteSessionStatus` |
| AuditEvent | `ai/roles/specialists/audit-evidence-reviewer.md` required-field list (timestamp, actor, edge, target, outcome, evidence-ref) + ADR-0045 chained-hash / idempotency-key |

## Acceptance criteria

- [ ] Five `*.schema.json` files exist under `packages/shared-schemas/`,
      each draft 2020-12, PascalCase `title`, camelCase properties,
      matching the existing 18 files' style.
- [ ] No hand-written Go structs or TS types are added (ADR-0041).
- [ ] No edits to `docs/api-contracts/openapi*` — the OpenAPI contract
      assembly and codegen wiring stay with T-009.
- [ ] `audit-event.schema.json` sets `additionalProperties: false`,
      constrains `edge` to the seven capability edges plus
      `service-service` (ADR-0046), and documents `prevHash` / `chainHash`
      / `idempotencyKey` as chain-authority-assigned (ADR-0045) — not
      producer-supplied. The ingest DTO producers POST is T-004's surface.
- [ ] `valid.json` validates against every schema; `invalid.json` fails
      against every schema.
- [ ] OpenAPI / schemas updated if applicable — schemas: **done**;
      OpenAPI: **N/A** (T-009).
- [ ] Docs / ADR updated if applicable — **N/A** per the no-edit-
      existing-ADRs rule (ADR-0001..ADR-0047 untouched).
- [ ] Unit tests cover valid + invalid + edge cases — fixtures land the
      valid + invalid surfaces; the executable validator is T-010.
- [ ] Verification commands run clean (see *Verification*).

## Out of scope

- OpenAPI `components/schemas` assembly + `task codegen` wiring +
  generated Go/TS types — **T-009**.
- A JSON-Schema validation runner / CI step — **T-010**.
- `services/*` and `packages/go/*` source that consumes these types —
  per-service tickets T-004..T-009.
- The AuditEvent **ingest** payload shape (`POST /v1/events`) and the
  chained-hash extension logic — **T-004** (audit-service), per ADR-0045.
- Redaction-tombstone overlay semantics — audit-service (T-004); not a
  shared-type concern.
- Renaming `Session` → `RemoteSession`, or reconciling the data-model ER
  diagram's `Device ⟶ RemoteSession` naming against the
  `Resource ⟶ Session` naming used here and in `openapi.yaml`. This is
  **not a standalone hygiene ticket** — the deciding constraint is the
  generated Go/TS symbol name (a bare `Session` is ambiguous next to
  `PairingSession`), which only becomes load-bearing when the OpenAPI
  contract + codegen land. **Whichever ticket assembles the OpenAPI
  `components/schemas` and wires `task codegen` (currently T-009) must
  resolve the `Session`/`RemoteSession` name and reconcile the ER-diagram
  prose at that point.** The `docs/deep-dive/11-data-model.md` diagram is
  historical (per `CANONICAL_SOURCE_OF_TRUTH.md`) and is not edited here.

## Diff budget

≤12 files / ≤500 LoC. Actual: 10 files, ≈200 LoC.

## ADRs required

None new. Consumes ADR-0041 (codegen-not-runtime), ADR-0045 (audit
chain), ADR-0046 (auth/org posture, edge set). All already Accepted.

## Verification

POSIX shell — files exist and are valid JSON:

```
for f in organization user resource session audit-event; do
  test -f "packages/shared-schemas/$f.schema.json"
done
test -f packages/shared-schemas/test/fixtures/valid.json
test -f packages/shared-schemas/test/fixtures/invalid.json
```

When T-010's validator lands (e.g. `check-jsonschema` / `ajv`), each
`valid.json` entry validates and each `invalid.json` entry fails. The
violated rule per invalid fixture:

- Organization — missing required `name`.
- User — `authMode` not in {`local`, `external_idp`}.
- Resource — `type` not in the enum (`telnet`).
- Session — `status` not in `RemoteSessionStatus` (`paused`).
- AuditEvent — unknown property (`forgedChainHash`) rejected by
  `additionalProperties: false`.

## Notes on the Organization / multi-tenancy tension

ADR-0046 commits P0 to a single bootstrap operator (no multi-tenant
SaaS). The data model nonetheless makes Organization the tenant root and
every API requires org context. Resolution: the schema exists (it is
cheap, forward-compatible, and already referenced by `User.organizationId`
and `memory-scope.schema.json` `scopeType: org`) but is modelled as a
bare `id`/`name` placeholder — no billing, plan-tier, or member-management
fields that would imply the multi-tenancy ADR-0046 rules out. At P0 it is
a single-row entity.
