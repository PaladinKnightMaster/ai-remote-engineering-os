# ADR-0059: Informed invocation approval — command visibility & parked-invocation lifecycle

## Status

**Accepted** — 2026-06-15 (ratified in the T-040 war-room consult). Part of the
v0.3.0 "Typed MCP Gateway" release (`docs/evaluation/v0.3.0-release-criteria.md`).
Binding spec for the invocation-approval ticket (T-040a/b/c).

**Builds on / conforms to:** ADR-0054 (the policy/approval primitive +
`<ApprovalPrompt>`), ADR-0057 (the gateway invocation + operator approval via
`services/api`), ADR-0058 (broker non-interactive exec + the retained-stdout
content model), ADR-0049 (access-controlled artifact custody; content-free audit
ingest), ADR-0050 (the operator auth/CSRF model on `services/api`). **No ADR
divergence (G2-clean):** it *extends* ADR-0057's approval surface and edits no
Accepted ADR. It supersedes a **non-normative migration comment** in
`services/mcp-gateway/migrations/0001_invocations.up.sql` ("the command text is NOT
stored here"), which was an implementation note from T-039a, not an ADR decision.

## Context

ADR-0057 §4 routes a parked invocation's approval to the operator (reusing
`<ApprovalPrompt>`), but **neither it nor ADR-0058 specifies what the operator
sees**. As built in T-039a, the `invocations` table stores **metadata only** — the
command flows gateway→broker in-memory at exec time and is persisted only
*post-exec* as the broker's `<invocationId>.exec` artifact (ADR-0058 §5). That
leaves the operator approving `remote.exec` **without the command** — the exact
payload that carries the risk (it executes an arbitrary command on a real host).

Informed human consent is the entire purpose of the approval primitive (project
doctrine: *"no AI autonomy before audit + policy + approval primitives exist"*;
CLAUDE.md core rule *"Fail loud"*). Approving an `(agent, resource-id)` pair with
the command invisible is not informed approval — it is approval theater on the one
action the release exists to govern. The v0.3.0 audit (T-040 consult) surfaced this
as **finding F1 (critical)**.

A second, coupled question: once the command is persisted at park time, **how long
does a parked invocation (and its command) live** if no operator ever resolves it?
The gateway owns invocation lifecycle (ADR-0057 §2) but T-039 added no finalizer
for the parked state, so the approval window — and the command's exposure — must be
bounded.

## Decision

1. **Persist the command on the invocation.** The `invocations` row gains a
   `command` column, written at **park** (`store.Create`). It is the single source
   the operator approval surface reads. The broker's retained `<invocationId>.exec`
   artifact (ADR-0058 §5) remains the *post-exec* forensic anchor; this is the
   *pre-exec* approval payload. No downstream contract change — the command was
   already flowing to the broker at exec (ADR-0058 §3).

2. **Operator-only read; never to agents; never logged.** The command is exposed
   **solely** through the `services/api` operator pending-invocations read
   (`human-agent` gate; CSRF on the resolve mutation per ADR-0050), proxied to the
   gateway's **internal service-service** list endpoint. It is **never** returned on
   the agent (`agent-tool`) plane and **never** written to logs. It is treated as
   potentially secret-bearing (a command may carry inline credentials) — the same
   posture the broker already applies to retained stdout (ADR-0049
   access-controlled artifact, content-free chain).

3. **`<ApprovalPrompt>` is generalized additively.** The presentational component
   (`packages/ui`, T-031c) gains **optional** props (`command`, `agent`, and a
   generic `title`/`description`) whose defaults preserve the v0.2.0 session copy —
   reuse without a breaking change for the existing session consumer (AP-3/FE-11).
   The operator approves against `{agent, resource, command}`. The change carries
   the DoD addendum (browser-rendered evidence) + a design-system preflight.

4. **The parked-invocation lifecycle is bounded — a gateway reaper.** A
   serve-time reaper (the `services/api` `ReapStale` pattern, T-032) finalizes a
   never-resolved parked invocation to **`failed`** past a TTL (≥ policy's approval
   TTL, ADR-0054 §6). This (a) frees the per-agent concurrency slot (PERF-3,
   ADR-0057 §7) and (b) **bounds command retention to the approval window**. The
   agent's block-and-wait deadline and this reaper are the two finalizers; both are
   **fail-closed** (no egress). This fills the lifecycle gap ADR-0058 §"Consequences"
   noted the *stateless broker exec* does not cover — here it is the *gateway's*
   parked record, not the broker's exec, that is reaped.

5. **Retention / GC.** Aging/clearing the persisted command folds into the **same
   deferred mechanism** as the `.exec` artifact GC (ADR-0058 Deferred) — recorded,
   not built here. Until then a terminal invocation may retain the command; the
   bounded approval window (4) keeps the *parked-state* exposure short.

## Consequences

- The operator gives **informed** consent — the release governs the actual command,
  not a resource handle. Closes audit finding F1.
- One small migration (a `command` column) + an additive `<ApprovalPrompt>` change;
  **policy-service, connector-service and the broker are unchanged** (the command
  already flowed to the broker at exec).
- A potentially secret-bearing string now persists in the gateway DB **for the
  approval window**. Mitigated by operator-only read, no-log, and the bounded
  lifecycle (4); consistent with the existing retained-stdout posture (ADR-0049).
  Full at-rest encryption of the command is **not** added at v0.3.0 (recorded
  below).
- The gateway gains its first lifecycle finalizer (the reaper), consistent with its
  ownership of the invocation (ADR-0057 §2).

## Deferred (recorded)

- **Command at-rest encryption / redaction** — a `pgcrypto` column (as `resources`
  uses) or pattern-redaction of inline secrets — v0.4.0; today's mitigation is
  access-control + bounded retention.
- **Persisted-command GC** — folds into the deferred `.exec` retention GC ticket
  (ADR-0058 Deferred).
- **Resolved resource *name* instead of the raw id** in the approval surface — the
  FE-12 resource picker (T-042).
- **Segregation-of-duties on who may approve** — deferred per ADR-0054/0057 (any
  operator may approve any invocation; invocation approval is not owner-scoped).
