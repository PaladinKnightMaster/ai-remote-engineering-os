# AI Remote Engineering OS — v0.9.0 release notes

**Status: release-candidate** — every code and verification rung (T-087..T-093) is
green; the remaining gates are the CEO §human WD-9 walkthrough (runbook Step 6) and
the closure commit + local tag (T-095). WD-9 is green on the compose e2e stack
(`task e2e`, 32 tests, chain intact) and on the real installed stack (`task wd1`, the
full production posture — 60s schedule floor, fresh 0.9.0 bundle, zero install-wiring
diff). v0.9.0 — **"Adaptive Workstreams"** — is the release where governed memory
feeds governed execution: the reserved `agent-memory` edge is enforced (read-only,
one path) and a charter's per-tick plan can be **agent-composed** — with a human
reviewing exactly that plan before anything runs (per
`docs/releases/v0.9.0/v0.9.0-release-criteria.md`; machine-readable coverage in
`release_manifest_v0_9_0.json`; how-to-verify in `verification_runbook.md`).

Baseline: **v0.8.0** (tag `58c68fd`) on v3.0.1.

## Why (the loop closes)

v0.5→v0.7 moved *who launches* governed work; v0.8 built what the platform
*remembers and forgets*. v0.9 connects them: **remember → plan → govern → act →
accrue → remember**. ADR-0064 recorded `agent-memory` enforcement and
memory→execution coupling as one fused trigger — "together or not at all" — gated on
a consent + containment model. ADR-0065 is that model. Doctrine holds throughout:
the plan author is the **customer's external agent, inbound over the existing
ADR-0056 MCP surface** (v0.6 agents already composed per-launch plans; the delta is
only the standing per-tick form) — **zero model dependency, zero embedding, zero new
egress; the L3 hard gate is untouched.** The gating primitive for plan-time memory
reads is the **delegation grant**; approval gates the use (every composed plan
parks); audit closes the loop (every agent-plane read is individually chained).

## What landed

- **The adaptive charter kind (WS-5).** `workstreams.kind` (`template`|`adaptive`,
  additive — template charters and WD-7 semantics are byte-untouched): an adaptive
  charter = objective + bounded scope + schedule + a **named composer agent**, and
  no template. The charter's auto-issued grant names **the composer as grantee**
  (R2) — one audited consent act now covers *who composes*.
- **The inbound plan channel (PLAN-1).** A tick opens a durable, TTL-bounded **plan
  request** (TTL = the interval, C2; at most one open per charter **structurally**
  — a partial unique index); the composer discovers it (`workstream.poll`) and
  answers it (`plan.submit`) over the gateway; an unanswered request **expires
  audited** and the next tick opens fresh — the charter never wedges. The platform
  never dials out (agents stay inbound MCP clients).
- **The plan-preview park (RUN-6).** A submitted plan passes plan ⊆ grant (the same
  shared `LaunchBounded` core — one validator, three callers) and parks as a
  **`proposed` Run**: inert to the sequencer/reaper/cancel guards, its **full plan
  (resource + command per step) visible to the owner on the operator GET** — the
  consent-successor invariant: for adaptive charters, **consent binds to each
  tick's reviewed plan**. Approve → `pending` → the unchanged engine (the deferred
  `run.launched` chains at approval); deny → terminal `canceled` + the distinct
  **`run.denied`** (C1 — a denied plan accrues exactly once like any canceled Run).
  At v0.9 an adaptive plan **always parks**; unattended-adaptive does not exist,
  not even as configuration.
- **The lit `agent-memory` edge (MEM-5, read-only).** One new path: the gateway
  `memory.read` tool → pinned api forward → `ReadForAgent` — a **separate** store
  method whose readable set derives **entirely server-side from the caller's own
  live grants** (charter scope + that grant's Runs — cross-grant reads impossible
  by construction, never by filter); tombstoned rows are excluded (forgetting means
  gone from the feeding surface); accrued reference rows are readable by default,
  **operator-authored content only after an explicit audited share flip**
  (`agent_visible`, default false; `memory.shared`/`memory.unshared` — MEM-6, with
  honest UI: no share toggle on accrued rows, whose visibility the flip cannot
  change). **Every agent-plane read is chained fail-loud** — the `memory.read`
  event enqueues inside the read transaction; no event, no data (R4). The edge's
  three carriers: the ToolDef metadata, the route posture, the event edge. The
  operator `/v1/memories*` surface is **byte-identical to v0.8** (WD-8 proves it).
- **The operator surface (FE-24/25, GOV-10).** The adaptive charter form (composer
  named in the consent copy; no step editor); plan requests on the charter detail;
  the **informed plan preview** on `/runs` (the full composed plan in the approval
  visual language, Approve/Deny both first-class); the share badge + flip behind a
  confirm that names the prompt-injection/exfiltration risk plainly.
- **Offline / installed (OI-16).** **Zero install-wiring diff again**: migration
  0013 rides `install.sh`'s existing migrate loop; the three tools ride the
  already-installed gateway binary + Caddy `/mcp` route — no new unit, env, or key.
  The full WD-9 ran installed at the production posture (60s floor), including
  **double governance live**: the plan-level park plus a flagged resource's
  invocation-level park inside the approved plan. Bundle rebuilt from HEAD as
  `0.9.0`: 116 MB / 41 MB gz (OI-4 holds; identical to v0.8).

## Security posture (SEC-32 — the release's named invariant)

Agent memory access is **read-only**, **same-grant-scoped by construction**,
**share-gated** for authored content, **per-read chained** (the ADR-0064 §8
escalation, fired for the non-owner plane only — operator reads stay unchained), and
**every agent-composed plan parks** before execution. Nothing new mints; the gateway
stays keyless (its three new verbs are forwards over its existing service identity);
`plan.submit` pins the Bearer subject to the grant's grantee; revocation cascades to
all three new surfaces (open requests close, poll/submit/read die, a `proposed` Run
of a dead grant is canceled with `run.canceled` — **never `run.denied`, which stays
exclusively the human review verdict**). SEC-20/21/25/26..31 all carry; the WD-8
tool-set negative is amended to the exact five-tool set with its spirit intact (no
memory tool other than `memory.read` reaches the agent plane).

## ADRs

- **ADR-0065 ("Adaptive Workstreams")** — the sole new ADR (Accepted 2026-07-16;
  extends ADR-0062/0063/0064, preserves ADR-0046 [the edge *set* is unchanged —
  enforcement of the reserved seventh edge, not an eighth]/0056/0060 and ADR-0064's
  operator plane; edits no Accepted ADR — G2-clean). The canonical ladder is through
  ADR-0065.

## Verification (recorded in `verification_runbook.md`)

`task e2e` green — 32 tests: the WD-9 capstone (`TestWD9HappyPath`: the closed loop
with the leg-7 assertion that the *next* tick's read returns the just-accrued
pointer, plus the deny cycle and the SEC-29 refusal) + the live negatives
(`TestWD9CrossGrant` no-oracle containment / `TestWD9ShareGate` / 
`TestWD9RevokeKillsAll` — the strongest form / `TestWD9ExpiryNeverWedges`), with
WD-5/6/7/8 elevated and byte-unchanged. `task smoke` 11/11 — the share flip with the
GOV-10 copy and the adaptive charter whose first tick opens a plan request with
nobody present, rendered at the real 60s floor. `task wd1` green on the real
privileged-systemd install — `TestWD9InstalledStack` at the full production posture
(252.6s; suite 487.7s against the 1500s ceiling), chain intact with the complete
v0.9 event vocabulary in the WANTS list. **Chain counts are range-stable, not
exact, since v0.9** — schedule-driven legs emit tick-timing-dependent
`plan.requested`/`plan.expired` events; integrity plus per-leg exact counts are the
assertions (observed: compose 518/515 across two independent green runs; installed
156).

**Process note:** built on the Commander–Implementer split (consults, reviews, and
this document in the Commander seat; each ticket implemented by a dispatched
implementation agent, `main` fast-forwarded only after the Commander's diff
review). Seven implementation cycles; zero slate-breaking findings; T-092's G1 was
independently confirmed twice (a concurrent duplicate verification in an isolated
compose project — see the runbook's concurrency note).

## Next (v1.0-candidate scoping)

**Agent memory writes** (a second consent problem; trigger: a real composer need
read-only context cannot express); **unattended adaptive execution** (trigger:
operational trust evidence from parked adaptive charters; its own consent
decision); **Layer 1** Live/Context Index (REQ-013, B-009 — now with a named
consumer: the composer's plan-time context); **Layer 3** (hard-gated unchanged);
computed `confidence`; a retention default on accrued rows; multi-operator ACL +
operator-read chaining (SoD); manifest-primary storage; the `internal/*`
extraction (trigger still unfired — every v0.9 path rides gateway→api in-process);
cron; DAG; the gateway exec worker; SSH pooling; full rate-limiting; TR-2.
