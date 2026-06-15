# ADR-0058: Broker non-interactive exec egress + audit content model

## Status

**Accepted** — 2026-06-14 (ratified in the T-038 war-room consult). Part of the
v0.3.0 "Typed MCP Gateway" release (`docs/evaluation/v0.3.0-release-criteria.md`).
Binding spec for the broker exec tickets (T-038a/b) and the gateway orchestrator
(T-039) that calls it.

**Builds on / conforms to:** ADR-0048 (`services/session-broker` = the SSH egress;
the cast model), ADR-0049 (hash-in-chain + cast-anchored audit content), ADR-0050
(the broker's service/operator auth model — the exec RPC mirrors its gating),
ADR-0053 (connector credential lease), ADR-0057 (the gateway invocation + the
`remote.exec` tool). **No ADR divergence (G2-clean):** it *extends* ADR-0048/0049
with a new egress modality; it edits no Accepted ADR.

## Context

ADR-0057's `remote.exec` tool needs a **non-interactive** command egress
(request → response). The broker's only egress today is the interactive PTY/Shell
path (`internal/sshpty`: `RequestPty` + `Shell`, ADR-0048). An MCP tool call is
one-shot, not an interactive cockpit session. We need exec egress that (a) **reuses
the broker's host-key pinning + connector lease** — the broker stays the *sole* SSH
egress engine, with no second SSH client and no second credential path — and (b)
audits the command + output in the **same chain model** as the PTY path (ADR-0049).

## Decision

1. **The broker gains a non-interactive exec path** (`sshpty.Exec`), reusing the
   existing host-key-pinned dial (`dialClient` + `ssh.FixedHostKey`) and the leased
   credential. It runs **one** command via `ssh.Session`, captures stdout, and
   closes. The broker remains the sole SSH egress engine — the gateway never dials
   SSH (SEC-18).
2. **Stateless — no `sessions` row.** Exec is a one-shot lease → dial → run → audit
   → close; it does **not** create a `sessions` row, allocate a PTY, or run the
   lifecycle state machine (ADR-0048). This diverges from the session-centric model
   deliberately: an agent tool call is not an operator cockpit session — the reaper,
   idle timeout, and WS lifecycle do not apply.
3. **Internal RPC surface.** `POST /v1/exec` on the broker — **service-service
   gated, audience `session-broker`** (the same auth class as the WS channel,
   ADR-0050). Body `{resourceId, command, invocationId}`; response
   `{stdout, exitCode, truncated}`. **Not** in the OpenAPI fragment (an internal
   service-service RPC, like the connector lease).
4. **Bounded (PERF-1).** A captured-output **byte cap** (`BROKER_EXEC_MAX_BYTES`,
   default 1 MiB) — over-cap output is truncated with `truncated:true`; and a
   **command timeout** (`BROKER_EXEC_TIMEOUT_SECONDS`, default 60s) — on exceed the
   session is closed (killing the command) and a timeout error returned. This is the
   command's timeout, **separate** from the gateway's approval block-and-wait.
5. **Audit content model (extends ADR-0049).** The exec emits **one** egress event:
   - **Outcome** = `remote.exec` (the exit disposition encoded — success vs error;
     final string settled in T-038a).
   - **Target** = `invocation:<invocationId>` — the correlation anchor.
     `invocation:<id>` plays the role `session:<id>` played in the v0.2.0 chain.
   - **EvidencePacketID** = `sha256:<stdout>` — the content anchor (the exec analog
     of the cast-file SHA).
   - The **captured stdout is retained** as an access-controlled artifact
     (`<invocationId>.exec` under `AIREOS_CAST_DIR`), so the SHA anchors a *retained*
     artifact (bytes-in-cast parity, ADR-0049) — not thin air. An agent's command
     output is forensically replayable.
   - `credential.leased` (connector) keeps `Target=resource:<id>`,
     `Evidence=invocation:<id>` — the broker passes `invocationId` as the lease
     correlation; connector treats it as opaque (**no connector change**).
6. **Correlation reuse, no downstream contract change.** `invocationId` flows as the
   opaque correlation id through connector (lease) + the exec event, exactly as it
   flows through policy (ADR-0057). Neither connector nor policy changes.

## Consequences

- The broker gains exec beside PTY; both share the host-key-pinned dial + the
  connector lease (DRY; the single-SSH-egress invariant holds).
- The exec event + retained artifact give the WD-3 chain
  (`tool.invoked → policy.decision → approval.* → credential.leased →
  remote.exec(stdout-SHA) → tool.result`) a replayable, hash-intact record.
- Stateless exec has no reaper/idle/lifecycle coverage — acceptable for a one-shot
  bounded by the command timeout.

## Deferred

- **Streaming** exec output (large/long commands) — v0.4.0 (the bounded synchronous
  capture suffices for the wedge; pairs with the deferred MCP SSE).
- **stderr in the audit** — T-038a captures stderr for the error path; whether to
  anchor it too is a later refinement.
- **`.exec` artifact retention GC** — the existing `session-broker casts gc` could
  extend to exec artifacts; recorded for a later ticket.
