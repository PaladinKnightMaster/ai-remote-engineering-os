---
name: T-031b-i Ticket
about: Governed Egress Foundation — api enforcement seam (policy consult at session-create)
title: "T-031b-i: api consults policy at session-create + pending_approval status"
labels: ["ticket", "policy", "security", "api"]
---

## Ticket ID and link

T-031b-i — the **create-side** of the api enforcement seam (ADR-0054 §5/OQ-F:
`services/api` enforces at session-create, the broker stays a pure data plane).
First half of T-031b (b-ii adds the operator approve/deny resolution). Builds on
the policy-service backend (T-031a-i/ii). Mocked on the api side (httptest policy
client); the live api→policy→audit proof is **T-033/WD-2**.

## Goal

At `POST /v1/sessions`, if the target resource is labeled `requires_approval=true`,
park the session `pending_approval` and consult the policy decision point; any
failure to obtain a `requires_approval` decision fail-closes the session.

> CreateSession → `Get(resource)` → label set? → `ParkForApproval` (pending_approval)
> → `policy.Decide(session_id, subject, edge=agent-remote-session,
> target=resource:<id>, action=session.start, requires_approval=true)` →
> requires_approval → return parked 201 · decide error / no policy → close
> (policy_unavailable) + 502/503. Unflagged → unchanged ('pending', no policy call).

## Priority

P1 — v0.2.0 spine (the approval primitive's enforcement seam).

## Files in scope (13; +1 over budget, CEO-approved — 4 are generated/SQL boilerplate)

- Modify: `docs/api-contracts/openapi.v3.fragment.yaml` (Session.status +`pending_approval`; closedReason +`policy_unavailable`)
- Regenerate: `services/api/internal/server/gen.go` + `packages/shared-types/src/generated/openapi.ts` (`task codegen`)
- Create: `services/api/migrations/0005_session_pending_approval.{up,down}.sql` (extend `sessions_status_chk`)
- Modify: `services/api/internal/session/session.go` (`StatusPendingApproval`, `ReasonPolicyUnavailable`)
- Modify: `services/api/internal/store/sessions.go` (`insertSession` helper + `ParkForApproval` + `MarkClosed`; `CreateSession` signature unchanged)
- Create: `services/api/internal/policy/client.go` (api→policy `Decide` client; service-service)
- Modify: `services/api/internal/server/server.go` (Store iface + `policyDecider`; `New` +`policyURL`; CreateSession consult)
- Modify: `services/api/internal/server/server_test.go` (fake methods; `New` 6th arg; flagged-park / fail-closed tests via httptest policy)
- Modify: `services/api/main.go` (`POLICY_URL`)
- Create: `docs/tickets/T-031b-i-…md` (this) + `docs/tickets/README.md` status

## Design decisions (from the consult, CEO-approved)

- **Default A:** api reads its OWN resource's `requires_approval` label and consults policy ONLY for flagged resources; policy does no cross-read of `resources`. Unflagged creates are unchanged and not on policy's critical path.
- **Fail-closed ordering:** park the session FIRST (so the broker's status guard blocks it), then decide; on any failure close it (`policy_unavailable`) — never leave a flagged session anywhere the broker could open it.
- **Broker unchanged:** its guard rejects status ∉ {pending, active}, so a `pending_approval` session can't open a WS until api flips it to `pending` on approval (T-031b-ii). The enforcement is purely api-side.
- **`api.New`** gains a 6th arg `policyURL` (mints the service-service token inline via the existing signer — the `auditURL` precedent), not a Config refactor.
- **`CreateSession` store signature unchanged** — added `ParkForApproval`/`MarkClosed` instead (the existing integration test is undisturbed; shared `insertSession` keeps it DRY).

## Carry-forward (tracked, not in b-i)

- The operator approve/deny route + reconciliation (incl. policy-timeout-during-approve → close) is **T-031b-ii**.
- A `pending_approval` session whose operator never acts (policy auto-denies the approval at ~120s, but cannot write api's session row) is closed by the **T-032 session reaper** — T-032's scope now includes "reap stale `pending_approval`". Harmless meanwhile (the broker rejects them).

## G1 / G2

- **G1** — mocked on the api side (httptest policy). Live api→policy→audit = T-033/WD-2.
- **G2** — conforms ADR-0054 §5/OQ-F (api enforces, broker pure data plane); no divergence.

## Verification (Dockerized golang:1.23 + node for codegen + compose-mvp Postgres)

- `task codegen` then `git diff --exit-code` clean (VER-2 zero-drift) — regenerated gen.go + openapi.ts committed.
- api build / vet / golangci-lint; api unit (flagged→park+decide-tuple, decide-error→502+fail-closed, no-policy→503+fail-closed, unflagged→pending unchanged).
- `api migrate up` applies 0005 (`pending_approval` accepted by the CHECK).

## Definition of done

Enforcement-at-create wired + fail-closed + verified; contract + regen drift-clean.
Commit on `dev`, ff `main`. No push. T-031b-ii (operator resolution) follows.
