# AI Remote Engineering OS — v0.12.0 release notes

**Status: RELEASED — 2026-08-09.** All four gates green. GATE-1 (WD-11 both stacks +
its negative leg), GATE-2 (WD-1..10 full regression, both stacks) and GATE-3 (browser
smoke) were verified at T-122a. **GATE-4 — the CEO §human walkthrough on a fresh
install — was driven live and signed off at T-122c**, on a stack stood up by
`bringup-demo.sh` from the from-HEAD 0.12.0 bundle.

**GATE-4 evidence (2026-08-09, live on the installed stack):**

1. **First run from an un-seeded stack.** Both checklist items unchecked at login →
   `setup-loopback-demo.sh generate` → target created in the cockpit → item 1 flipped
   on its own → one-step Run reached **completed** → item 2 flipped on its own. The
   chain: `run.launched → tool.invoked → credential.leased → remote.exec →
   tool.result → run.completed`.
2. **The stack reports its profile.** `deployment profile lan-only` in the cockpit
   header, additive on `/v1/auth/me`, no new route and no audit event.
3. **Pairing end to end.** Enabled deliberately from its shipped-off default → QR
   rendered → redeemed in a second browser profile, which landed on the **approval
   surface only** → a held invocation approved from that paired session, chaining
   **`approval.granted` with actor `operator:admin#paired`** (AUD-34 live) → revoked
   from the desk → the paired session was **refused on its very next request** while
   its token remained cryptographically valid for another ~8h. The product's first
   revocable session, proven.
4. **Button states.** The `disabled` state observed rendering on the installed stack;
   hover / active / `focus-visible` are rigorously proven by
   `tests/smoke/button.smoke.spec.ts`, which asserts **computed styles** across all
   four variants in real Chromium. Spot-check, not primary evidence — stated as such.

**Two defects the §human gate caught that no automated gate could**, both fixed
before sign-off: `bringup-demo.sh` hard-coded a stale bundle version (invisible for
eight releases *because* `BUNDLE_VERSION` silently defaulted to the same stale value,
so the stale default was concealing the stale hard-code); and
`setup-loopback-demo.sh` gated on `systemctl is-active ssh`, which is wrong on
**Ubuntu 24.04 — this product's own primary install target** — where sshd ships
**socket-activated**, leaving `ssh.service` inactive while `ssh.socket` listens. It
reported "no sshd running" on a host where sshd was running and reachable, blocking
the documented quickstart at its first step. Same species as T-123: a real-world
default configuration no fixture in this product exercised.

**One unscripted demonstration worth recording.** Removing the demo target flipped
"Register a resource" back to unchecked while "Complete a governed action" **stayed
checked** — derived state tracking live reality, and the audit chain refusing to
forget. Mutable state and immutable evidence, visible in one screen.

Baseline: **v0.11.0** (local annotated tag `v0.11.0`, no remote) on v3.0.1.

## Why (the second of the three-release Beta-1 path)

v0.12.0 is **"Access & Onboarding"**, the second of the ratified Beta-1 path
(`docs/requirements/epic_story_catalog_v4.md` §Part 4): **v0.11 Operator Experience →
v0.12 Access & Onboarding → v1.0 MVP Beta 1**. Where v0.11 fixed defects and added no
capability, v0.12 is **the release that adds surfaces** — deliberately, so the release
that freezes the API adds nothing. It closes two unmet P0 promises found NOT-STARTED
or PARTIAL at the v4 requirements scoring (mobile access, REQ-001 gap G1; deployment
profiles, REQ-002 gap G4), ships the product's first first-run experience (REQ-022,
new at v4), and closes the Button affordance defect carried out of v0.11's own §human
walkthrough (no `<button>` in the product had a hover or active state).

## What landed

- **E-02 — Operator mobile & QR pairing (REQ-001, B-015, gap G1).** Paired operator
  sessions (**ADR-0068**) — the product's **first revocable session**. An operator
  requests a pairing at the desk, scans the rendered QR (vendored encoder, FE-39, no
  new npm dependency), and the redeeming device gets a session **capability-narrowed
  to `approvals@1`**: read pending approvals/invocations, approve/deny, read the
  audit surface, read its own session — nothing else (SEC-35/36, single-sourced in
  `services/api/internal/pairing`). Liveness is checked **per request** against the
  `paired_sessions` row, not at mint time (SEC-34); a revoked or expired session is
  refused live. No sliding refresh (SEC-39) — `PairedTTL = 8h` expires hard. Off by
  default (SEC-41): `AIREOS_PAIRING_ENABLED=0` shipped by `install.sh`, and the four
  routes answer a plain 404 while disabled — no oracle distinguishing "off" from
  "doesn't exist." The approval surface is proven usable at a 375px viewport with no
  horizontal scroll (FE-38, a real Playwright mobile project). The desk cockpit lists
  live paired sessions and can revoke any of them; revoking from the desk removes the
  phone's access on its next request (GOV-14).
- **E-03 — Deployment profiles (REQ-002, B-016, gap G4).** `install.sh` **requires**
  `AIREOS_DEPLOYMENT_PROFILE ∈ {lan-only, vpn-first, dmz-first}` (prompt-or-env,
  ADR-0051 idempotence preserved), persists it in `aireos.env`, and **refuses**
  `vpn-first`/`dmz-first` when `AIREOS_SITE_ADDRESS` is unset, `localhost`, or a
  loopback literal — a unit-tested assertion, not documentation alone (OI-22).
  `GET /v1/auth/me` carries the active profile as an additive field and the cockpit
  displays it (OI-24); no new route, no audit event for a static installation fact.
  `lan-only` is proven end-to-end by `task wd1`; `vpn-first`/`dmz-first` carry the
  enforced assertion plus documentation only — stated honestly, not overclaimed
  (OI-25). No ZTNA dependency adopted (OI-26).
- **E-04 — First-run onboarding (REQ-022, B-017, gap G3).** A checklist on the
  Dashboard derives entirely from live reads (resources exist? a governed action has
  completed?) — no flag, no migration (ONB-1); dismissal is `localStorage`-only, the
  server records nothing about onboarding progress (ONB-2). The seeded demo target is
  a **loopback resource — the installed host SSHing to itself** — and the demo action
  traverses the real policy → approval → connector → broker chain, chaining real audit
  events (ONB-3); it creates nothing privileged and is removable through the existing
  governed `DELETE /v1/resources/{id}` path (ONB-4). `docs/devops/quickstart-first-
  governed-action.md` takes an operator from an installed stack to a completed
  governed action using only documented commands and the cockpit (ONB-5).
- **E-05a — `Button` (B-018 partial, gap G5).** `packages/ui` exports a `Button` with
  variants `default`/`primary`/`destructive`/`ghost` and real hover/active/
  `focus-visible`/disabled states (FE-36); every presenting `<button>` in `apps/web`
  (48 across 9 routes at the v0.11 §human finding) migrated to it, with the existing
  link-styled idiom preserved as `ghost` so the migration is not also a visual
  redesign (FE-37).

**Hard constraint held.** v0.12.0 adds no capability edge, no actor class, no
credential path, no egress path, and no agent-plane change of any kind — the
`mcp-gateway` stays keyless (SEC-20/SEC-40), ADR-0053's sole-decrypt-authority posture
is untouched, and both agent read planes (`agent-memory`, `agent-workspace`) are
byte-unchanged. No new third-party dependency (OI-30): the QR encoder and the vendored
demo tooling are hand-rolled/vendored with recorded provenance.

## Drops — recorded, not silent (VER-10)

- **ONB-6 — reference MCP client — CUT (T-119).** Shipping a *supported* client
  artifact immediately before the E-06 API freeze would create a compatibility
  obligation the freeze would then have to honour. Promotion trigger: the E-06
  compatibility policy.
- **E-05b — `ConfirmDialog` — CUT (recorded CEO decision, 2026-08-03).** The stated
  case for shipping it — closing a verification hole — **did not hold**:
  `tests/smoke/context-sources.smoke.spec.ts` and `memories.smoke.spec.ts` already
  assert the consent copy through Playwright's dialog handler (`dialog.message()`).
  What remained was presentation-only, weighed against the risk of an accessibility
  regression on the six moments where consent matters most (a hand-rolled focus trap
  replacing a browser-native, natively-accessible `window.confirm`). FE-40/FE-41
  deferred; FE-42 stays closed on its own terms (T-115/T-116/T-117/T-118 already
  satisfy the general browser-evidence rule it states). **B-018 stays PARTIAL** — E-05a
  shipped, E-05b did not. Promotion trigger: the v1.x design pass (B-037).

## ADRs

- **ADR-0068 ("Paired operator sessions")** — the sole new ADR (Accepted 2026-08-02;
  eleven ratified decision points D1–D11). Extends ADR-0005 (implements its unmet
  installer clause), ADR-0046 (adds a **tier**, not an edge), ADR-0047 (first exercise
  of the vendored-component clause), ADR-0051 (extends the env surface), ADR-0053
  (the "state, never credentials" non-negotiable), ADR-0062 (the durable-row +
  revoke-verb + audited-terminal-flip shape), ADR-0067 (the event set conforms to its
  lifecycle-noise finding). **The one declared divergence (G2):** ADR-0046 fixes four
  stateless TTL tiers; §D4 adds a fifth tier with **server-side liveness** — filed as
  this new ADR, **ADR-0046 is not edited**, its seven-edge set and single-bootstrap-
  operator posture stand. No capability edge added, lit, or reserved: a narrowed
  operator session is a **scope within `human-agent`** (§D5), documented at
  `docs/security/agent-permission-model.md`'s new "Paired operator sessions" section.

## Disclosed limitations — read before auditing this release

- **AUD-34 forks the audit actor.** A paired approval chains `approval.granted` (or
  `approval.denied:operator`) with actor `operator:<id>#paired`; `audit-service`'s
  actor filter is **exact match**, so filtering `GET /v1/audit/events` for
  `operator:<id>` does **not** return that operator's paired-device approvals. This is
  the only mechanism available under the constraints that were both must-pass at
  once: SEC-38 requires `policy-service`/`audit-service` stay byte-unchanged, and
  AUD-34 requires a paired approval be attributable as such — `resolvedBy` is the only
  field `api` controls on this path. Disclosed at T-114b review, live-asserted by
  WD-11 (T-121), and minted as a v1.x backlog residual (**B-034**: audit provenance as
  a structured field, not an actor-string suffix). **Also correcting the record:** the
  release criteria's §5 AUD-34 text names events `invocation.approved`/
  `invocation.denied` that **do not exist anywhere in `services/`**; the real events
  are `approval.granted` and `approval.denied:operator` / `approval.denied:timeout`,
  chained by `services/policy-service/internal/store/store.go`. AUD-34's substance is
  met; the criterion's own event names were wrong and are corrected in the criteria
  document (2026-08-03).
- **GATE-1's expired-session leg is proven on compose only.** SEC-34's *revoked*
  sub-case is proven live on both stacks. The *expired* sub-case is proven on compose
  (`tests/e2e/wd11_negatives_test.go`, seeded stale Postgres state) but is a
  **documented skip on the installed stack**: `PairedTTL` is a hardcoded 8h constant
  with no env override, and the `task wd1` tester has no network path to the installed
  Postgres to seed a stale row. Closing it would need a debug force-expire route — new
  product surface purely to satisfy a gate, one ticket before the freeze — so it is
  named here as an exception rather than folded silently into "GATE-1 green." The
  liveness-check code path is identical on both stacks; only the test harness's
  reachability differs.
- **OI-25 — verification depth is honest, not uniform.** `lan-only` is proven
  end-to-end by `task wd1`. `vpn-first` and `dmz-first` carry the installer's enforced
  refusal (OI-22, unit-tested) plus documentation — they have **not** been stood up
  against a real VPN/mesh or DMZ gateway. No claim in this release states otherwise.
- **The session/FK limitation (operator-visible).** `sessions.resource_id`
  (`services/api/migrations/0002_sessions.up.sql`) is a hard FK with no `ON DELETE`
  clause, so a resource that has **ever** had a session opened against it — even one
  long since closed — cannot be deleted through `DeleteResource`'s referential guard
  (T-118c). Minted as **B-036**.
- **SEC-38 ruling on `services/connector-service`'s diff.** `connector-service`
  appears in the `v0.11.0..HEAD` diff (T-118b adds `credentials unset-secret` for the
  demo target's `--undo` path). This is **not** a SEC-38 breach: SEC-38 sits in §4
  "Paired sessions — security" and its byte-unchanged clause concerns **paired-session
  containment** — this change has no route reachable from a pairing token, no pairing
  relationship, and no paired-session reachability. `session-broker` also carries a
  diff (T-123, below) for the same reason: neither change is on the pairing path.

## What shipped beyond the criteria — worth stating plainly

- **T-123 fixed a P0 that had been latent since v0.1.0.** `session-broker`'s
  `dialClient` pinned the host key via `ssh.FixedHostKey` but never set
  `HostKeyAlgorithms`, so `x/crypto/ssh` negotiated the host-key algorithm from its
  own internal preference order rather than the pinned key's type. Against any sshd
  offering more than one host-key type — **essentially every real OpenSSH server** —
  the handshake died even with the correct key pinned and present. Found by **T-118b's
  live proof** on a real systemd-installed host with a real sshd, not by any test,
  because **every SSH fixture in the product offered exactly one key type** until
  then. T-121 (compose) and T-122a (installed) both made their sshd fixtures
  genuinely multi-key, permanently closing that blind spot rather than re-opening it
  by accident later.
- **`BUNDLE_VERSION` defaulted to a stale `0.4.0`** with nothing setting it — the
  offline bundle artifact would have shipped misnamed (eight releases stale).
  `infra/offline/build-bundle.sh` now fails loud when it is unset;
  `Taskfile.yml`'s `offline-bundle` task is the single source of truth, passing
  `BUNDLE_VERSION` explicitly (T-122a).
- **OI-31's upgrade path was exercised live for the first time.** An unattended
  re-run of `install.sh` on a bare v0.11.0 install with no
  `AIREOS_DEPLOYMENT_PROFILE` dies loudly, leaving `aireos.env` untouched; supplying
  the profile applies migration 0016 (27→29 tables — exactly `pairing_requests` and
  `paired_sessions`) and every existing key/row/operator survives byte-identical
  (T-122a).

## Contract & codegen

- **VER-2 — zero codegen drift.** `task codegen` byte-deterministic across
  `services/{api,audit-service}/internal/server/gen.go` and
  `packages/shared-types/src/generated/openapi.ts` (confirmed at every ticket that
  touched the fragment: T-112b, T-113→T-114a, T-115b).
- **CON-3 — fragment discipline.** The new routes and parameters are new named
  components; `LimitParam`, `CursorParam`, and `PageMeta` are untouched (the T-106
  lesson from v0.11.0, re-applied — the `pairing` tag's schemas are wholly new).
- **CON-4 — additive only.** No existing operation, parameter, schema, or response
  shape is removed or narrowed. `PUT /v1/resources/{id}` was found live during T-118c
  with **no consumer in `apps/web`** — left untouched (removing a live route is a
  CON-4 violation; building a UI for it was out of this ticket's scope) and minted as
  a v1.x decision point, **B-035**: keep-or-remove must be decided at the E-06 freeze,
  not carried silently past it.

## Verification

`task wd1` (installed stack) **PASS** — audit chain intact, **238 event(s) verified**,
WD-1…WD-11 all green (the full regression plus the pairing walkthrough). `task e2e`
(compose stack) **PASS** — audit chain intact, **639 event(s) verified**, no
collateral damage across the WD-1..11 suite. `task smoke` **20/20 PASSED** (real
Chromium), including the pairing, mobile-approval, and Button assertions. Offline
bundle rebuilt from HEAD: **116 MB unpacked / 42 MB gzipped** (v0.11.0 baseline held;
OI-4 caps [500 MB / 150 MB] hold with wide margin — OI-29), filename
`aireos-offline-0.12.0.tar.gz`. **OI-27** — `git diff v0.11.0 HEAD --
infra/offline/install.sh infra/offline/systemd/ infra/offline/Caddyfile` is
**non-empty by design**, and its entire content is the two env vars
(`AIREOS_DEPLOYMENT_PROFILE`, `AIREOS_PAIRING_ENABLED`) + their shared resolution
helpers + the OI-22 assertion, all inside `install.sh` — `systemd/` and `Caddyfile`
carry **zero diff**; no new systemd unit, port, key material, or Caddyfile change.
**OI-28** — migration **0016** (`pairing_requests`, `paired_sessions`) lands on
`services/api` only; `audit-service`'s forward-only schema is untouched. `task
licenses`, `task lint`, `task test`, and `python infra/scripts/doc_check.py` all
clean (T-122a). These are the numbers T-122a's own report recorded; **this ticket
(T-122b) re-ran nothing** — it is documentation-only, per its own gate.

## Diff-budget position across the release (VER-8)

**≤12 files / ≤500 LoC per ticket unless explicitly approved.** Below is every
ticket-level diff (a Commander-review follow-up commit is merged into its parent
ticket's row, matching the v0.11.0 ledger's convention). Three tickets carry a
**CEO-explicitly-approved** overage: **T-113** (the pairing data layer's live-Postgres
integration tests), **T-116** (`Button`'s browser-rendered-evidence smoke suite), and
**T-118b** (the loopback demo script + its own shell-level test harness). Several
other sub-tickets also exceed the ≤500 LoC guideline once their same-cycle review
commits are counted — recorded honestly below rather than hidden, per the project's
standing VER-8 discipline, and driven overwhelmingly by generated contract code
(`gen.go`/`openapi.ts`), the vendored QR encoder, or criteria-mandated live/browser
tests, not by hand-written scope creep:

| Ticket | Files | Insertions+Deletions | Driven by |
|---|---|---|---|
| T-112a (install profile + addendum) | 8 | 391 | within budget |
| T-112b (`/v1/auth/me` profile field) | 12 | 174 | within budget |
| **T-113** (pairing data layer + 2 review fixes) | 7 | 1,028 | **CEO-approved** — live-PG integration tests across both create/redeem paths, a single-clock expiry fix, and a closed malformed-id oracle |
| T-114a (pairing tier + contract + install fix) | 13 | 1,160 | generated `gen.go` (+233) + `openapi.ts` (+259) = 492 mechanical; remainder is the fragment + `packages/go/auth` tier + the install-side pairing-enabled persistence fix |
| T-114b (the pairing plane + review) | 11 | 1,507 | 612-line live Go test suite (`pairing_test.go`) proving SEC-34..41/GOV-12..14/AUD-32..34 against a real router walk, not scope creep |
| T-115a (vendored QR + desk surface + review) | 12 | 1,684 | 1,050 lines are Nayuki's vendored MIT QR encoder, copied byte-faithful with recorded provenance, not authored |
| T-115b (`/pair` redeem + mobile surface) | 16 | 710 | the new mobile Playwright project + its spec (FE-38) alongside the redeem route |
| **T-116** (`Button`) | 6 | 518 | **CEO-approved** — `tests/smoke/button.smoke.spec.ts` alone is 246 lines proving every variant × state combination (FE-42's mandatory browser-rendered-evidence DoD) |
| T-117 (Button migration) | 12 | 452 | within budget |
| T-118a (checklist) | 6 | 471 | within budget |
| **T-118b** (loopback demo + review) | 8 | 918 | **CEO-approved** — `setup-loopback-demo.sh` (383) + its own shell test harness (171) are the ONB-3 live governed-action proof; not UI code |
| T-118c (resource deletion + review) | 6 | 523 | the live-Postgres integration test that caught the `sessions.resource_id` FK finding (B-036) |
| T-119 (quickstart + ONB-6 drop) | 2 | 276 | within budget |
| T-120 (E-05b drop, docs) | 1 | 27 | within budget |
| T-121 (WD-11 compose) | 5 | 479 | within budget |
| T-122a (WD-11 installed + upgrade leg) | 5 | 312 | within budget |
| T-123 (SSH host-key P0 fix, unticketed at the ladder's authoring) | 2 | 180 | within budget |
| T-124 (offline bundle fix, unticketed) | 3 | 95 | within budget |

**A finding for the Commander, not a claim resolved here:** the brief for this ticket
named T-113/T-116/T-118b as the CEO-explicitly-approved overages. Recomputing every
ticket's diff from `git log`/`git diff --stat` (T-122b's own verification) shows
T-114a, T-114b, T-115a, and T-115b also exceed 500 LoC once their same-cycle review
commits are summed — all with a legible driver (generated code, vendored code, or
live-test coverage), none flagged as a scope-creep concern during their own Commander
review. This document records the actual numbers rather than only the three named in
the brief; whether the wider set needed separate sign-off is for the Commander to
reconcile, not something T-122b (documentation-only) resolves unilaterally.

## Verification & gates

- **VER-6 (G1 — live cross-boundary test).** SEC-34 (revoked + expired sessions
  refused), SEC-35 (out-of-scope actions refused), GOV-12 (single-use redeem under a
  concurrent second attempt), OI-22 (the installer refusal), ONB-3 (the demo action
  reaches a real governed terminal state), and FE-38 (the mobile-viewport approval)
  are all live-tested, no mocked boundary — proven across T-113/T-114b/T-115b/T-121.
- **VER-7 (G2 — ADR divergence).** ADR-0068 records **exactly one** declared
  divergence — ADR-0046's stateless TTL tiers gaining a fifth, server-side-live tier
  (§D4). ADR-0046 itself is **not edited**. No further Accepted-ADR contradiction was
  found unrecorded across the ladder: every review-caught defect in the ticket history
  (the T-112a re-run precedence, the T-113 malformed-id oracle and clock-authority
  fixes, the T-114a pairing-enabled persistence fix, the T-118c demo-resource-name
  single-sourcing fix) is an implementation bug fixed in-cycle, not a conflict with an
  Accepted ADR.
- **VER-9 — Commander review gate.** Every implementer diff was Commander-reviewed
  against G1/G2 before `main` advanced; review caught and fixed, before merge: the
  T-112a re-run precedence gap, T-113's malformed-id oracle + single-clock expiry,
  T-114a's pairing-enabled persistence regression, T-114b's undisclosed AUD-34 actor
  fork (documented, not fixed — the fork is load-bearing, see above), T-115a's
  untested SEC-41 degrade path, and T-118c's demo-resource-name duplication.

## Deferred (unchanged from the ratified criteria §14, plus this release's new residuals)

Generalized session revocation, `vpn-first`/`dmz-first` end-to-end proof, ZTNA
adoption, mobile beyond the approval surface, Radix UI adoption, push notification to
the paired device, and multi-device policy all carry forward exactly as ADR-0068 §14
recorded them at consult. **New this release, minted at DOC-24**
(`docs/requirements/backlog_v4.csv`):

| Row | What | Target |
|---|---|---|
| B-032 | Generalized session revocation (the criteria's own named trigger — SEC-34's natural sequel) | v1.0 (E-06) |
| B-033 | Resource-lifecycle audit — `CreateResource`/`UpdateResource` chain no audit event; T-118c added one for delete only | v1.x |
| B-034 | Audit provenance as a structured field, not an actor-string suffix (the AUD-34 fork) | v1.x, trigger-gated with B-032 |
| B-035 | Decide the fate of `PUT /v1/resources/{id}` (live, gated, unused) | v1.0 (E-06) |
| B-036 | `sessions.resource_id`'s hard FK blocks deletion of any resource that ever had a session | v1.x |
| B-037 | Design pass — the font-weight inconsistency + the `text-primary` link-styled outlier (T-116/T-117); the home for a future E-05b if one is ever argued | v1.x |
| B-038 | Enforcement-of-enforcement test gaps — an ESLint override that matched nothing, Tailwind's `packages/ui/src` scan gap, workspace-package whole-module mocking | v1.x |
| B-039 | General resource-management UI — `POST /v1/resources` has no call site outside T-118b's demo action | post-Beta |

## Process note

Built on the Commander–Implementer loop, JIT-authored per the criteria's §15 ladder
(opener → T-112 → T-113 → T-114 → T-115 → T-116 → T-117 → T-118 → T-119 → T-120 →
T-121 → T-122), with T-123 (the SSH host-key P0) and T-124 (the offline-bundle fix)
inserted out-of-ladder when T-118b's and T-122a's live proofs surfaced them. T-122
itself split three ways at the Commander's direction: **T-122a** (installed-stack
gates — done, every automated gate green), **T-122b** (this ticket — the record),
**T-122c** (the CEO's GATE-4 walkthrough, sign-off, and the tag — not yet run).

## Next — T-122c (the remaining gate)

**Not done here.** The CEO §human GATE-4 walkthrough on a **fresh install**
(`docs/releases/v0.12.0/v0.12.0-release-criteria.md` §2), scripted as
`verification_runbook.md`'s GATE-4 section, covering: (1) first-run from an un-seeded
stack reaching one real governed action, (2) the installed stack reporting its active
deployment profile, (3) the full pairing walkthrough including a live revoke observed
from a second device, and (4) Button hover/active states across the routes. On
sign-off: this artifact trio flips to **released**, `main` fast-forwards, and the
local annotated tag `v0.12.0` lands (no remote, per repo doctrine).
