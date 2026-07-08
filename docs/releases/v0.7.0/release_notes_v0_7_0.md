# AI Remote Engineering OS — v0.7.0 release notes

**Status: release-candidate** — the code + verification are complete and green; the
local annotated tag `v0.7.0` is applied at closure, after the CEO §human WD-7
walkthrough is captured + signed off (Phase B; evidence retained at
`tests/smoke/test-results/wd7/`, not git-archived — the v0.4/0.5/0.6 mechanics). WD-7
is green on the compose stack (`task e2e`, chain 357), on the real installed stack
(`task wd1`, WD-7 leg at the 60s production floor), and — for the operator surface —
in the browser smoke suite (8/8, incl. the charter→pause→resume→retire lifecycle).
v0.7.0 — **"Chartered Workstreams"** — adds the **third initiator** to the Run engine
(per `docs/releases/v0.7.0/v0.7.0-release-criteria.md`; machine-readable coverage in
`release_manifest_v0_7_0.json`; how-to-verify in `verification_runbook.md`).

Baseline: **v0.6.0** (tag `8ba5bfe`) on v3.0.1.

## Why (the privilege delta)

Each release moved who is present at launch: v0.5 a **human** (the cookie identity is
the authority; informed launch is the authorization); v0.6 an **agent** under a grant
(the grant is the standing authority; the agent composes the plan). v0.7 removes the
launch-time party entirely — **a schedule fires with nobody present**. The required
authority is therefore standing, bounded, revocable, and consented-to in advance —
which is **exactly the v0.6 delegation grant**. The GOV-4 consent move (per-plan →
per-grant) generalizes in time (per-grant → per-charter, over a reviewed immutable
template); **no third authority model is created** — the central design decision of
ADR-0063, and why the module is thin.

## What landed

- **The chartered Workstream (WS-1/WS-2, GOV-8).** A `workstreams` + `workstream_steps`
  record in `services/api` (`internal/workstream`, sibling of `internal/run`/
  `internal/grant`): objective + a bounded resource/verb scope + an **immutable, sealed
  plan template** + a fixed-interval schedule + bounds. **Chartering is one consent act**
  — it auto-issues the Workstream's own delegation grant **in the same transaction**
  (grantee = `workstream:<id>`, principal = the chartering operator, 1:1);
  `workstream.chartered` + `grant.issued` chain together. The template is immutable
  post-charter (edit = retire + re-charter — consent binds to the reviewed artifact).
  Operator surface: `POST /v1/workstreams` + list/get + pause/resume/retire (typed
  `human-agent` + CSRF) + the new **`/workstreams` UI** (FE-20).
- **The schedule ticker (WS-3, DUR-8).** A third `services/api` background goroutine
  (beside the sequencer + reaper) — **no cron dependency**: a durable `next_fire_at`, a
  single-winner CAS claim that advances from now (**no backfill** — missed ticks
  collapse to one), a fire-time-deterministic launch key (a re-fired tick re-attaches,
  never double-launches). It fires due charters with **nobody present at launch**.
- **The shared bounded-launch core (SEC-30).** Plan-vs-grant (SEC-29) + the PERF-7
  bounds + the idempotent create were extracted into **one** `run.LaunchBounded` used by
  **both** the v0.6 gateway forward and the ticker — one validator, two callers (a second
  diverging launch path is a release blocker). Behavior-preserving: WD-6 stayed green
  before the ticker rode it.
- **The windowed step budget (WS-4).** `delegation_grants` gains a nullable
  `budget_window_seconds` — a rolling-strict window over the step budget (NULL = the v0.6
  cumulative-forever semantics unchanged; charter grants set it, default = the interval).
  It closes the cumulative-budget-bricks-a-standing-charter gap without touching v0.6
  behavior.
- **"Agent" = any non-human actor under delegated authority.** A Workstream Run records
  **actor = `workstream:<id>`, principal = the operator, grant_id** on the ADR-0062
  actor/principal split, unchanged. `policy.Decide` subject = the Workstream, so
  ADR-0054 self-approve cannot fire — a flagged tick **parks** for a live human, whose
  `/invocations` view shows **scheduled by `workstream:<id>` · principal · grant · run**
  (FE-21/GOV-6). The step bracket edge is the existing **`agent-tool → agent-remote-
  session`** — the canonical seven-edge set is **unchanged** (ADR-0063 fixes the
  *reading* of `agent-tool`, not the set).
- **Lifecycle + containment (WS-5, PERF-8, DUR-7).** Pause is **soft** (ticks stop,
  in-flight Runs complete, the grant stays active); resume re-arms `next_fire_at = now +
  interval` (no catch-up fire); **retire** is the kill switch — one transaction flips the
  charter terminal + revokes its grant, then the existing cascade cancels its in-flight
  Runs. A **dead grant self-retires** the charter on the next tick
  (`workstream.retired:grant-dead`) so a revoked/expired grant cannot spin eternal
  denials. The one new abuse vector — a runaway schedule — is quadruple-bounded: an
  interval floor (60s production default) + per-grant concurrency ≤ the gateway
  in-flight cap + the windowed budget + the per-operator ceiling, with flagged resources
  still parking.
- **Offline / installed (OI-14).** The charter store + the ticker ship inside
  `services/api` — **no new unit, no new Caddy route, no new env, no new key material**.
  Migration 0011 rides `install.sh`'s existing migrate loop; the ticker rides the
  `GATEWAY_URL` already on the installed api (OI-13); the schedule floor is the 60s
  `main.go` default (the install ships the production default). The leanest install leg
  since the module line began. Bundle rebuilt from HEAD as `0.7.0`: 116 MB / 41 MB gz
  (OI-4 caps hold).

## Security posture (no earlier invariant weakened)

No new minting/escalation surface exists by construction: nothing new mints, nothing new
is agent-reachable, and `api` was already the launch authority. The genuinely new vector
is the schedule itself, quadruple-bounded (PERF-8). The single accepted residual is v0.6's
**confused-deputy by design**, extended in time and identically contained (scope / verbs /
TTL / windowed budget / concurrency + flagged-resource parking + retire-revokes-the-grant).
SEC-26/27/28/29 carry (the grant is server-side; the launch surface stays keyless — v0.7
adds zero agent-facing anything; the split binds actor/principal server-side; plan-vs-grant
is the enforced scope). SEC-20/21/25/17/18 carry. **SEC-30** is the new invariant: one
launch validator, two callers.

## ADRs

- **ADR-0063 ("Chartered Workstreams")** — the sole new ADR (Accepted 2026-07-05;
  extends ADR-0061 + ADR-0062, preserves ADR-0060 trivially, edits no Accepted ADR —
  G2-clean). ADR-0041..0062 unchanged; the canonical ladder is through ADR-0063.

## Verification (recorded in `verification_runbook.md`)

`task e2e` green — the WD-7 capstone: recurrence (`TestWD7HappyPathRecurrence` — ≥2
distinct Runs fire and complete unattended, actor = the Workstream) + the flagged
park/approve (`TestWD7FlaggedTickParksAndApproves`) + the four negatives
(`TestWD7RetireKillsInFlight`, `TestWD7PauseResume`, `TestWD7BudgetWindowRecovers`,
`TestWD7SelfRetireOnDeadGrant`), with WD-5 + WD-6 as the behavior-preserving regression
guard and `audit chain intact: 357 events`. `task smoke` 8/8 — the `/workstreams`
charter→pause→resume→retire lifecycle rendered in the browser (FE-20 DoD). `task wd1`
green on the real privileged-systemd install — `TestWD7InstalledStack` (a schedule fire
at the 60s production floor → the flagged tick parks with workstream context → approve →
completed → retire), chain complete incl. `workstream.chartered`/`workstream.retired` +
the workstream-actor `run.launched`.

**Sign-off (Phase B):** the CEO §human WD-7 walkthrough (charter a Workstream on
`/workstreams` seeing the GOV-8 consent copy → watch the schedule fire → approve the
flagged tick seeing scheduled-by-workstream · principal · grant beside the verbatim
command → completed → pause/resume → retire) is captured through the rendered cockpit and
signed off; then v0.7.0 is tagged **locally** (`v0.7.0`, annotated). No remote; nothing
pushed.

## Next (v0.8+)

The Workstream's **scoped memory / Memory + Context Index** (P2 — the Workstream is its
future owning entity); the charter **sources / rules** fields (arrive with Memory);
**cron-expression schedules** (over the fixed interval); **agent-composed plans inside a
Workstream** (the charter invokes an agent to author each tick's plan); the
`internal/run`+`internal/grant`+`internal/workstream` extraction on a real out-of-process
consumer; parallel/branch/retry DAG; the gateway-side exec worker; SSH pooling + full
per-edge rate-limiting.
