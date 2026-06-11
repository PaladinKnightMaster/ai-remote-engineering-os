---
name: T-033a Ticket
about: Governed Egress Foundation — WD-2 live approval e2e (compose leg, G1 capstone)
title: "T-033a: WD-2 live approval e2e — policy decide/resolve + approval round-trip on real wires"
labels: ["ticket", "test", "security"]
---

## Ticket ID and link

T-033a — first slice of **T-033** (the v0.2.0 release capstone), split a/b/c at
the 2026-06-11 war-room consult (b = OI-6 offline re-point + installed WD-2;
c = VER-5 Taskfile legs + DOC-7..10 + ADR-0055 + `docs/releases/v0.2.0/`).
The **G1 capstone**: the live proof of every boundary mocked during T-031 —
api↔policy `Decide`/`Resolve` (T-031b stubbed policy with httptest) and the
policy outbox→audit relay — stacked on the T-030b live connector lease.
Conforms ADR-0054 (api enforces at create; sync block-and-wait; same-operator
self-approve at v0.2.0) + ADR-0053/0045/0046.

## Goal

> login → create a session against a **requires_approval-flagged** resource →
> parked `pending_approval` (policy consulted) → operator **approve** via
> `POST /v1/sessions/{id}/resolve` → broker WS → type a command → read PTY
> output → end → assert the LIVE chain: `policy.decision:requires_approval` +
> `approval.granted` + `credential.leased` + `session.open` +
> `terminal.input.line` + `session.close` (cast SHA). **Deny leg** (WD-2.3's
> other half): a second parked session is denied → `approval.denied:operator`
> chains, the broker refuses the WS attach, no `session.open` ever appears —
> deny actually **blocks**. No mocked boundary.

## Priority

P1 — WD-2 is the single load-bearing v0.2.0 criterion; this is its headless compose form (VER-6).

## Files in scope (6)

- Modify: `infra/docker-compose/docker-compose.e2e.yml` — `policy` service
  (real PASETO, `:8084`, audit relay); api `POLICY_URL`; seed runs the policy
  migration + a second `set-secret` (flagged resource); tester depends on policy.
- Modify: `tests/e2e/cmd/seed/main.go` — second resource row: same sshd coords
  + `requires_approval:"true"` (only the gate differs between the two tests).
- Modify: `tests/e2e/harness_test.go` — `flaggedResourceID`; `createSessionFor`
  (status-parameterized; `createSession` delegates); `resolveSession`;
  `pollEvidenceOutcome` (policy events anchor evidence `session:<id>`).
- Create: `tests/e2e/approval_test.go` — `TestGovernedEgressApprovalEndToEnd`.
- Create: this file + `docs/tickets/README.md` split + status flip.

## G2

No divergence: Accepted ADRs + the criteria doc untouched. The WD-2.5 rider
divergence (T-032's deferrals) is papered by ADR-0055 in T-033c, not here.
The broker stays keyless and approval-blind — the deny leg's refused attach
proves the control-plane park alone blocks the data plane (ADR-0054 §5).

## Verification

`MSYS_NO_PATHCONV=1 task e2e` green with BOTH `TestSSHCockpitEndToEnd`
(ungated path, unchanged) and `TestGovernedEgressApprovalEndToEnd`; `gofmt -l`
clean + `go vet -tags e2e|wd1 ./tests/e2e/...` clean in `golang:1.23`.
De-risk pre-run for T-033c (report-only): golangci-lint v1.61.0 + go-licenses
over `services/policy-service` — both CLEAN.

## Definition of done

Six WD-2 events live-chained for the approved session; deny blocked + chained;
`task e2e` green; ticket + README updated. Commit `dev`, ff `main`. No push.
