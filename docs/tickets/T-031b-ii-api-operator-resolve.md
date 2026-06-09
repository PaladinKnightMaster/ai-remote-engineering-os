---
name: T-031b-ii Ticket
about: Governed Egress Foundation — operator approve/deny resolution on services/api
title: "T-031b-ii: operator approve/deny resolution (POST /v1/sessions/{id}/resolve)"
labels: ["ticket", "policy", "security", "api"]
---

## Ticket ID and link

T-031b-ii — the **resolve-side** of the api enforcement seam (ADR-0054 §4/§5),
completing T-031b after b-i. The operator approves/denies a parked
`pending_approval` session; api consults policy-service (the decision authority)
and transitions its own session. Mocked on the api side (httptest policy); the
live api→policy→audit approve/deny proof is **T-033/WD-2**.

## Goal

> `POST /v1/sessions/{id}/resolve {decision: approve|deny}` (operator-scoped) →
> GetSession (404 / 409 if not pending_approval) → `policy.Resolve(id,
> granted|denied, operator)` → approve → `ApproveSession` (pending_approval→pending,
> broker may attach) → 200; deny → `MarkClosed(approval_denied)` → 200. policy 409
> (approval expired) → `MarkClosed(approval_timeout)` + 409; policy outage → 502
> (session stays parked); no policy configured → 503.

## Files in scope (10; in budget, no bump)

- Modify: `docs/api-contracts/openapi.v3.fragment.yaml` (route `resolveSession` + `SessionResolve` schema; closedReason +`approval_denied`/`approval_timeout`)
- Regenerate: `services/api/internal/server/gen.go` + `packages/shared-types/src/generated/openapi.ts`
- Modify: `services/api/internal/session/session.go` (`ReasonApprovalDenied`/`ReasonApprovalTimeout`)
- Modify: `services/api/internal/store/sessions.go` (`ApproveSession`; reuse `MarkClosed`)
- Modify: `services/api/internal/policy/client.go` (`Resolve` + `ErrNotPending` sentinel)
- Modify: `services/api/internal/server/server.go` (`ResolveSession` handler; `policyDecider`+`Resolve`, Store+`ApproveSession`)
- Modify: `services/api/internal/server/server_test.go` (fake `ApproveSession`; 7 resolve tests)
- Create: `docs/tickets/T-031b-ii-…md` (this) + `docs/tickets/README.md` status

**No migration** — `pending_approval` already in the 0005 CHECK; the new closed
reasons are free TEXT. **No services/session-broker change** — its status guard
already blocks `pending_approval` until api flips to `pending` on approval.

## Design decisions (consult, CEO-approved)

- api body uses **operator verbs** `approve|deny`; mapped to policy `granted|denied`.
- **The status pre-check disambiguates the 409-reconcile:** api only calls
  `policy.Resolve` while its session is still `pending_approval`, so a policy 409
  can only mean a policy-side timeout → reconcile to `approval_timeout`.
- **Fail-closed:** policy outage → 502, session stays parked (operator retries);
  never flip to `pending` without policy confirming.
- **Same-operator self-approve** (ADR-0054 §4): `resolved_by` = the GetSession-scoped owner.
- **Idempotency:** double-approve → 409 (clean), not a 500.

## G1 / G2

- **G1** — mocked on the api side (httptest policy: decisions + resolve). Live proof = T-033/WD-2.
- **G2** — conforms ADR-0054 §4/§5; broker untouched; no divergence.

## Verification (Dockerized golang:1.23 + node:22-alpine + compose-mvp Postgres)

`task codegen` regen + byte-determinism (VER-2 zero-drift); api build/vet/golangci-lint; api unit incl. the 7 resolve cases (approve→pending+decide tuple/bearer, deny→closed:approval_denied, policy-409→approval_timeout+409, double-approve→409, unknown→404, no-policy→503, CSRF-required); `migrate up` stays version 5 (no new migration); api store integration non-regressive.

## Definition of done

Resolve wired + fail-closed + reconcile + verified; contract + regen drift-clean.
Commit on `dev`, ff `main`. No push. Completes T-031b; next is T-031c (`<ApprovalPrompt>`).
