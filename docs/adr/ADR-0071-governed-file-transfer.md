# ADR-0071: Governed file transfer — widening the egress tool surface without widening the trust boundary

## Status

**Accepted** — 2026-08-19 (T-137, the v0.14.0 "Governed Tool Surface" opening consult). Decision
points D1–D9 signed off on the Commander recommendation after a pre-authoring audit that **changed
the release scope twice**: once by finding that the Run engine cannot express a non-exec step, and
once, during authoring, by finding that the SEC-16 back-compat rule would have granted file powers to
every existing agent token. Author/decider: CEO (war-room). Binding spec for T-138…T-144; code is
authored just-in-time per ticket from here.

**No divergence from any Accepted ADR — G2 is clean.** This ADR *extends* ADR-0057's typed tool
catalog with a second egress family, *reuses* ADR-0058's broker egress plane and ADR-0053's connector
lease unchanged, and *narrows* ADR-0059's informed-approval semantics for one case, which D3 states
explicitly rather than leaving to implementation.

---

## Context

### The diagnosis

Six releases deepened the governance spine — audit chain, policy, approvals, credential broker, MCP
gateway, Runs/Workstreams, memory and context. Every one of them wrapped further control around the
same single outward capability: `remote.exec` over SSH.

The gateway registry holds six tools, but five of them (`run.launch`, `workstream.poll`,
`plan.submit`, `memory.read`, `context.search`) are **control-plane** — they orchestrate this product.
**Exactly one tool reaches the outside world.** That asymmetry, not any missing control, is what makes
the product hard to do real work with, and it is why no walkthrough has ever had a real user: one
command at a time is not enough to operate a host.

The most conspicuous absence is file transfer. `grep -rn "sftp\|scp"` over the repository returns
nothing. An agent governed by this product can run `cat /etc/nginx/nginx.conf` and read the output out
of stdout, but cannot fetch the file, cannot put one back, and cannot list a directory.

### What the pre-authoring audit found

Four findings, verified in code at this consult, not inherited:

1. **The governed pipeline is genuinely plane-agnostic — except at its last step.**
   `runGovernedStep` (per-initiator concurrency cap → `requires_approval` lookup → invocation create
   with idempotency → `policy.Decide` → park and block-and-wait → terminal outcome) contains nothing
   exec-specific. The coupling is confined to `execAndFinish`
   (`services/mcp-gateway/internal/catalog/catalog.go:870`, 28 lines) and the single-method `Broker`
   interface (`:340`). **The seam is real and it is small.**

2. **The bounding machinery already generalizes at zero cost.** SEC-16 agent-token scope
   (`packages/go/auth/auth.go:41`, `Operations []string`) and delegation-grant verbs
   (`services/api/internal/grant/grant.go:36`, `Verbs []string`) are free-form string arrays, stored as
   text arrays, **validated against no closed set anywhere in the repository**. New operation verbs
   need no migration and no schema change. ADR-0057 and ADR-0062 were built for this.

3. **The Run engine cannot express a non-exec step.** `services/api/internal/run/launch.go:38` defines
   `const remoteExecVerb = "exec"` and `:83` gates every plan on
   `slices.Contains(g.Verbs, remoteExecVerb)`. A plan step is `{resourceId, command}`. Generalizing it
   makes a step a sum type, which changes `run.launch`'s input schema **and** `/v1/runs` — the latter
   being in the OpenAPI fragment. **This is what D6 scopes out.**

4. **The evidence model is not merely compatible — it is the same code path.** The broker's exec
   handler already records `Target invocation:<id>, Evidence sha256:<stdout>`. ADR-0049 fixed the
   content-free ingest rule (`evidencePacketId = sha256:<bytes>`, never raw bytes in the chain) five
   releases before anything needed to transfer a file. A file operation is bytes with a digest.

### The finding that surfaced during authoring, and changed a decision

**SEC-16's back-compat rule is permissive by design, and would have leaked authority.**
`catalog.go:459` gates a tool on the agent token's scope **only when the token declares operations**:

> `if len(capb.Scope.Operations) > 0 && !slices.Contains(capb.Scope.Operations, def.Operation)`

An empty `Operations` list therefore means *unrestricted*, deliberately, so that resource-only scoped
tokens minted before SEC-16 kept working. That was correct when `exec` was the only operation. It
stops being correct the moment a second egress operation exists: **every existing agent token, and
every future token minted without an explicit operations list, would silently acquire the ability to
read and write files on any resource in its scope.**

No test would have caught this. It is the same class as the recurring defect this project keeps
finding — a control that is correct on its own terms, and a caller whose assumptions have moved. D7
closes it fail-closed.

---

## Decision

### D1. The family: three tools on the existing egress edge

| Tool | Operation (SEC-16 verb) | Action (policy + audit) | Effect |
|---|---|---|---|
| `remote.file.read` | `file.read` | `remote.file.read` | Read one file from a governed resource; returns content plus its `sha256` |
| `remote.file.write` | `file.write` | `remote.file.write` | Write bytes to one path on a governed resource |
| `remote.file.list` | `file.read` | `remote.file.list` | List one directory (names, sizes, modes, mtimes) |

All three carry `Edge: "agent-remote-session"` — **the same capability edge as `remote.exec`**, because
they cross the identical trust boundary: authenticated SSH access to a governed resource, using the
same credential leased from connector-service. Minting a new edge would imply a new boundary where
there is none, and would require a change to `docs/security/agent-permission-model.md`'s seven-edge
model for no security gain.

**`file.list` deliberately shares the `file.read` verb.** Listing a directory is a read of the
filesystem; splitting it would give operators a third checkbox that no threat model distinguishes.

`remote.file.write` is the only member that mutates a target.

### D2. The seam: generalize the terminal step, change nothing above it

`execAndFinish` becomes operation-dispatching, and `Broker` gains file methods alongside `Exec`.
Everything above it — the concurrency cap, the approval park, the idempotency key, the fail-closed
rule (SEC-19), the audit brackets — is **untouched**.

**T-138 ships this refactor with no new tool**, and its proof is that the existing suite stays green:
a pure seam extraction whose behaviour is unchanged is verifiable precisely because nothing about
`remote.exec` may move. Any test change in T-138 is a signal that the refactor was not neutral.

### D3. Informed approval for a write is **path, size and digest — not content**

ADR-0059 established that an operator must not approve blind, and made the invocation persist the
`command` so `<ApprovalPrompt>` could show it. A file write cannot honour that literally: no operator
meaningfully reviews four kilobytes of configuration in an approval prompt, and pretending otherwise
would make the prompt a formality.

**A file operation renders a human-readable descriptor into the existing `command` field**, for
example:

```
remote.file.write /etc/nginx/nginx.conf (4,218 bytes, sha256:9f2c…a41b, mode 0644)
```

The operator approves **the intent and the digest**, not the bytes. Consequences, stated rather than
buried:

- The whole existing path is reused unchanged — persistence, the F3 at-rest command encryption
  (ADR-0060), the approval prompt, the parked-invocation lifecycle, the audit brackets. **Zero schema
  change, zero migration, zero fragment change.**
- The digest is what makes the approval binding: the bytes executed must hash to the digest approved.
  **An implementation that re-reads or re-renders the content after approval breaks this ADR.** The
  approved-command-swap attack closed in v0.3.0 is the precedent, and the capstone must re-prove it
  for this path.
- **This is a genuine narrowing of "informed"** and it is the honest one. It is the same position
  ADR-0049 already takes for session evidence: the chain carries the digest, the content lives
  elsewhere under access control.

### D4. Transport: SFTP over the existing lease, in the broker

The broker gains a `POST /v1/file` sibling to `/v1/exec`, following its shape exactly: load resource
coordinates → lease the credential from connector-service → perform the bounded operation → retain the
artifact and audit (`Target invocation:<id>`, `Evidence sha256:<bytes>`) → respond. It carries the same
R5-ext subject pin (`capb.Subject != "mcp-gateway"` → 403) and the same service-service edge.

Implementation uses `github.com/pkg/sftp` over the broker's existing `golang.org/x/crypto/ssh`
connection. The broker remains the **sole SSH egress** (SEC-18); no new egress path is introduced, so
CLAUDE.md §Forbidden work is not engaged.

**Rejected: file transfer over the existing exec channel** (`cat`, `base64`, shell redirection). It
needs no new dependency and no broker work, and was seriously considered for exactly that reason. It
is rejected because it would put shell strings in the audit chain where typed file operations belong,
has no natural size bound, depends on which coreutils the target happens to ship, and — decisively —
would make the D3 approval descriptor a lie, since what the operator approved would be a shell command
and what ran would be a shell command with different failure modes.

### D5. Bounds, fail-closed

- A configurable **maximum transfer size**, defaulting to **1 MiB**, enforced on both directions.
- The read path refuses a file above the cap rather than truncating it. **This is deliberately
  different from `remote.exec`**, which truncates stdout and flags `Truncated`: a truncated command
  output is still evidence of what the command printed, whereas a truncated file is a corrupt file and
  a digest that matches nothing.
- Per-operation timeout, mirroring exec's.
- Over-cap, timeout, permission-denied and no-such-path all fail closed (SEC-19) and mark the
  invocation `failed`.

### D6. Scope: the agent-direct path only. Runs stay exec-only.

File tools are reachable through the gateway's `Invoke` path. **`run.launch` plans, delegation-grant
plan validation, and `/v1/runs` are untouched in v0.14.0.**

This is a scope decision, not an oversight. Making a Run step polymorphic changes `run.launch`'s input
schema and `/v1/runs`'s step shape; the latter is in `docs/api-contracts/openapi.v3.fragment.yaml`,
and a zero-fragment-change release is most of why this release is cheap. It is also a second design
problem — a plan step becomes a sum type, and the grant check at `launch.go:83` has to bound verbs
per step-kind rather than per plan.

**Deferred with its trigger stated in §Deferred.** Grants may already carry `file.read`/`file.write`
verbs (D7 requires it for the direct path); those verbs simply have no effect on Run plans yet, and
the criteria must not claim otherwise.

### D7. **File operations require explicit scope. The empty-operations back-compat is `exec`-only.**

The SEC-16 check becomes: an empty `Operations` list continues to mean *unrestricted* **for `exec`
alone**. Every other operation verb, including all three introduced here, requires its verb to be
present explicitly in the token's scope.

Rationale: the back-compat allowance exists to keep pre-SEC-16 resource-only tokens working. It was
never a decision that future capabilities should be granted by default, and reading it that way would
mean **every agent token in existence silently gains file read and write on every resource in its
scope** the moment T-140 merges.

Fail-closed and surgical: no existing `remote.exec` caller changes behaviour, and no file operation is
reachable without an operator explicitly minting a token that names it.

**This must be proven by a negative test, not asserted** — a token with an empty operations list must
be **rejected** for `remote.file.read` and **accepted** for `remote.exec`, in the same test, so the
distinction itself is what is under test.

### D8. Evidence and audit

- Every file operation is an invocation, with the same brackets as exec (`tool.invoked` /
  `tool.result` on the `agent-tool` edge) and the same `invocation:<id>` anchor.
- The broker records `Evidence sha256:<bytes>` for the transferred content, reusing ADR-0049's
  content-free ingest. **Raw file content never enters the audit chain**, in either direction.
- A write records the digest of what was written; a read records the digest of what was read. The
  descriptor persisted at D3 already carries the digest, so an auditor can bind approval to outcome
  without either record containing the bytes.

### D9. Zero OpenAPI fragment change

Gateway tools are an MCP surface, not a REST surface, and are deliberately absent from the fragment
(the same reason connector lease and policy decide are absent: internal or non-REST planes stay out).
Adding tools therefore touches no generated code and cannot drift `task codegen`.

The only fragment-adjacent surface in this release is the operator UI's rendering of a file
invocation, which reads fields that already exist.

**VER-2 (zero codegen drift) must still be asserted at the capstone**, because "we did not intend to
change the fragment" and "the fragment did not change" are different claims.

---

## Security posture

**What this release does not change.** The trust boundary is identical: the same edge, the same
resource registry, the same credential custody in connector-service, the same sole-SSH-egress broker,
the same policy decision point, the same approval primitive, the same chained-hash audit. No new
daemon, no new network path, no new key, no new datastore.

**What it does change.** The blast radius of a compromised agent token that *names* the file verbs now
includes reading and writing files on resources in its scope, where before it included running
commands. Given `remote.exec` already permits `cat` and shell redirection, this is a **narrowing of
the gap between what the audit trail describes and what the agent can do**, not a widening of
capability: the same actions become typed, bounded, digest-anchored and separately grantable instead
of hiding inside an opaque command string.

**Residuals, stated:**

- An operator who approves a write approves a digest, not content (D3). A malicious plan can still
  produce content whose *digest* the operator has no way to evaluate. The mitigation is that the
  descriptor names the path and size, and that the write is separately grantable — not that the
  operator has read the bytes.
- `remote.exec` remains able to do everything the file tools do, less legibly. This release does not
  and cannot close that; only containment (B-040) constrains what an approved command may do once it
  runs.
- The 1 MiB default is a resource-safety bound, not a security control. An agent with the write verb
  and many approvals can write many files.

---

## Consequences

### Positive

- The product gains a real second capability, on the existing spine, with no new architectural risk.
- Dogfooding becomes possible: exec plus file transfer is the minimum set for actually operating a
  host, which is the standing blocker on every walkthrough having a synthetic user.
- The seam from D2 is the reusable artifact. A third egress family (process control, service status,
  host metrics) costs a `ToolDef`, a resolver, and a broker leg — not a refactor.
- Typed file operations make the audit trail more legible than the shell strings they replace.

### Negative

- One more dependency (`github.com/pkg/sftp`) in the offline bundle and in `task licenses`.
- `execAndFinish` and `Broker` are touched, which is the most governance-critical code in the product.
  This is why D2 ships as its own ticket with an unchanged-behaviour proof.
- Three tools mean three input schemas, three resolvers and three sets of failure modes to audit.
- D7 is a behaviour change to the SEC-16 gate. It is fail-closed, but it is a change, and it must be
  tested in both directions.

---

## Relation to prior ADRs

- **ADR-0057** (tool-catalog schema, tool→edge mapping, invocation-anchored governance) — extended.
  This is the second egress family its `ToolDef` shape was designed to admit.
- **ADR-0058** (broker non-interactive exec egress) — extended with a sibling leg on the same plane,
  same subject pin, same statelessness, same audit anchor.
- **ADR-0059** (informed invocation approval) — **narrowed for one case, explicitly, at D3.** The
  principle (never approve blind) is preserved; the literal mechanism (show the command) is replaced
  by a descriptor for operations whose payload is not reviewable.
- **ADR-0049** (audit content model: hash-in-chain, content-free ingest) — reused as-is; D8 is an
  application of it, not an amendment.
- **ADR-0053** (connector-service as sole credential custody) — untouched; the file leg leases exactly
  as exec does.
- **ADR-0060** (asymmetric per-workload keys; F3 command-at-rest encryption) — the D3 descriptor is
  stored in the same encrypted `command` field and inherits its protection.
- **ADR-0062** (delegation grants) — verbs generalize for free; D6 scopes their *effect* on Run plans
  out of this release.

---

## Deferred (recorded)

| Item | Promotion trigger |
|---|---|
| **File steps in Runs and Workstreams** (polymorphic plan step; per-step-kind grant verb check at `launch.go:83`; `run.launch` + `/v1/runs` schema) | The first ask for an unattended multi-step plan that moves a file — or v1.0.0, whichever is first, since it changes the fragment and the freeze lands there |
| Recursive or glob transfer, directory upload | A concrete operator need; deliberately out of a release whose point is a narrow proven seam |
| Resume, chunking, or transfers above the cap | An operator hits the 1 MiB bound in real use during dogfooding |
| File operations in the operator cockpit UI (not just approval rendering) | B-037's design pass, or a §human finding |
| A `host.metrics` / process-control family on the same seam | The beszel assessment (references `v6` batch) resolves to a governed tool rather than a sidecar |

---

## Ratification (resolved 2026-08-19)

Five decision points were put to the CEO at the v0.14.0 criteria consult and signed off as proposed:
the Run-coupling scope (D6), the three-tool set (D1), the approval narrowing (D3), the bounds (D5),
and the gate sizing (no new WD — carried in the release criteria, §2). D7 was added during authoring
after the SEC-16 back-compat finding and is the one decision here the consult did not anticipate; it
is fail-closed and reduces authority relative to both the current code and the proposal, which is why
it is recorded as an ADR decision rather than re-opened as a question.
