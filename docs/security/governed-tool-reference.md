# Governed tool reference — what an operator is approving

**Authored 2026-08-24 (T-145, v0.14.0 "Governed Tool Surface"). Closes DOC-37.**

Four tools cross the gateway's `agent-remote-session` edge as of this release: `remote.exec` (v0.3.0)
and the `remote.file.read` / `remote.file.write` / `remote.file.list` family (v0.14.0, ADR-0071). This
document is the operator-side counterpart to that ADR: what each tool does, what an approval actually
authorizes, and what the digest shown in the prompt binds to. It does not restate the governance
mechanics (policy decision, approval park, audit brackets) — those are unchanged across all four tools
and are covered in `docs/security/agent-permission-model.md`.

**Where this is grounded.** Every description below matches the copy an operator actually sees in
`<ApprovalPrompt>` (`packages/ui/src/components/ApprovalPrompt.tsx`, `TOOL_COPY`) and the tool
descriptions returned by `tools/list` (`services/mcp-gateway/internal/catalog/catalog.go`) — this is a
reference to the as-built prompt, not a separate claim about it.

---

## The four tools

| Tool | What it does | Mutates the target? |
|---|---|---|
| `remote.exec` | Runs one non-interactive command on a governed SSH resource. | Whatever the command does — unbounded, untyped. |
| `remote.file.read` | Reads one file from a governed resource and returns its bytes plus a sha256 digest. | No. |
| `remote.file.list` | Lists one directory (names, sizes, modes, mtimes) on a governed resource. | No. |
| `remote.file.write` | Writes bytes to one path on a governed resource. | **Yes — the only file-family member that changes the host.** |

All four are policy-checked, may block for human approval, may be denied, and are audited identically
(`tool.invoked` / `tool.result` on the `agent-tool` edge, anchored `invocation:<id>`) — an agent-facing
honesty `remote.exec` already practised and the file family inherits unchanged (CON-10).

## What approving each tool actually authorizes

**`remote.exec`.** The operator approves the **literal command string**, shown verbatim in the prompt.
This is the one tool where "what you approved" and "what will run" are the same text — there is no
narrowing, because there is nothing to narrow: a command *is* its own complete description.

**`remote.file.read` and `remote.file.list`.** The operator approves a **disclosure**, not a mutation.
The prompt states the consequence in plain language:

- read: *"The file's contents will be disclosed to the agent."*
- list: *"This directory's filenames, sizes and timestamps will be disclosed to the agent."*

A directory listing is not file content, but it is reconnaissance-relevant (what exists, how big it is,
when it last changed) — the prompt names that rather than implying `list` is inert because it doesn't
mutate anything.

**`remote.file.write` — a genuine narrowing of "informed" (DOC-39, ADR-0071 §D3).** No operator
meaningfully reviews several kilobytes of file content inside an approval prompt, so the write tool does
not attempt to show the bytes. Instead the prompt renders a human-readable descriptor:

```
remote.file.write /etc/nginx/nginx.conf (4,218 bytes, sha256:9f2c…a41b, mode 0644)
```

and states the consequence directly beneath it: *"You are approving the path, size, digest and mode
shown below — not the file's contents."* The heading also carries a **"Changes the host"** badge —
the one visual cue that distinguishes this tool from the three that only read.

**Say this plainly, because it is the honest position, not a hedge:** approving a write means trusting
that the content behind the digest is what it claims to be, not having reviewed that content. The
mitigation is that the descriptor names the path and size, and that the write verb is separately
grantable — not that the operator has read the bytes.

## What the digest binds (SEC-49)

The sha256 shown in a write's descriptor is not decorative. `remote.file.write`'s execution step
**recomputes the digest over the bytes about to be written and refuses the write if it does not match
the approved descriptor** — before the broker is ever called. A payload swapped in after approval (a
different file offered under the same approved path) is refused cryptographically, not by convention.
This is the same shape of protection as the v0.3.0 approved-command-swap closure for `remote.exec`, and
the capstone re-proves it on this path (SEC-49, `services/mcp-gateway/internal/catalog`).

The digest also anchors the audit record: the broker records `Evidence sha256:<bytes>` for the
transferred content on both reads and writes. **Raw file content never enters the audit chain in either
direction** — an auditor binds approval to outcome via the digest, never by reading the bytes back out
of the chain.

## What the digest does *not* mean

The digest is a binding, not a review. It proves the bytes written are the bytes the operator's
approval described — it says nothing about whether those bytes are safe, correct, or what the operator
assumed they were. A malicious plan can still produce content whose digest an operator has no
independent way to evaluate before approving. See `docs/security/threat-model.md` §2a for the residual
this leaves open, and why containment (deferred) — not this digest — is what eventually closes it.

## A write changes ownership and permission semantics (residual, v0.14.0 criteria §14b)

`remote.file.write` writes to a temp sibling and atomically renames it into place, rather than
truncating the existing file in place. This is the correct trade for atomicity — a failed write cannot
destroy the target — but it has an operator-visible consequence: the target's **inode is replaced**, so
an existing file's owner/group become the connecting SSH user's, and the operation needs **write
permission on the containing directory**, not merely on the file. A write that would have succeeded
under the old file-level-permission model can fail (or silently change ownership) under this one. If a
write behaves unexpectedly against a file you expected to be group-writable, check directory
permissions first.

## Bounds every operator should know

- **1 MiB default transfer cap**, both directions, configurable. A read over the cap is **refused, not
  truncated** — a truncated file is a corrupt file and a digest that matches nothing.
- Per-operation timeout mirrors `remote.exec`'s.
- Over-cap, timeout, permission-denied and no-such-path all fail closed and mark the invocation
  `failed` — never silently dropped.

## Scope: an agent token must name the file verbs explicitly

An agent token with an **empty** operations list is unrestricted for `remote.exec` alone — that
back-compat allowance predates the file family and is not extended to it. **Every file verb
(`file.read`, `file.write`) must be explicitly present in the token's scope**, or the corresponding
tool call is refused (ADR-0071 §D7, SEC-48). Minting a token that can run commands does not, by itself,
grant it file access — an operator choosing to widen an agent's authority to include file transfer must
say so explicitly when minting the token or issuing the grant.

## Honest limits

- **`remote.exec` can still do everything the file tools do, less legibly.** `cat`, `base64` and shell
  redirection reach the same files with no typed bounds, no digest anchor, and no disclosure line. The
  file family is better governed than the tool beside it; only containment (deferred, B-040) closes
  that gap.
- **Live file transfer is proven on the compose stack only**, not the installed stack — see
  `docs/security/threat-model.md` §2a.

See also: `docs/adr/ADR-0071-governed-file-transfer.md` (the binding spec), `docs/security/threat-model.md`
§2a (the residuals this reference draws from), and `docs/devops/quickstart-first-governed-action.md`
(the `remote.exec` operator walkthrough this document complements).
