# AI Remote Engineering OS — v0.8.0 release notes

**Status: released** — local annotated tag `v0.8.0` (no remote, per repo doctrine);
the CEO §human WD-8 walkthrough was captured + signed off (evidence retained at
`tests/smoke/test-results/wd8/` — charter with the GOV-8 consent copy → the tick
fires with nobody present → **the accrued content-free memory nobody wrote** on the
charter detail → author with the GOV-9/R2 consent copy in frame → the Redacted
tombstone → **Expired-vs-pinned after a real sweep** → the manifest export → the
content-free `memory.*` events on `/audit`; 10/10 smoke specs green alongside the
capture). WD-8 is green on the compose e2e stack (`task e2e`, chain 434), on the real
installed stack (`task wd1`, chain 103, at the full production posture — 60s schedule
floor + 2-minute sweep), and in the browser. v0.8.0 — **"Persistent Scoped Memory"**
— is the P2 tier opener and the platform's first **governance/data-plane** release
(per `docs/releases/v0.8.0/v0.8.0-release-criteria.md`; machine-readable coverage in
`release_manifest_v0_8_0.json`; how-to-verify in `verification_runbook.md`).

Baseline: **v0.7.0** (tag `47622d7`) on v3.0.1.

## Why (the 90° turn)

v0.5 → v0.7 each moved *who launches* governed work: an operator, an agent under a
grant, a schedule with nobody present. v0.8 is **not a fourth initiator** — it turns
90° and answers a different question: *what does the platform remember, and how does
it forget?* The audit chain is immutable by design — it cannot redact, expire, or
permission-scope what it records. Governed operation still demands **governed
forgetting** (retention limits, redaction of operator notes, expiry of stale working
state). Persistent Scoped Memory is where that lives: the **mutable, curated,
forgettable projection standing beside the immutable evidence spine** — the standard
event-sourcing projection architecture, applied to agent memory, self-hosted and
offline. The tier question was resolved first: the standalone Memory/Context Index is
**P2** per the requirements ladder (REQ-013/REQ-014); the P1 pillar was REQ-007
Living Workstreams, whose scoped-memory attribute shipped read-time at v0.7 (EVD-1) —
v0.8 promotes it to a durable, governed store (ADR-0064; only Layer 2 of the
three-layer context system — L1 Live Index and L3 Reasoning Retrieval stay deferred,
L3 hard-gated because it is the layer that would demand an embedding/AI dependency
against the offline + no-SaaS doctrine). Deterministic end to end: **zero new
dependency, zero AI, zero new egress, zero gateway diff.**

## What landed

- **The memory primitive (MEM-1).** A `memories` table (migration 0012) + the
  `internal/memory` store — the fourth sibling seam beside
  `internal/run`/`internal/grant`/`internal/workstream`: scoped
  (`{workstream, run, operator}`), source-traced, owner-scoped rows; nullable
  `content` **sealed at rest** via the shared `CommandSealer`; `confidence` recorded
  (never computed — an L3 concern); no update-in-place (correction = redact + write).
- **Accrual by reference (MEM-2, the core).** Every terminal Run — all three
  initiators, all outcomes (completed / failed / canceled, C1) — writes ONE
  **content-free evidence pointer** in the **same transaction** as its once-only
  terminal flip (the F-a seam: both `FinishRun` and `CancelRun` route through the
  shared guarded flip, so a no-op flip can never double-accrue and the pointer commits
  or rolls back with the Run's terminal event). This is the literal promotion of
  v0.7's read-time EVD-1 slice into a durable record, with **zero** secret-at-rest
  expansion. `memory.written` carries the Run's actor (`workstream:<id>` / the agent /
  the operator).
- **Governed forgetting (MEM-3, R2).** Redact / expire / pin as **idempotent
  once-only tombstone flips** with content-free chained events (`memory.redacted` /
  `memory.expired` / `memory.pinned` / `memory.unpinned` — the ADR-0049 pattern:
  chain the fact, never the content). **The honest boundary is stated everywhere**
  (ADR, criteria, UI): these verbs govern the mutable memory projection ONLY — the
  audit chain, session recordings (casts), and previously exported copies are
  explicitly out of redaction's reach. That boundary *is* the design.
- **The expiry sweep (DUR-9).** A bounded batch (100 rows, `FOR UPDATE SKIP LOCKED`)
  riding the **existing api reaper goroutine** on a second ticker — no fourth
  goroutine; exactly-once per row by the flip guard, not the timer; pinned rows are
  expiry-exempt. Default cadence = the 2-minute reaper tick;
  `AIREOS_MEMORY_SWEEP_SECONDS` is an optional override (the e2e compose sets ~2s;
  the installer writes nothing).
- **The operator surface (MEM-4, SEC-31).** `GET/POST /v1/memories` +
  `POST /v1/memories/{id}/redact|pin|unpin` — typed `human-agent` + CSRF,
  owner-scoped (foreign/unknown id → 404), authored writes capped at 64 KiB (a real
  413, PERF-9), list pagination store-clamped (50/200). Plus the **`/memories` UI**
  (FE-22/23): authoring with the GOV-9 consent copy (what is stored; what redaction
  reaches and what it *cannot* reach; no dark-pattern "delete" language), tombstones
  rendered as distinct **Redacted / Expired** states (derivable:
  `expiresAt ≤ redactedAt`), pinned badges, and **export** — a client-side
  deterministic markdown manifest download (D4: storage is DB-only; the manifest is
  rendered on demand, never hidden-only). The v0.7 charter detail is **promoted** to
  read the persisted store beside its EVD-1 evidence links.
- **Memory is operator-plane ONLY (SEC-31 — the release's named invariant).** No
  agent path to memory exists: no gateway tool, no agent-audience route, no
  service-service route into the store; content never enters the chain; authored
  content is secret-class at rest; memory **feeds no execution**. The canonical
  `agent-memory` edge stays **reserved-unenforced as a recorded decision** — lighting
  it up is a cross-grant prompt-injection + secret-exfiltration channel until its own
  consent/containment ADR exists (the same trigger as adaptive/agent-composed
  Workstreams). Proven live by the WD-8 N2 negative on both stacks: a **valid**
  agent Bearer is rejected 401 at `/v1/memories*`, and `tools/list` exposes only
  `remote.exec` + `run.launch`.
- **Offline / installed (OI-15).** **The first release with zero install-wiring
  diff of any kind**: migration 0012 rides `install.sh`'s existing migrate loop and
  the sweep rides the reaper goroutine that starts unconditionally — no new unit, no
  new Caddy route, no new env, no new key material, not one installer line changed.
  The full WD-8 ran installed at the production posture (the 60s workstream floor +
  the 2-minute sweep cadence). Bundle rebuilt from HEAD as `0.8.0`: 116 MB / 41 MB gz
  (OI-4 holds; byte-comparable to v0.7).

## Security posture (no earlier invariant weakened)

Nothing new mints; nothing new is agent-reachable; the gateway remains the sole
governed-step orchestrator and had **zero diff** this release. SEC-20/21/25/26..30
all carry. **SEC-31** is the new named invariant (above). The genuinely new risk
class is **stored operator content**, confined to the opt-in authored path — sealed
at rest, operator-only on read, absent from every audit event. The accepted residual
is the R2 boundary itself: redaction cannot reach the immutable spine — by design,
stated honestly in the ADR, the criteria, and the UI. Riding this release: the wd1
test harness now **verifies Caddy's TLS certificate chain + hostname against its own
exported local CA** instead of `InsecureSkipVerify` (closing a security-hook flag
raised during T-084; harness-side only).

## ADRs

- **ADR-0064 ("Persistent Scoped Memory")** — the sole new ADR (Accepted 2026-07-12;
  extends ADR-0061 + ADR-0063, preserves ADR-0046/0049/0059/0060, edits no Accepted
  ADR — G2-clean). ADR-0041..0063 unchanged; the canonical ladder is through
  ADR-0064.

## Verification (recorded in `verification_runbook.md`)

`task e2e` green — the WD-8 capstone (`TestWD8HappyPath`: unattended accrual →
author → scoped read → redact → the sweep-driven expiry with the pinned survivor;
plus the four negatives `TestWD8*`: idempotent redact / the agent-plane rejection /
owner-404 / failed+canceled accrue exactly once), with WD-5 + WD-6 + WD-7 as the
**elevated** regression guard (the accrual hook rides the terminal flip every
initiator shares) — 24 tests + 2 subtests, `audit chain intact: 434 events`.
`task smoke` 9/9 — the `/memories` authoring → redact-tombstone → pin flow rendered
in the browser (FE-22/23 DoD). `task wd1` green on the real privileged-systemd
install — `TestWD8InstalledStack` at the full production posture (accrual off a 60s
schedule tick, the expiry leg at the real 2-minute sweep), chain intact + complete
incl. `memory.written`/`memory.redacted`/`memory.expired`.

**Sign-off:** the CEO §human WD-8 walkthrough was captured through the rendered
cockpit on the smoke stack (a throwaway walkthrough spec, deleted after; screenshots
retained on disk, not git-archived — the v0.5–v0.7 precedent) and signed off. No
product bug surfaced during capture; two honest UX observations were recorded for a
future ergonomics pass, neither release-blocking: the expiry picker is
minute-granularity against a strictly-future server check (a manual walkthrough
should pick an expiry ≥2 minutes out), and `/audit` paginates oldest-first with no
server-side filter (the `memory.*` events need several "Load more" pages on a busy
stack).

**Process note:** v0.8.0 is the first release built on the Commander–Implementer
model split — consults, audits, and reviews in the war-room Commander seat; each
ticket implemented by a dispatched implementation agent against the ratified consult
shape, committing to `dev` only on green gates, with `main` fast-forwarded only after
the Commander's diff review. Seven cycles, zero slate-breaking findings.

## Next (v0.9+)

**`agent-memory` enforcement + memory→execution coupling** (one trigger, together or
not at all: the adaptive/agent-composed-Workstream consent + containment model — its
own ADR); **Layer 1** Live/Context Index (REQ-013, the B-009 spike); **Layer 3**
Reasoning Retrieval (hard-gated: any embedding/AI dependency = its own ADR against
the offline + no-SaaS doctrine); computed `confidence` (with L3); scopes beyond
`{workstream, run, operator}` (with their owning modules); multi-operator ACL +
per-read audit (with SoD); a retention default on accrued rows (trigger: observed
growth); manifest-primary storage (trigger: filesystem-first workflows); the
`internal/*` extraction; cron; DAG; the gateway exec worker; SSH pooling; full
rate-limiting; TR-2.
