---
name: T-031a-ii Ticket
about: Governed Egress Foundation — policy-service decide/resolve RPCs + audited store + fail-closed sweeper
title: "T-031a-ii: policy decide/resolve RPCs + audited store + timeout sweeper"
labels: ["ticket", "policy", "security"]
---

## Ticket ID and link

T-031a-ii — second slice of the policy-service backend (ADR-0054), completing the
T-031a backend. It adds the decision logic on top of the T-031a-i spine, drawn
along the connector **T-029b-ii** line (gated RPC + audited store ship together).
Conforms ADR-0054 §1 (typed decision) / §3 (audit-chained) / §6 (own outbox,
fail-closed ~120s timeout) + ADR-0045/0049.

## Goal

Give policy-service its teeth: the gated decide/resolve RPCs, the audited
approval-lifecycle store (each mutation tx-with-enqueue), and the fail-closed
timeout sweeper.

## Priority

P1 — v0.2.0 spine (the approval primitive).

## Files in scope (8)

- Create: `services/policy-service/internal/store/store.go` + `store_integration_test.go`
- Modify: `services/policy-service/internal/server/server.go` (+`server_test.go`) — `New(Config{Pool,Verifier,TTL})` + decide/resolve handlers
- Modify: `services/policy-service/main.go` — capability verifier, sweeper goroutine, pass `Config`
- Modify: `services/policy-service/go.mod` (middleware/uuid → direct, regenerated)
- Create: `docs/tickets/T-031a-ii-policy-decide-resolve-sweeper.md` (this file) + `docs/tickets/README.md` status

## Store (tx-with-enqueue, mirrors connector `store.Lease`)

- `CreatePending(in, ttl)` — INSERT pending (`expires_at=now+ttl`) + enqueue `policy.decision:requires_approval`; idempotent on `session_id` (duplicate decide → existing row, no 2nd event).
- `Resolve(sessionID, decision, resolvedBy)` — UPDATE `WHERE status='pending'` → granted/denied + enqueue `approval.granted` / `approval.denied:operator`; `ErrNotPending` guards double-resolve / expired.
- `ExpireTimedOut(now)` — one tx: UPDATE all expired-pending → denied/timeout (RETURNING) + one `approval.denied:timeout` per row; returns count. Rows drained+closed before the enqueues (single-conn tx).
- `Get(sessionID)`.

## RPCs (INTERNAL, gated `service-service`, audience `policy-service`; NOT in the OpenAPI fragment — connector-lease posture)

- `POST /v1/decisions` `{session_id,subject,edge,target,action,requires_approval}` → `{decision, approval?:{id,expires_at}}`. `requires_approval` → park + `requires_approval`; else `allow` (no row, no audit at v0.2.0 — api consults policy only for flagged resources; allow-audit + rules = v0.3.0).
- `POST /v1/approvals/{session_id}/resolve` `{decision:granted|denied, resolved_by}` → `{status,reason}`; 409 if no still-pending approval.

## Audit outcomes (ADR-0054 §3; outcome carries kind+reason)

`policy.decision:requires_approval` · `approval.granted` · `approval.denied:operator` · `approval.denied:timeout`. All carry `target=resource:<id>`, `evidencePacketId=session:<id>`. Actor = requester (decide) / `resolved_by` (operator resolve) / `system:policy-timeout` (sweeper).

## Sweeper

A `serve()` goroutine ticking every **10s**, calling `ExpireTimedOut(now)`. TTL default **120s** (ADR-0054 §6), overridable via `POLICY_APPROVAL_TTL_SECONDS`. Fail-closed: an un-actioned approval becomes `approval.denied:timeout`.

## Design calls (from the consult, CEO-approved)

- Decide idempotent on `session_id`; resolve guards `status='pending'` (→ 409).
- Timeout denial actor `system:policy-timeout`, edge = the stored gated edge; operator denial actor = `resolved_by`.
- Decide returns `allow` for unflagged input (real + unit-tested, not dead); `deny` not produced at v0.2.0 (no deny rules until v0.3.0).
- TTL 120s / tick 10s.

## G1 / G2

- **G1** — single-service: store integration (create→resolve→expire, asserting `policy_outbox` outcomes) + server unit (gate 401 / bad-body 400 / allow path, no DB). Live api→policy→audit decision/approval proof = **T-033 / WD-2**.
- **G2** — no divergence; ADR-0054 §1/§3/§6. Internal service-service RPC posture matches the connector precedent; operator-facing approve/deny stays on api (§5).

## Verification (Dockerized golang:1.23 + compose-mvp Postgres)

build / vet / golangci-lint v1.61.0; server unit; `migrate up` then store integration (`-tags integration`, `POLICY_TEST_DATABASE_URL`) — create/resolve/expire flows + outbox outcomes green.

## Definition of done

Decide/resolve/expire audited + verified; sweeper wired; gate enforced. Commit on
`dev`, ff `main`. No push. T-031b (api enforcement seam) follows.
