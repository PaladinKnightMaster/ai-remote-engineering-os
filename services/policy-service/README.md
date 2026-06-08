# policy-service

The approval/policy **decision point** (ADR-0054). Given an `(actor, edge, target,
action)` tuple it returns `allow` | `deny` | `requires_approval`, and brokers the
human approve/deny round-trip when the answer is `requires_approval` — every
decision audit-chained (ADR-0045/0049). This is the **approval** leg of the
CLAUDE.md doctrine "no AI autonomy before audit + policy + approval primitives
exist".

policy-service owns its tables (`pending_approvals` + `policy_outbox`) in the
shared cockpit DB (ADR-0048), the same own-tables pattern as connector-service.

**Status — v0.2.0 (in progress):**

- **T-031a-i (this slice):** service spine — the schema (`pending_approvals` +
  `policy_outbox`), the HTTP `serve` surface (ungated `/healthz`), and the
  in-process audit relay.
- **T-031a-ii:** the gated **internal** decide/resolve RPCs (`edge:
  service-service`, deliberately not in the shared OpenAPI fragment) + the audited
  store (tx-with-enqueue) + the ~120 s **fail-closed** timeout sweeper.
- **T-031b:** the `services/api` enforcement seam — consult policy at
  session-create; park a `requires_approval` session; operator approve/deny.
- **T-031c:** the `<ApprovalPrompt>` `packages/ui` component + `apps/web`
  round-trip (closes the v0.1.0 §FE-5 deferral).

The synchronous block-and-wait shape, same-operator self-approval, and the
~120 s fail-closed timeout are settled in ADR-0054 (segregation-of-duties + an
async approval queue are v0.3.0).

## CLI

```
policy-service serve                    # default; HTTP surface + in-process outbox relay
policy-service migrate up|version|force <v>
policy-service outbox relay             # run the outbox relay in the foreground
```

Needs `POLICY_DATABASE_URL` (or `DATABASE_URL`). `serve` uses `POLICY_ADDR`
(default `:8084`) and, for the audit relay, `AIREOS_AUDIT_URL` + `AIREOS_PASETO_KEY`
+ `AIREOS_CLUSTER_ID`.
