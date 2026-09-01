# B-041 — remaining work to v1.0.0

**Analysis, 2026-09-01 (Commander seat).** ⚠ **This is a scoping INPUT, not a scope.** It decides
nothing and authorises nothing; v1.0.0 has no criteria document and is not scoped.

**Question answered:** what actually stands between today (`v0.15.0`, tagged `53782dd`) and a v1.0.0
whose flagship is B-041 — *governed harness integration, layer 2*.

---

## 1. Where B-041 actually stands

| | |
|---|---|
| Tier / status | **P0 · NOT-STARTED** [read: `docs/requirements/backlog_v4.csv:44`] |
| Target | **v1.0.0**, with **its own ADR + WD-13** |
| Depends on | **B-040 containment** — *deferred, unscheduled* |
| Ships as | **COOPERATIVE** governance while B-040 is deferred, and the backlog says the limitation **must be stated in the v1.0.0 criteria** |

**Nothing has been built.** There is no ADR, no WD-13, no criteria document, and no code. This is a
release-sized build starting from zero, not a finishing pass on existing work.

---

## 2. ⚠ A contradiction in the planning record that must be resolved first

`docs/requirements/epic_story_catalog_v4.md` contains **both** of these, twelve lines apart:

> *"So layer 2 may proceed without containment, on one condition: the limitation is STATED."*

> *"**Containment is the PREREQUISITE for layer 2, not a parallel track.** You cannot safely govern a
> third-party harness that is not contained — the harness would have to *choose* to call us. That
> dependency forces v0.14 before v1.0; it is not a preference."*

**The second is stale pre-split text** — it argues for an ordering ("forces v0.14 before v1.0") that
history has already overtaken: v0.14.0 shipped as *Governed Tool Surface*, not containment. The
ratified position is the first one, and `CLAUDE.md` records it: containment is deferred, not
cancelled; **it is not a build blocker**; layer 2 may proceed **provided the limitation is stated,
not drifted into**.

⚠ **Left as-is, anyone scoping B-041 from that paragraph concludes the work is blocked.** Filed as
**DOC-17** in the v0.16.0 criteria. **Resolve it before v1.0.0 scoping**, not during.

---

## 3. What "cooperative, not enforced" actually costs — the honest version

This is the single most important thing for the v1.0.0 criteria to state plainly, because it bounds
what the release may claim.

**Every governance guarantee this product ships today rests on the agent reaching the outside world
only through the governed path.** For our own `mcp-gateway` that holds by construction — it is the
sole orchestrator and holds no cluster key (ADR-0060, SEC-21 proven).

**A third-party harness is different in kind.** OpenHands and openworker are general agent runtimes
that can execute code, open sockets, and spawn processes. Without containment, **a harness that
declines to route through us simply is not governed** — the audit chain will faithfully record the
calls it *chose* to make, and record nothing about the rest.

⭐ **So the v1.0.0 claim must be: "we govern what the harness routes to us, and the harness is
cooperating." NOT "we govern the harness."** Those are different products. The first is honest and
still valuable — it is the same posture the gateway already ships and already discloses. The second
requires B-040 and cannot be claimed until it lands.

⚠ **This is exactly the drift risk the epic catalog warned about**, and this project's record shows
how such claims soften: the war-room's own #1 ranking was reversed at the v0.14 checkpoint precisely
because scoping pressure corrupts them.

---

## 4. The two reference integrations — what is known, and what is unverified

Both were chosen because they expose block-and-wait as a **first-class remote protocol**, so
integration is a *client*, not a patch — which is what makes "govern, don't fork" possible.

### OpenHands
- Seams: `POST /confirmation_policy`, `POST /security_analyzer`, `POST /respond_to_confirmation`.
- ⚠ **The `OpenHands/OpenHands` repo has PIVOTED to an Electron control centre**; the agent now lives
  in `OpenHands/software-agent-sdk` (MIT). **Any intake older than that pivot points at the wrong
  repository.**

### openworker
- Seams: `GET /v1/inbox`, `POST /v1/inbox/{id}/resolve`.
- **Worth reading as prior art:** its `permissions.py` rejects any command containing shell operators
  *before* prefix-matching argv — because `git status` would otherwise auto-approve
  `git status && rm -rf ~`. ⭐ **That is a real bypass class we should test ourselves against**, and
  it is adjacent to a limitation v0.15.0 already ships: **arguments are not yet governable** — a rule
  matches `(subject, edge, action, target)`, never the command string.
- ⚠⚠ **Two default-on cloud egress paths — an AWS API-Gateway WSS relay and telemetry — that must be
  disabled for air-gap.** In a product whose defining claim is air-gapped operation, **adopting a
  dependency that phones home by default is the sharpest risk in B-041.**
- ⚠ **Maturity: 14k stars against 130 commits, 8 contributors, 3 weeks old.** Stars are not maturity.

### Studied but explicitly NOT adopted
- `nearai/ironclaw` — a competing governance plane whose `InvocationFingerprint` is the same
  anti-swap property as ours, but whose seams are Rust traits in a composition root: adoption means
  forking an 884-crate tree.

⚠ **All of the above is `[inherited:]` from the 2026-08-10 war room.** `references_v4.csv` says its
own verification is **README-level**, and that *any row gating real spend needs the deep pass first*.
**B-041 gates a whole release.** T-153 is the precedent for what that pass looks like and what it is
worth: it turned an inherited assumption into a measured number and changed the answer.

---

## 5. Remaining work, in dependency order

| # | Work | Why it is first / notes |
|---|---|---|
| **1** | ⭐ **A deep verification pass on both harnesses** — actually run them, exercise the block-and-wait seams, confirm the endpoints exist as documented, and **measure openworker's egress paths and prove they can be disabled** | Every claim in §4 is `[inherited:]`. **T-153's lesson: the cheap deep pass changes answers.** ⚠ Do this before the ADR, or the ADR is built on README-level intake |
| **2** | Resolve the **DOC-17** contradiction (§2) | Cheap; blocks clean scoping |
| **3** | **The B-041 ADR** — the integration architecture, the cooperative-governance boundary, and the containment dependency stated as a limitation | Doctrine: capability needs an ADR before it is built |
| **4** | **WD-13** — the gate, defined before the build | New governed capability genuinely earns a WD here (unlike v0.16.0) |
| **5** | **The integration itself** — a client against the block-and-wait seams, routing into the existing policy → approval → connector → broker chain | The whole point is that this reuses the shipped chain rather than adding a second one |
| **6** | **The air-gap demo** — operator-provisioned model + harness + governance, `lan-only`, disconnected | ✅ **Now viable**: T-153 measured that a runtime serves inference with the network severed [read: `docs/spike/inference-substrate-measurement.md`] |
| **7** | **The surface freeze** — v1.0.0 is where the OpenAPI contract freezes | ⚠ Its own body of work: everything the freeze binds must be *right*, because after it, changes cost |
| **8** | Capstone: WD-1..15 + WD-13, the §human pass, notes stating the cooperative limitation | Per every prior release |

---

## 6. Honest assessment of size

**B-041 is not one release-sized ticket; it is a release.** It carries a deep external-dependency
verification pass, a new ADR, a new WD, an integration against two third-party runtimes, an air-gap
demo, **and** an API surface freeze — any one of which has been a release theme before.

⚠ **The surface freeze alone deserves scrutiny.** v0.15.0 spent a ticket (T-151b) on *"the last
fragment change before the v1.0.0 freeze."* A freeze is a promise about a contract; making it in the
same release as a first third-party integration means **freezing a surface whose newest consumer has
existed for weeks**.

⭐⭐ **RATIFIED BY THE CEO 2026-09-01: the surface freeze is SPLIT OUT of the integration release.**

**v1.0.0 Beta 1 = B-041 integration, surface explicitly NOT frozen. The freeze lands at v1.0.0 final,
after the integration has actually been used.** The reasoning it was accepted on: freezing a contract
whose newest consumer has existed for weeks binds a promise to something nobody has run in anger.

⚠ **Three consequences that follow, and must not be lost:**

1. **The v1.0.0 Beta 1 criteria must state that the surface is NOT frozen** — as plainly as they state
   that governance is cooperative. An unstated non-freeze will be read as a freeze by anyone building
   against it, which is the same drift-by-silence this project has recorded before.
2. ⚠ **The "last fragment change before the freeze" framing changes.** v0.15.0 spent T-151b partly on
   that basis [read: `docs/releases/v0.15.0/v0.15.0-release-criteria.md`], and it was one of the
   original arguments for sequencing that release at all. **That argument was sound when made and is
   now superseded** — fragment changes remain possible through Beta 1. Recorded so a future reader
   does not find the two statements in conflict and assume one is an error.
3. **The counter-argument is accepted with the decision, not dismissed:** a Beta that freezes nothing
   may fail to force the hardening a freeze forces. **Mitigation — v1.0.0 final must carry its own
   freeze criteria**, not inherit an assumption that the surface settled by itself during Beta.

*(Original recommendation, retained for the record:)* consider
**splitting the freeze out of the integration release**. Ship B-041 as v1.0.0 Beta 1 with the surface
explicitly *not* frozen; freeze at 1.0.0 final once the integration has been used. That keeps the
version number meaningful — *"layer 2 is real"* — without binding a contract to a consumer nobody has
run in anger yet. **The counter-argument is real too:** a Beta that freezes nothing may fail to force
the hardening a freeze forces.

---

## 7. What is NOT in the way

Recorded because it is easy to over-estimate the gap:

- **The governance chain is built and proven.** Policy → approval → connector lease → broker exec →
  hash-intact audit has shipped since v0.3.0 and has been extended, not replaced, ever since.
- **The decision plane is real** as of v0.15.0 — operator-authored rules and a machine `deny` before a
  human is asked.
- **The inference question is answered.** It was the largest open unknown in the demo story a day ago.
- **B-040 is not a blocker** — only a limitation on what may be *claimed* (§3).

**The distance to v1.0.0 is one substantial, well-understood release — not an unknown.** The main
risks are external-dependency verification (§4) and freeze timing (§6), both of which are cheaper to
resolve *before* scoping than during it.
