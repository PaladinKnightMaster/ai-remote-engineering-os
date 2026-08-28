# ADR-0073: The product boundary — the ledger defines membership

## Status

**Accepted** — 2026-08-27 (CEO ruling, ecosystem-architecture session, post-v0.14.0; held
deliberately separate from v0.15 scoping so the boundary was not drawn around whatever fits a
release). Author: War Room Commander; ratified by the CEO after three rounds of adversarial review
that found eight defects in the draft, five of them in claims about the as-built.

**Supersedes nothing. Edits no Accepted ADR** (G2-clean). It supplies the *membership test* that
**ADR-0002** (Accepted, never superseded) assumed but never stated.

---

## Context

### The question, and why it is forced now

ADR-0002 split the product into Cockpit / Agentic Desktop / Work Board, with Knowledge Base and Code
Forge as future modules. It said *what* the pieces are. It never said **what makes something a
piece** — so for fifteen months there has been no test, and without a test the boundary drifted:
`docs/executive/vision.md` (canonical read item #1) describes **one combined product**; the module
map lists **fourteen**; the live planning record uses a third grouping under the same word "layer."

Five ecosystem tracks are now queued at once — model runtime, execution sandbox, harness/IDE,
knowledge base, work board. **Without a membership test each of them gets decided at the moment it is
scoped, which is the moment least able to decide it well.** That is the specific failure this
session was split off to prevent.

### What the as-built actually is — verified 2026-08-27, not inherited

- **Zero model, inference, or embedding dependency.** Read from every `go.mod` direct-require block
  (both the `require (...)` and single-line `require X v` forms): **ten** third-party modules across
  ten `go.work` modules — six services, three shared packages, and `tests/e2e`. Nothing else.
- **No first-party agent client.** ONB-6 / S-16 was **CUT** at v0.12.0 (T-119).
- **`requirements_v4.csv` REQ-006 asserts *"the platform runs no models by doctrine"* — and there is
  no ADR behind that sentence.** It contradicts **ADR-0007** (*"Support … internal LiteLLM/vLLM/
  **Ollama** endpoints and local fallback"*), **ADR-0021**, **ADR-0027** (*"local AI setup profile
  with LM Studio/Ollama/Continue"*) and **ADR-0040** (*"escalate from **local model** to frontier"*),
  all Accepted, none superseded. **A fourteen-release G2 divergence, never declared.**
- A partial re-cut *was* recorded — in `references_v4.csv`'s `CLUSTER-MODELS` cell, citing a
  *"ratified mission"* document that, searched across the repo, the workspace and session memory,
  **does not exist.** Doctrine cannot live in a CSV cell citing a phantom.

### The one thing we have that nobody else does

The audit chain is a single global chain; **ADR-0069** signs its heads with Ed25519, and
**`aireos-verify`** verifies an export with **no database, no network, and no configuration**.

⚠ **Corrected during this ADR's own review, because the first draft asserted the opposite.**
`audit-service` **does hold its own signing key** — ADR-0069 §D5 gives it one *"for the first time"*
and defends in-service signing on the ground that the service already holds the symmetric cluster
key, so the incremental blast radius is small. *"A key the writing service does not hold"* was
**S-28's goal, not what ADR-0069 accepted.** The property below is therefore **not separation of
duties**; it is the narrower thing ADR-0069 §Context actually claims — verification **"by an outsider
who holds no database access."** Still unusual, but it is unforgeable *by outsiders*, not by a
compromised `audit-service`, and ADR-0069 records that residual deliberately.

The v7 reference pass (17 sources fetched and read 2026-08-27, `references/intake-v7-2026-08-27.md`)
measured this against the current field:

- **`CopilotKit/OpenBot`** (MIT, 3.2k★) governs the same seam we do — isolated environment per agent,
  *"every action decided before it happens and recorded after,"* CEL policy, audit trail. **But it
  has no cryptographic chain, and *"true offline operation is not possible with default
  configuration."***
- **`apache/maka`** (ASF incubating, 3,949 commits) keeps an **append-only log** and requires approval
  when a tool **crosses the sandbox boundary**. Also no signed chain, and **macOS-only**.
- **Across all sixteen repos, not one is both governed and air-gapped.**

**So the differentiator is not that we log. It is that our log is checkable, offline, on a
disconnected network, by an outsider who holds no database access.** That is a test, and a test can
define a boundary. **Phrased narrowly on purpose** — the correction above states why anything broader
claims more than ADR-0069 shipped.

### Independent convergence on the rule below

`maka`: *"does not bundle a shared model connection; users must provide their own."*
`OpenBot`: *"no model ships in the box."*

**Two projects reached the substrate rule below independently, one of them at the ASF.** This is not
our idiosyncrasy; it is becoming the category norm.

### The escalation precedent already exists in this repo

**ADR-0051** required host postgres as a guided pre-step with staged offline packages. **ADR-0052**
escalated it to per-OS companion artifacts — and it fired on a **measurement**: the §OI-c probe found
the dependency closure was **80–85 MB per OS against the 150 MB OI-4 cap**. A number decided it, not
a preference. That is the shape Decision §3 reuses.

---

## Decision

1. **Membership test.** A component is **in the ecosystem** if and only if its consequential actions
   are recorded on the **one signed evidence ledger**, in a form an outsider can verify **offline,
   holding only an export and a public key — no database access, no network, no configuration**.
   Logging is not the test; **externally provable logging is.** Everything that meets it is a member
   and may carry the product's governance claim.

   ⚠ **The test is external verifiability, not separation of duties.** Per ADR-0069 §D5,
   `audit-service` signs with a key it holds, so a compromise of that service remains a stated
   residual. §1 asks that evidence be **checkable by an outsider**, not that it be **unforgeable by
   an insider**. Do not let a later document quietly upgrade the claim.

2. **Substrate.** Anything the system needs that cannot meet §1 — an **inference** runtime, an
   **execution** sandbox, a third-party **agent** harness or IDE — is a **substrate**. Substrates are
   **governed at their seam, operator-provided by default, and never merged into the core.** The
   core's dependency surface stays small enough to read; that auditability is a load-bearing
   property, not an accident.

3. **Escalation.** The platform may ship a substrate itself only after **(a)** a feasibility
   **measurement** in the shape of ADR-0052's §OI-c probe, and **(b)** its own ADR. **Preference,
   demand, or enthusiasm is not a trigger; a number is.** Candidates are recorded, not adopted —
   inference: `magnitude` (Apache-2.0, offline after download, harness-compatible); execution:
   `matchlock` + `iron-proxy` (B-040, deferred); agent: adapters per **ADR-0015**, never a fork.

4. **Air-gap is a property of the system, not of the product.** Any claim of air-gapped or
   offline-complete operation must **name every artifact the deployment requires**. Claiming it for
   the core bundle alone is a documentation defect, gated by the release checklist.

**What this settles, as consequences rather than separate decisions:** the unsupported doctrine line
is replaced by §2 (*the platform runs no models in-process; it governs a runtime beside it*);
ADR-0007/0021/0027/0040 are discharged by §2+§3 rather than superseded; B-040's deferral becomes a
§3 escalation whose trigger has not fired; and the CEO's Ollama question is answered **yes** at the
seam (§2) and **not yet** in the bundle (§3).

**The contradicting text, corrected in the same change that accepts this ADR — every location swept
and verified 2026-08-27, not assumed:**

| File | Instance | Action |
|---|---|---|
| `docs/requirements/epic_story_catalog_v4.md`:12 | *"the platform runs no models"* in the **doctrine constraints that bind every story** | Highest impact — takes the §2 wording |
| `docs/requirements/requirements_v4.csv` | REQ-006 evidence: *"the platform runs no models by doctrine … the 'models' clause is deliberately not met"* | Takes the §2 wording; the "not met" verdict is withdrawn |
| `references/references_v4.csv` | `CLUSTER-MODELS` cites a *"ratified mission"* that does not exist | Citation replaced with this ADR |
| `references/reference-audit-v4.md`:97 | discusses the *"runs no models"* line | Annotated, not rewritten — it is a dated analysis |
| `references/intake-v4-2026-07-23.md` | applies *"runs no models"* as its doctrine filter | **Not touched** — archival, per ADR-0072 §5 |

⚠ **Correction of record.** An earlier draft of this ADR located the contradicting text in
`requirements_v4.csv`, `CLAUDE.md` and `module-map.md`. **Two of those three were wrong**, and it
missed the highest-impact instance above. `CLAUDE.md` never carried the phrase; `module-map.md`:77
carries only the narrower and still-true *"zero model dependency; the plan author is the customer's
external MCP client."* **Naming a location without reading it is the recurring "a correct control,
and its caller was never checked" defect — committed here, inside the clause written to remedy an
instance of it.** The table above is the swept result.

---

## Consequences

### Positive
- **ADR-0002 becomes usable.** The split stops being a list and becomes a rule that decides new cases,
  including the five queued tracks, without a war room each time.
- **A fourteen-release undeclared G2 divergence closes**, and the doctrine line moves out of a CSV
  cell into a decision record.
- **The claim we defend gets narrower and truer**: not "we govern agents" — which OpenBot and maka
  also do — but "we govern them where nothing else works, and an outsider holding no database access
  can check the record."
- **The core's ten-module dependency surface is protected by rule**, not by habit — and since
  2026-08-28 by `task go-deps-check`, which fails on any unallowlisted direct dependency.

### Negative — including the parts the CEO will not like
- **It defers Ollama-in-the-box behind a measurement**, and the measurement has not been taken. The
  honest position for now is *point at your own runtime*, which is a weaker demo than *click to run*.
- **It formally accepts shipping a deliberately incomplete system** and saying so. "Copy one bundle
  to a disconnected host" becomes "copy the bundle and bring a substrate." ADR-0052 already made this
  trade once for postgres; this is the second time, and a third would make the offline story genuinely
  awkward.
- **§1 is a demanding test that our own future products must pass.** A knowledge base or work board
  that merely stores data does not qualify — it has to write provable evidence. **That is more
  engineering per pillar than a conventional wiki or board would cost**, and it is the price of the
  claim.
- **§3 will feel like an obstacle exactly when enthusiasm is highest.** It is designed to.
- **It leaves ADR-0015 (adapters, never forks) as the only thing holding the harness line.** Its
  sibling ADR-0010 (integrate forges before cloning) had been Draft for fifteen months and was
  **resolved to Accepted on 2026-08-27, in this same session, precisely because this ADR exposed the
  dependency**. That closes the forge half; the *harness* half still rests on one ADR, and if
  ADR-0015 is ever reopened this boundary loses its only structural defence.

### Held in reserve
- **Governed model egress** — intercepting inference at a policy decision point (`SWITCHYARD` shape;
  pre-alpha, do not adopt). This would upgrade a substrate to a member under §1 and needs its own ADR.
- **A graduated approval ladder** — `munder-difflin`'s *steer → constrain → stop*. Our approval is
  binary; the "constrain" rung is the missing middle and speaks to residual R1.
- **A swappable sandbox abstraction** (Vercel Academy's local / in-memory / remote behind one
  interface) — the implementable shape of §2 for the execution class whenever B-040 re-enters.

---

## Relation to prior ADRs

- **ADR-0002** — supplies the membership test 0002 assumed. 0002 is not edited and not superseded.
- **ADR-0007 / 0021 / 0027 / 0040** — discharged by §2+§3. This is the first document to say *how*
  they are satisfied without putting a model inside the core.
- **ADR-0051 → ADR-0052** — the escalation precedent §3 reuses, including that a measurement fired it.
- **ADR-0069 / ADR-0070** — the ledger's signed heads and offline verifier are what make §1 a real
  test rather than a slogan. Without them §1 is unenforceable.
- **ADR-0015** — preserved and explicitly load-bearing: §2 places harnesses in the agent substrate
  class, integrated by adapter, never forked.
- **ADR-0001** — §4 is what makes 0001 true of the deployed *system* rather than only of our software.
- **ADR-0031 / ADR-0056** — untouched. MCP remains the tool seam; this ADR adds no tool, edge, or
  fragment change.

## Review triggers

Re-open if any of these becomes true. **Each is observable** — ADR-0041's triggers could not fire on
toolchain age, ADR-0072 exists because of that, and this section is written not to repeat it.

- **A release claims air-gapped or offline-complete operation without naming every required
  artifact.** §4 is then violated in shipped documentation; the release-checklist assertion is the
  trigger.
- **A substrate is proposed for shipping without a completed feasibility measurement.** §3 is being
  bypassed. The measurement's absence is itself the trigger.
- **The core acquires an inference, embedding, or model-serving dependency.** §2 is being bypassed.
  ⚠ **No existing gate catches this, and the first draft of this ADR wrongly said one did.**
  `tools/forbidden-deps.mjs` inspects **`apps/*/package.json` for JS meta-frameworks only** (ADR-0047
  §Verification) and cannot see a Go module; `tools/forbidden-features-check` is a **path-existence**
  check for ADR-0046's forbidden features, not a dependency check. **Making this trigger fire
  mechanically requires a new check** — a direct-require allowlist over every `go.mod` in `go.work`,
  in the shape of the existing two tools. Until that exists **this trigger is manual**, and saying so
  is the point: an unfireable trigger is the ADR-0041 defect, and naming a gate that does not cover
  the case is the recurring "the caller of a correct control was not updated" class.
- **A first-party agent harness, client, or IDE is proposed.** §2 and ADR-0015 must be re-examined
  *together*: a platform that both authors plans and runs models is a different product from the one
  this ADR bounds, and that line should be crossed deliberately, never incrementally.
- **A competitor ships externally-verifiable offline evidence.** §1 is our only unmatched property;
  if it stops being unmatched the boundary is drawn around nothing and the whole ADR needs re-deriving.
  The v7 pass established the current baseline: **nobody had it on 2026-08-27.**
- **B-041 or any later layer-2 work cannot complete its walkthrough in the `lan-only` profile.** That
  is §4 failing in the field, and it fires whether or not a substrate ever shipped.
