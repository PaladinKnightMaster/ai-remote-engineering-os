# ADR-0068: Paired operator sessions — revocable sessions, QR pairing, deployment profiles, and the consent surface

## Status

**Accepted** — 2026-08-02 (v0.12 "Access & Onboarding" scoping consult; the eleven decision points
D1–D11 were signed off on the war-room recommendation, including three recommendation-level
divergences from the ratified epic catalog: **D3** splits E-05 into a non-droppable Button half and a
droppable dialog half, **D5** narrows the paired session's capability to the approval surface —
collapsing E-02/S-08 from a whole-SPA responsive pass — and **D9** places E-04/S-16 *ahead of* E-05b
in the drop order, softening the catalog's single-drop-candidate framing). Author/decider: CEO
(war-room). Binding spec for the v0.12 ticket ladder; code is authored just-in-time per ticket from
here.

**Builds on / conforms to:** ADR-0005 (VPN-first / DMZ-first topologies — §D1 **implements** its
unmet installer clause and changes none of its decision), ADR-0046 (in-process PASETO capability
tokens, the seven-edge set, the tiered TTLs, the single bootstrap operator — §D4 adds a **tier**, not
an edge), ADR-0047 (the HttpOnly/Secure/SameSite=Strict session cookie, double-submit CSRF, the
same-origin SPA posture, vendored shadcn/ui in `packages/ui` — §D7 is the first exercise of the
component clause), ADR-0051 (the offline installer and its provisioning-by-prompt-or-env contract —
§D2 extends its env surface), ADR-0053 (connector-service is the sole decrypt authority — the
non-negotiable that §D3's "state, never credentials" rule inherits), ADR-0062 (delegation grants —
the durable-row + revoke-verb + audited-terminal-flip shape §D4 reuses), ADR-0067 (audit read
semantics — §D6's event set is chosen to conform to its lifecycle-noise finding).

**ADR divergence, declared (G2).** ADR-0046 fixes four TTL tiers (agent 5min / operator 8h /
service 1h / lease = lease expiry) and a **stateless** verification model with no revocation
primitive. §D4 adds a fifth tier with **server-side liveness**. That is a real divergence from an
Accepted ADR, which is why it is filed here as a **new** ADR and ADR-0046 is **not edited**. No
capability edge is added, lit, or reserved: a narrowed operator session is a **scope within
`human-agent`**, and the canonical seven-edge set is unchanged.

## Context

**The trigger.** Two P0 requirements came back **INCOMPLETE** in the v4 scoring
(`docs/requirements/requirements_v4.csv`), and they are the reason v1.0 is two releases away rather
than one:

- **REQ-001** — "browser/**mobile** GUI and terminal access." The terminal cockpit shipped at v0.1.0;
  **mobile was never built**, despite being named in the requirement text and in roadmap Phase 1 (G1).
  An unmet P0 promise.
- **REQ-002** — "Support LAN, VPN-first, and DMZ-first deployment profiles."
  `docs/deep-dive/03-network-topologies.md` names the three profiles and states *"all documentation
  and installers must identify which profile is active."* A P0 requirement with an unmet
  implementation clause (G4).

**As-built findings (HEAD `08748b0`).** Six findings, each verified against code rather than
inherited from the catalog. Two of them changed a recommendation.

1. **`install.sh` has no profile concept — and the reason that is a *small* fix matters.** Every
   service binds `127.0.0.1` (`infra/offline/install.sh` §6, `API_ADDR`…`MCP_ADDR`). **Caddy is the
   only externally-bound process**, driven by one variable, `AIREOS_SITE_ADDRESS`, defaulting to
   `localhost` with `tls internal`. **The product's entire network contract is one edge.** This is
   what makes E-03 genuinely bounded — and it is also what makes the profile more than a label,
   because `vpn-first` and `dmz-first` are *incompatible* with a `localhost` site address served by a
   self-signed local CA.
2. **There is no server-side session revocation. This is the release's central finding.** Operator
   auth is a **stateless** PASETO token (`packages/go/auth/auth.go` — `OperatorTTL = 8 * time.Hour`)
   carried in an HttpOnly cookie. `Logout` (`services/api/internal/server/server.go`) only *clears the
   cookie* — the token remains valid until expiry. There is no `jti` denylist and no auth-session
   table (`services/api/migrations/0002_sessions` is **SSH terminal** sessions, not auth sessions).
   E-02/S-06 requires the pairing state be "revocable, TTL-bounded, audited"; **the platform cannot
   today revoke any operator session at all.**
3. **`/v1/pairing-sessions` exists only in the aspirational v3.0.1 contract.**
   `docs/api-contracts/openapi/openapi.yaml` declares it; the **implemented** fragment
   (`docs/api-contracts/openapi.v3.fragment.yaml`) does not, and no pairing code exists anywhere.
   Nothing to build on — and nothing to unpick.
4. **The button-affordance finding is sharper than recorded.** 48 `<button>` elements across 9
   routes. Of 27 `hover:` styles, 7 *are* on buttons — but every one is `hover:underline` on a
   **link-styled** button ("Refresh"). **No button with an actual button appearance (bordered or
   filled) has any hover or active state.** The gap is not inconsistent styling; it is that **no
   `Button` appearance exists in the design system at all.**
5. **`packages/ui` has zero Radix dependencies.** ADR-0047 names vendored shadcn/ui + Radix
   primitives, but Radix was never pulled in — `ApprovalPrompt` and `TerminalView` are hand-rolled.
   `apps/web` carries **no non-workspace runtime dependency** beyond `react`, `react-dom` and
   `react-router-dom`. So both a QR encoder and a dialog primitive are live dependency decisions
   against a two-for-two hand-rolled precedent.
6. **E-04 starts from nothing, and `api` has no health/version route.** No quickstart, no runbooks
   directory, no reference MCP client (the `tests/e2e/*_driver.go` helpers are Go test helpers, not a
   shippable artifact). `services/api` exposes no `/healthz` or `/version` — so "the stack reports its
   active profile" has no existing home (§D2 resolves this).

**Why an ADR and not four tickets.** Three of the four epics are ordinary work that conforms to
existing Accepted ADRs. The fourth introduces the **first revocable operator session in the product**,
diverging from ADR-0046's stateless model — and it does so in the release immediately before the one
that freezes the API. That decision cannot be made in a feature ticket.

## Decision

### D1. Deployment profiles are **documentation plus an installer assertion** — no ZTNA dependency

**Adopt none of NetBird / OpenZiti / Pangolin / Headscale in Beta-1.** All four solve *reaching the
Caddy edge* — which, per as-built finding 1, is the product's only network contract and is
infrastructure the buyer's SOC already owns. Adopting one would put a second network daemon inside
the offline bundle (against the OI-4 budget), add a supply chain to an air-gap-marketed product, and
require its own ADR — in order to duplicate what the customer already has. The four remain
**undispositioned Tier-1 watch items** in `references/source_watchlist.md`.

**Declining a dependency changes no baseline and therefore needs no further ADR.** Adopting one later
would.

**The deliverable is not a label.** The honest statement is: *the product's only network contract is
the Caddy edge; the profile declares how that edge is reached and what the installer therefore
asserts.* Concretely — `AIREOS_DEPLOYMENT_PROFILE ∈ {lan-only, vpn-first, dmz-first}`, required by
`install.sh` (prompt-or-env per ADR-0051), persisted in `aireos.env`, and **enforced**: the installer
**refuses** `vpn-first` or `dmz-first` when `AIREOS_SITE_ADDRESS` is unset, `localhost`, or a loopback
literal. That assertion is unit-testable without ever standing up a VPN, which is precisely why it is
a real gate rather than a nominal one.

**Verification depth is asymmetric and stated as such.** `lan-only` is proven end-to-end by
`task wd1`. `vpn-first` and `dmz-first` ship an **honest support statement** plus the installer
assertion. The release does not claim to have tested topologies it has not stood up.

### D2. The active profile is a **read-only system fact**, surfaced on `/v1/auth/me`

**No audit event.** A per-boot configuration echo is exactly the content-free lifecycle volume
ADR-0067 §D3 was written to stop generating; the profile is a static installation property, not a
governed action. It is recorded in the installer log and in `aireos.env`, and it is **read** by the
cockpit.

**It rides `GET /v1/auth/me` as an additive field**, not a new route. Rationale: as-built finding 6
shows there is no health/version route to extend, and minting a new route two releases before the
E-06 OpenAPI freeze is the wrong order. `/v1/auth/me` is already the SPA's bootstrap call
(`<CapabilityProvider>`, ADR-0047), already capability-gated, and already answers *"what is the
context of this session."*

*Accepted smell, recorded rather than hidden:* a deployment fact riding an auth route is a mild
concern-mixing. It is preferred to a new pre-freeze route, and E-06 may relocate it behind the
compatibility policy.

### D3. E-05 splits into a **non-droppable defect fix** and a **droppable consent surface**

The catalog designates E-05 the drop candidate. Left whole, that designation is a trap: the Button is
a **prerequisite** of the modal (a consent dialog cannot be built without deciding primary/destructive
button variants), so dropping E-05 would strand the affordance defect in a v1.0 that **by structural
rule #1 adds nothing functional** — meaning it could never be fixed before Beta-1.

- **E-05a — `Button` in `packages/ui` + call-site migration. NOT droppable.** As-built finding 4
  establishes this as a **defect on already-shipped functionality** — the same class as E-01, not
  polish. Variants: `default` / `primary` / `destructive` / `ghost` (the existing link-styled idiom,
  preserved so the migration is not also a visual redesign). States: hover, active, `focus-visible`,
  disabled. Tokens come from the existing Tailwind theme; **no new token is minted.**
- **E-05b — `ConfirmDialog` + migration of the six `window.confirm` sites + re-verified DoD.**
  **This** is the drop candidate.

**The consent copy does not change.** All six call sites carry copy that was consent-reviewed when it
shipped; only the *presentation* changes. This keeps the gate's semantics byte-identical and makes the
re-verification narrow and honest.

**G2 — clean.** ADR-0047 already ratifies vendored shadcn/ui in `packages/ui`. A Button and a Dialog
are **conformance** with it, not divergence. No ADR is needed for E-05, and none is filed.

### D4. **Paired operator sessions** — the first revocable session in the product

As-built finding 2 forces a choice. Three options were considered and two rejected:

- **Rejected — mint a short-TTL operator token for the phone.** TTL-bounded and audited, but **not
  revocable**. It fails E-02/S-06 as written, and it means an operator who loses their phone has no
  recourse but to wait out the clock.
- **Rejected — make only the *pairing request* revocable, leaving the resulting phone session an
  ordinary 8h cookie.** This is worse than the first option because it is *dishonest*: a "revoke"
  button that stops future pairings while an already-paired phone keeps working would tell the
  operator they had done something they had not.
- **Accepted — a durable, revocable paired session.** A `paired_sessions` row (id, operator subject,
  label, `scope`, `created_at`, `expires_at`, `revoked_at`); the minted token carries the row id as its
  **`jti`**; `services/api` checks the row is **live** on every request. This is structurally the move
  ADR-0062 already made for delegation grants — a durable row, a revoke verb, an audited terminal flip
  — applied to sessions.

> A governance product in which an operator cannot revoke a lost phone's access is not defensible in a
> commercial Beta 1. Revocability, **not** a shorter TTL, is the control.

**Where the liveness check lives — and why not in shared middleware.** `RequireCapability`
(`packages/go/middleware`) is imported by every service; a database check there would give
`session-broker`, `policy-service`, `connector-service` and `audit-service` a dependency on an
`api`-owned table. **The paired session exists on the `services/api` operator plane only**, and api
performs the liveness check in its own layer, where the store already is. The phone's SPA talks only
to api (same-origin, ADR-0047), so it **structurally cannot reach** the other services.

**TTL and refresh.** `PairedTTL = 8h` — the same as `OperatorTTL`, deliberately: inventing a fifth
number would imply the TTL is the control, and it is not. But the paired session gets **no sliding
refresh** — it expires hard and must be re-paired. That is a real tightening at zero cost, and it is
the one place a phone should differ from a desk.

### D5. The paired session is **capability-narrowed to the approval surface**

The phone's session resolves a **named, versioned scope — `approvals@1`** — deliberately mirroring
ADR-0067 §D3's `governed-actions@1`: membership lives in **one** place in `services/api`, is
enumerated in the v0.12 release criteria, and cannot be widened without a criteria change and
therefore a new ADR.

`approvals@1` permits: read pending approvals/invocations, **approve/deny**, read the audit surface,
read its own session. It **denies**: launching Runs, issuing or revoking grants, registering or
removing context sources, minting agent tokens, opening terminal sessions, and creating further
pairings.

Two things are bought at once. It is the security-correct posture — a phone is the least-trusted
device that will ever hold an operator session. And it collapses **E-02/S-08 from "make nine routes
responsive" to "make the approval surface responsive"**, which is where the mobile value actually is:
a governance product is fundamentally about the async human-in-the-loop moment.

**It also enforces the catalog's scope discipline in code rather than by intention.** E-02 must not
become a rebuild of mobile control over a Claude/Codex agent session — the customer's agent ships
that. A session that cannot open a terminal or launch a Run **cannot become that**, whatever a later
ticket intends.

**No new capability edge.** A narrowed operator session is a scope *within* `human-agent`. The
canonical seven-edge set (`docs/security/agent-permission-model.md`) is unchanged.

### D6. The pairing mechanism — one-time secret, fragment-carried, single-use, audited

1. **Request.** An authenticated desktop operator `POST`s `/v1/pairing-requests`. api generates a
   32-byte secret, stores **only its hash**, and returns the secret **once** in the response body.
   `expires_at = now + 120s` — long enough to unlock a phone and scan, short enough that a QR left on
   a screen is worth little.
2. **Render.** The cockpit renders a QR encoding `https://<site>/pair#<id>.<secret>`. The secret rides
   the **URL fragment**, which is never sent to the server and therefore never lands in an access log
   or a `Referer` header; the SPA reads it in JS and posts it deliberately.
3. **Redeem.** The phone `POST`s `/v1/pairing-requests/{id}/redeem`. api verifies the hash, the
   expiry, and that the request is unconsumed; marks it **consumed in the same transaction**
   (single-use); creates the `paired_sessions` row; mints the token with `jti` = row id and scope
   `approvals@1`; sets the session cookie. Failed redeems are capped, after which the request is
   consumed.
4. **Revoke.** The desktop cockpit lists paired sessions and revokes any of them. Logout **from the
   phone** revokes its own row — which, for the first time in this product, makes logout mean
   something on the server.

**"State, never credentials" is enforced by construction, not by care.** What crosses the QR is a
one-time secret that is exchanged for a *narrower* session. The operator's password, the cluster
keys, and every connector credential are untouched — ADR-0053's sole-decrypt-authority posture is not
even adjacent to this path.

**Three audit events, not five: `pairing.requested`, `pairing.redeemed`, `pairing.revoked`.** There is
deliberately **no `pairing.expired`** and no expiry-sweep event. An unredeemed request that lapses is
a non-event, and a reaper chaining one per lapse would manufacture exactly the content-free ticker
volume ADR-0067 §D3 condemned. All three retained events are operator-initiated facts, bounded by
operator actions.

### D7. Dependencies — hand-roll both; no new npm package

As-built finding 5: `apps/web` has taken on no new runtime dependency since v0.1.0, and
`packages/ui` hand-rolls the two components it has.

- **QR encoding** is ~200 lines of pure computation with no I/O and no platform surface. **Vendored /
  hand-rolled**, consistent with ADR-0047's copy-paste-not-depend clause for shadcn.
- **The dialog** is hand-rolled: focus trap, Escape to cancel, backdrop-click to cancel,
  `role="alertdialog"`, `aria-labelledby` / `aria-describedby`, and **initial focus on the safe
  action** for destructive confirms. Radix remains ADR-0047-sanctioned and unused; pulling it for one
  component would trade a bounded ~150 lines for a dependency tree in the release before a freeze.

### D8. Onboarding is derived from **live state**, and the demo target is the host itself

- **First-run detection needs no migration and no flag.** The checklist is derived from live state —
  is there a resource? a connector credential? has a governed action ever completed? — rather than
  from a stored `first_run_completed` column. Derived state cannot go stale, cannot disagree with
  reality, and costs no schema. A per-browser dismissal (localStorage) hides the panel without
  becoming server state.
- **The demo path targets the installed host.** An air-gapped single-host install cannot conjure a
  second machine, so a "seeded demo target" that requires one is not a demo — it is a prerequisite.
  The honest one-click path is a **loopback resource: the host SSHing to itself**, which is a real,
  fully governed action through the real policy → approval → connector → broker chain. This is the
  only genuine one-click governed demo available on the deployment posture we actually ship.

### D9. The gate — WD-11 for pairing only, re-tested against the as-built

Structural rule #2: *a new WD-N is minted **only** when a release adds new governed capability.*
Re-tested per epic at this consult, not inherited:

| Epic | New governed capability? | |
|---|---|---|
| **E-02 pairing** | A new session type, a new token tier, the **first revocation primitive**, a new consent-bearing act (pairing a device). | **Yes → WD-11** |
| E-03 profiles | An install-time env var and an installer assertion. No route, edge, actor, token, or consent moment. | No → rides `task wd1` |
| E-04 onboarding | A checklist derived from existing reads, plus a resource created through the **existing** governed path. | No |
| E-05 consent surface | Presentation of gates that already exist; copy byte-unchanged (§D3). | No |

**WD-11 must carry a negative leg**, in the shape SEC-21 established at WD-4: a **revoked** and an
**expired** paired session are both refused, **and** a live paired session is refused an out-of-scope
operator action (launch a Run / mint an agent token / open a terminal). A capability claim is proven
by what it *denies*.

**Two gate corrections, made because the ratified assignment was re-tested rather than inherited:**

- **"Profiles ride `task wd1`" is true but weak as written.** `wd1` installs exactly one way, so it
  will only ever exercise `lan-only`. Stated honestly: **`wd1` proves `lan-only` end-to-end; the
  `vpn-first`/`dmz-first` installer refusal is unit-tested; the rest is a documented support
  statement.**
- **Onboarding had no gate at all.** Every gate the product owns starts from a *provisioned* stack,
  and `task smoke` runs against a seeded backend — but a first-run experience is **by definition**
  about the un-seeded state. No new ceremony is minted to fix this: **`task wd1` already builds a
  stack from scratch, so the v0.12 §human leg is the first-run walkthrough on that fresh install.**

### D10. Pairing ships **disabled by default**

`AIREOS_PAIRING_ENABLED` defaults to **off**, written as `0` by `install.sh`. Some locked-down SOCs
forbid phones on the floor (E-02/S-09); a pairing capability that is on by default in an air-gapped
install is a posture regression shipped as a feature. When disabled the routes answer **404** — not
403 — consistent with the no-oracle precedent in ADR-0065/0066, and the QR affordance is hidden by
capability rather than by CSS.

This also protects the installed-stack gate: with pairing off, `task wd1`'s stack behaves exactly as
v0.11's did unless the walkthrough deliberately turns it on.

### D11. Fragment discipline and the sequencing that follows from it

New routes (`/v1/pairing-requests`, `/v1/pairing-requests/{id}/redeem`, `/v1/paired-sessions`,
`/v1/paired-sessions/{id}/revoke`) and the additive `/v1/auth/me` field are **new named components**
in the one fragment. **`LimitParam` and `CursorParam` are not edited** — ADR-0067 §D9's CON-1 trap
stands, and v0.11's T-106 lesson (a shared *schema* is as dangerous as a shared parameter) applies:
**`PageMeta` is not extended either.** `task codegen` drift stays zero (VER-2).

**This is the last growth of the API surface before the E-06 freeze**, and it lands *before* the
freeze rather than needing an exception after it.

## Security posture

This release adds a **session type**, a **token tier**, and a **revocation primitive**. It adds **no
capability edge**, no actor class, no credential path, and no egress path. The `mcp-gateway` stays
keyless (SEC-20); ADR-0053's sole-decrypt-authority posture is untouched; no agent-plane surface
changes at all.

The genuinely new risks, and their controls:

- **A QR photographed off a screen.** The secret is single-use, 120-second, and exchanged only for a
  scope-narrowed session that is itself revocable and listed on the desktop.
- **A lost paired phone.** Revocation — the primitive this ADR exists to add. Before v0.12 this risk
  existed for the *desktop* session and had **no** control at all; the release therefore leaves the
  product strictly safer than it found it.
- **Scope creep on `approvals@1`.** Membership is single-sourced, versioned, enumerated in the
  criteria, and criteria-drift requires a new ADR — the same containment ADR-0067 §D3 put around
  `governed-actions@1`.
- **A pairing surface enabled where phones are forbidden.** Off by default, 404 when off (§D10).

## Consequences

- **Two unmet P0 promises close** — REQ-001's mobile clause (G1) and REQ-002's installer clause (G4) —
  which is what makes the following release able to be a stability-only v1.0.
- **The product gains its first revocable operator session.** A durable capability that outlives the
  pairing feature and is the natural substrate for the post-Beta multi-operator work (E-14/G14).
- **`logout` becomes meaningful on the paired plane**, and remains cosmetic on the desktop plane. That
  asymmetry is deliberate and stated: generalizing revocation to *all* operator sessions is a larger
  change than v0.12 should carry before a freeze, and it is recorded as deferred below.
- **The design system gains its first real component tier** (`Button`), fixing a defect that has been
  in every route since v0.1.0.
- **No new third-party dependency** — the QR encoder and the dialog are both vendored (§D7), so
  `task licenses` stays flat and the OI-4 bundle budget is untouched by the frontend.
- **Install wiring changes for the first time since v0.5.0** — two new env vars
  (`AIREOS_DEPLOYMENT_PROFILE`, `AIREOS_PAIRING_ENABLED`) plus one api migration. The
  zero-install-wiring-diff criterion that held for v0.9–v0.11 **cannot** hold here and is not claimed;
  it is replaced by an explicit, enumerated diff.
- **Accepted cost:** api now performs one indexed liveness read per request on paired sessions. It is
  bounded, single-table, and on the operator plane only — desktop sessions take no new read, because a
  token with no paired `jti` skips the check entirely.

## Red-team

| # | Attack / failure | Closed by |
|---|---|---|
| 1 | A photographed QR is redeemed by an attacker | Single-use + 120s TTL + hash-at-rest; the redeeming device gets a scope-narrowed, revocable, listed session (§D6, §D5) |
| 2 | The pairing secret leaks through access logs or `Referer` | It rides the **URL fragment**, which browsers never transmit (§D6) |
| 3 | A lost phone retains access indefinitely | Revocation (§D4) — the primitive that did not exist before this release |
| 4 | A revoked session keeps working until its token expires | Liveness is checked **per request** against the row, not at mint time (§D4); WD-11's negative leg proves it (§D9) |
| 5 | The phone session is used to launch Runs or mint agent tokens | `approvals@1` denies them server-side (§D5); WD-11 asserts the denial, not just the permission |
| 6 | The phone reaches broker / connector / policy / gateway directly | It cannot: the paired session exists on the api plane only, and the SPA is same-origin to api (§D4) |
| 7 | `approvals@1` is silently widened | Single-sourced, versioned, enumerated in the criteria; widening is criteria drift ⇒ new ADR (§D5) |
| 8 | Pairing is enabled by default in a SOC that forbids phones | Off by default; 404 when off, no oracle (§D10) |
| 9 | Credentials cross the pairing channel | Nothing but a one-time secret crosses; ADR-0053's decrypt authority is not on this path (§D6) |
| 10 | Brute-forcing the redeem endpoint | 32-byte secret + single-use + 120s window + a failed-attempt cap that consumes the request (§D6) |
| 11 | A pairing reaper manufactures audit noise | No `pairing.expired` event and no sweep event exist by decision (§D6), conforming to ADR-0067 §D3 |
| 12 | A shared fragment component is edited and silently changes every list route | §D11: new named components only; `LimitParam`/`CursorParam`/`PageMeta` untouched; codegen drift zero |
| 13 | `vpn-first` is installed against a `localhost` site address and quietly serves nothing reachable | The installer **refuses** it (§D1) — a unit-testable assertion, not a doc sentence |
| 14 | The consent gate's meaning changes while its presentation is restyled | Copy is byte-unchanged across all six sites (§D3); the smoke leg asserts the modal and the DoD is re-verified in a real browser |

**Verdict.** The release adds one genuinely new primitive — a revocable session — and every risk it
introduces is controlled by that same primitive plus a narrowed, versioned scope. The residual worth
naming is that **pairing widens the operator surface to a device class the product has never
supported**; it is controlled by making that device strictly less capable than the desk it was paired
from, and by shipping the whole capability off by default.

## Deferred (recorded)

- **Generalizing revocation to *all* operator sessions** (making desktop `logout` real). The natural
  sequel to §D4 and deliberately out of v0.12: it would change the auth posture of every existing
  session in the release before an API freeze. Trigger: the E-06 security-audit capstone, or the
  multi-operator work (E-14/G14).
- **`vpn-first` / `dmz-first` proven end-to-end.** Documented + assertion-tested only (§D1). Trigger:
  a customer deployment on that topology, or a ZTNA adoption decision — which would need its own ADR.
- **Adopting NetBird / OpenZiti / Pangolin / Headscale** — **declined for Beta-1**, not deferred
  pending analysis; they remain Tier-1 watch items under the standing decision filter.
- **Mobile beyond the approval surface** (terminal, Runs, charters on a phone) — rejected for Beta-1
  on §D5 grounds, not merely deferred. A future need requires arguing past the narrowed scope.
- **Radix UI adoption in `packages/ui`** — ADR-0047-sanctioned and unexercised (§D7). Trigger: a
  component whose accessibility surface genuinely exceeds a hand-rolled implementation (a combobox or
  a date picker would qualify; a confirm dialog does not).
- **E-04/S-16 reference MCP client** — in scope but **first in the drop order** (§D9/D11 of the
  consult): shipping a supported client artifact immediately before an API freeze creates a
  compatibility obligation the freeze would then have to honour.
- **Push notification / out-of-band alerting to the paired device** — the obvious mobile follow-on and
  a SaaS-dependency trap on an air-gapped product. Not scoped.
- **Multi-device policy** (limit N paired sessions, geo/network binding) — the listing and revocation
  surface makes it observable; enforcing it is post-Beta.

## Relation to prior ADRs

- **ADR-0046 (auth adapter, capability tokens, TTL tiers)** — **the one declared divergence**: §D4
  adds a fifth TTL tier with server-side liveness where ADR-0046 specified four stateless tiers.
  Filed as a new ADR; ADR-0046 is **not edited**. Its edge set, its single-bootstrap-operator posture,
  and its no-IdP-daemon decision are all preserved.
- **ADR-0005 (VPN-first / DMZ-first topologies)** — §D1 **implements** its unmet installer clause and
  changes none of its decision. Conforms; no follow-up ADR required by its own terms.
- **ADR-0047 (SPA, cookies, CSRF, vendored shadcn/ui)** — §D3/§D7 are the first exercise of its
  component clause; the cookie attributes, the same-origin posture, and the double-submit CSRF
  requirement are unchanged and apply to the paired session identically. Preserved.
- **ADR-0051 (offline installer)** — §D1/§D10 extend its prompt-or-env provisioning contract with two
  variables. Its idempotence guarantee is preserved: re-running with an unchanged profile is a no-op.
- **ADR-0053 (connector-service sole decrypt authority)** — untouched and not on the pairing path;
  it is the source of the "state, never credentials" non-negotiable §D6 satisfies.
- **ADR-0062 (delegation grants)** — supplies the durable-row + revoke-verb + audited-terminal-flip
  shape §D4 reuses for sessions. Extends by analogy; edits nothing.
- **ADR-0067 (audit read semantics)** — §D6's three-event set and the deliberate absence of
  `pairing.expired` conform to its §D3 lifecycle-noise finding; §D11 inherits its CON-1 fragment
  discipline. Preserved.
- **ADR-0060 (per-workload asymmetric keys) / ADR-0065 / ADR-0066** — the agent plane, the
  always-park invariant, and both agent read planes are untouched by this release. Preserved.

## Ratification (resolved 2026-08-02)

All eleven decision points were signed off on the war-room recommendation, and are now binding:

1. **D1 — ZTNA: DOCUMENT.** No dependency adopted in Beta-1; the deliverable is an enforced installer
   assertion, not a label.
2. **D2 — The active profile is a read-only system fact on `/v1/auth/me`**; no audit event, no new
   route.
3. **D3 — E-05 splits** into non-droppable **E-05a (`Button`, a defect fix)** and droppable **E-05b
   (`ConfirmDialog`)** — a recommendation-level divergence from the ratified catalog's single-epic
   framing.
4. **D4 — Paired sessions are durable and revocable**; the stateless short-TTL and revocable-request-
   only options are both rejected. The one declared ADR-0046 divergence.
5. **D5 — The paired session is narrowed to `approvals@1`** — a versioned, single-sourced,
   criteria-enumerated scope within `human-agent`; no new capability edge. Collapses E-02/S-08.
6. **D6 — Pairing mechanism**: one-time hashed secret, fragment-carried, 120s, single-use,
   three audit events and deliberately no `pairing.expired`.
7. **D7 — Hand-roll the QR encoder and the dialog**; no new npm dependency.
8. **D8 — Onboarding derives from live state** (no flag, no migration); the demo target is the
   installed host itself.
9. **D9 — Gate = WD-11 for pairing with a mandatory negative leg**; `wd1` proves `lan-only` and the
   profile refusal is unit-tested; **the §human leg is the first-run walkthrough on a fresh `wd1`
   install** — closing the un-seeded-state hole no existing gate covered.
10. **D10 — Pairing ships disabled by default**, 404 when off.
11. **D11 — Fragment discipline**: new named components only; `LimitParam` / `CursorParam` /
    `PageMeta` untouched; codegen drift zero. The last API growth before the E-06 freeze.

Everything else = the CEO-ratified Beta-1 path (`docs/requirements/epic_story_catalog_v4.md` §Part 4)
plus this consult's as-built review (findings 1–6, of which 2 and 5 changed a recommendation).
