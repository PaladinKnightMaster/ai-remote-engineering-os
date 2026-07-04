# AI Remote Engineering OS — v0.6.0 release notes

**Status: released** — local annotated tag `v0.6.0` (no remote, per repo doctrine);
the CEO §human WD-6 walkthrough was captured + signed off (evidence retained at
`tests/smoke/test-results/wd6/` — grant issuance → agent `run.launch` → agent-driven
`/runs` label → the flagged step parked with the full delegated context → approve →
completed → revoke-cancels-in-flight). WD-6 is green on the compose stack
(`task e2e`, chain 142), on the real installed stack (`task wd1`, chain 58), and in
the browser smoke suite (7/7, incl. the delegated-context legs); the pre-release
delegated-plane edge + stress sweep is green (chain 259). v0.6.0 —
**"Agent-initiated Runs"** (the delegation-grant model) — adds the **second
initiator** to the v0.5.0 Run engine (per
`docs/releases/v0.6.0/v0.6.0-release-criteria.md`; machine-readable coverage in
`release_manifest_v0_6_0.json`; how-to-verify in `verification_runbook.md`).

Baseline: **v0.5.0** (tag `981cb0e`) on v3.0.1.

## Why (the privilege delta)

v0.5.0 let an **operator** author and launch a governed Run; both pillars it stood
on vanish when an agent initiates: an `agent-tool` token is SEC-16-scoped to one
resource + one verb (it cannot author a multi-step plan — widening is escalation),
and **no human is present at launch** (informed-launch consent is absent). v0.6.0
supplies the missing authority as a **scoped, audited, revocable delegation grant**
(ADR-0062): the operator's consent moves from **per-plan to per-grant**, and the
agent then composes plans *within* it — bounded autonomy with the containment
shipped alongside the capability (the project's signature pattern, like SEC-20's
keyless gateway).

## What landed

- **The delegation grant (DEL-1, SEC-26, GOV-4/7).** A durable, **revocable,
  server-side record** in `services/api` (`internal/grant`, sibling of
  `internal/run`): principal / agent / resources / verbs / TTL / max-steps-per-run /
  max-concurrent-runs / step-budget. RFC 7662 reference-token custody — the agent
  references it by id and **never mints or holds anything mintable**. Operator
  issuance surface: `POST /v1/grants` + list/revoke (typed `human-agent` + CSRF;
  `grant.issued`/`grant.revoked` chained in-tx) + the new **`/grants` UI** (FE-17).
- **The keyless agent launch path (DEL-2, SEC-27).** A new **`run.launch`** gateway
  MCP tool (the agent's existing `aud=mcp-gateway` token; control-plane — no
  single-resource target, mints no egress, **idempotency-keyed** so a retry
  re-attaches instead of double-launching). The gateway **forwards** {agent-subject,
  grant-id, plan} to api's first inbound service-service route
  (`POST /v1/internal/delegated-runs`) — **subject-pinned `subject=="mcp-gateway"`**
  on a **route-scoped asymmetric-only verifier**. The symmetric-key-holding `api`
  stays **non-agent-reachable** (the ADR-0060/SEC-20 posture, proven as WD-6 N3 on
  compose AND the install).
- **Plan-vs-grant validation (DEL-3, SEC-29, PERF-7).** Before any dispatch, api
  validates the plan against the grant server-side (agent identity, active,
  unexpired, every resource+verb in scope, plan size, per-grant concurrency ≤ the
  gateway in-flight cap, step budget, the per-operator ceiling). Out-of-grant →
  denied + audited, **no egress** (WD-6 N1).
- **The actor/principal split (DEL-5, SEC-28, AUD-17).** A delegated Run carries
  **actor = the agent, principal = the operator, grant_id** — bound server-side from
  the grant across the `runs` row, `/v1/steps`, and the gateway invocation; the
  delegated step bracket edge is **`agent-tool → agent-remote-session`** (derived;
  operator Runs unchanged). `run.launched` + terminal events attribute the agent;
  the Evidence Packet (EVD-3) makes a delegated Run attributable to **both** the
  driving agent and the authorizing operator. No v0.4/v0.5 subject-pin loosened.
- **Governance intact under autonomy (GOV-5/6).** Ungated steps run **unattended**
  (the autonomy payoff); a flagged step still **parks** — `policy.Decide` subject =
  **the agent**, so ADR-0054 self-approve cannot fire under the absent principal.
  The approving human sees the full delegated context (**agent · on behalf of ·
  grant · run**) beside the verbatim command on `/invocations` (FE-18), and
  agent-driven Runs are visible + cancelable in the principal's `/runs` (FE-19).
- **Instant revocation cascades (DUR-7).** Revoking a grant blocks new launches AND
  **cancels its in-flight Runs** (`grant.revoked` + `run.canceled` chained); a
  parked step finalizes per the running-orphan policy — proven live mid-park (WD-6
  N2, compose + installed).
- **Offline / installed (OI-13).** The grant store ships inside `services/api`, the
  tool inside the gateway — **no new unit, no new Caddy route**; migrations ride
  `install.sh`'s existing loop. The **one new wire** is `API_URL` on `gateway.env`;
  api verifies the forward with `AIREOS_GATEWAY_PUBLIC_KEY`, already on shared
  `aireos.env` since OI-11 — **zero new key material**. Bundle rebuilt from HEAD as
  `0.6.0`: 115 MB / 41 MB gz (OI-4 caps hold).

## Security posture (no earlier invariant weakened)

The agent never mints (SEC-26); the launch surface stays on the keyless gateway and
api is not agent-reachable (SEC-27, live N3); plan-vs-grant is the relocated,
generalized SEC-16 (SEC-29); the split binds actor/principal server-side and no
subject-pin moved (SEC-28, SEC-25 carried); SEC-20/21/17/18 carried (the WD-4
forge-negative still runs in-suite). The accepted residual is the **confused-deputy
by design**: within a grant, any command on a granted resource+verb runs unattended
— the intentional autonomy payoff, contained by resources/verbs/TTL/budget/
concurrency + flagged-resource parking + instant cascading revocation.

## ADRs

- **ADR-0062 ("Agent-initiated Runs")** — the sole new ADR (Accepted 2026-06-30;
  extends ADR-0061 + ADR-0046, preserves ADR-0060, edits no Accepted ADR —
  G2-clean). ADR-0041..0061 unchanged; the canonical ladder is through ADR-0062.

## Verification (recorded in `verification_runbook.md`)

`task e2e` green — the WD-6 capstone (`TestWD6HappyPath` mixed 3-step
unattended→park-with-context→approve→continue + `TestWD6RevokeCancelsInFlight`) plus
N1/N3, the SEC-21 forge-negative, and the WD-5 regression guard, with `audit chain
intact: 142 events`. `task smoke` 7/7 — the `/grants` issue→revoke leg + the real
delegated flagged Run rendering **agent · on behalf of · grant** in the browser.
`task wd1` green on the real privileged-systemd install — `TestWD6InstalledStack`
(delegated happy + revoke-cascade + N3) with chain intact at 58 events incl. the
delegation lifecycle + the agent-actor `run.launched` attribution check.

**Sign-off:** the CEO §human WD-6 walkthrough (issue a grant on `/grants` → an agent
launches within it → watch the agent-driven `/runs` label → approve the flagged step
seeing agent · on behalf of operator · grant beside the verbatim command → completed
→ revoke cancels a second in-flight Run) was captured through the rendered cockpit
and signed off; v0.6.0 is tagged **locally** (`v0.6.0`, annotated). No remote;
nothing pushed.

## Next (v0.7+)

The chartered **Workstream** module (objective / sources / schedule / scoped memory
that *creates* Runs — the module the grant model was built to serve); the
`internal/run`+`internal/grant` extraction on a real second consumer; plan-level
grant-with-preview; fine-grained (per-command) grant policy; segregation-of-duties
at multi-operator; parallel/branch/retry DAG; Memory/Context Index (P2).
