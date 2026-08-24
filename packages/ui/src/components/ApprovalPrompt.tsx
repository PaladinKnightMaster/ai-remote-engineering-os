// <ApprovalPrompt> — the human-in-the-loop approval panel for an action that
// requires explicit operator authorization (ADR-0054; ADR-0047 §FE-5). Reserved
// in the @aireos/ui barrel for "the first approval-touching action ticket" — landed
// at T-031c gating a session-start; generalized at T-040c to also gate an MCP tool
// invocation (ADR-0057/0059).
//
// Presentational only: the consumer (apps/web) owns the API call + routing and
// drives busy / error / onApprove / onDeny. Same boundary as <TerminalView>.
//
// The props are additive + backward-compatible: with only resourceId it renders the
// v0.2.0 session copy; an invocation consumer also passes agent + command (the
// persisted payload, shown verbatim so the operator gives INFORMED consent, ADR-0059)
// and overrides title/description.
//
// T-117 migrates its Approve/Deny actions onto <Button>. This is a deliberate,
// disclosed superset of FE-37's literal text ("every <button> in apps/web") — CEO
// decision at the T-117 war room: ADR-0068 frames E-05a as a defect on already-
// shipped functionality, product-wide, not directory-scoped; the as-built count of
// 48 was taken across ROUTES, so packages/ui components were never excluded, only
// never counted; FE-42's browser-evidence rule already names packages/ui; and this
// is literally the approval surface the paired phone approves from (ADR-0068 §D4/
// D5) — shipping a release about phone approval with stateless buttons on that exact
// screen would be absurd. An intra-package import (Button lives in the same package).
//
// T-143 (FE-43, ADR-0071 §D3) generalizes copy off `tool` rather than the caller
// hardcoding it: before this, every invocation (exec OR file) was told "run a
// command", which became a live falsehood once remote.file.read/write/list
// registered. TOOL_COPY drives description + payload aria-label + the
// write-only mutating affordance from the PERSISTED tool name — never from
// parsing `command` (the §D3 descriptor stays opaque prose here by design; a
// second parser of that format, in this language, would drift silently from the
// gateway's own descriptor parser). `tool` is optional and additive: omitting it
// (the v0.2.0 session-start caller) keeps the original default copy untouched.

import { Button } from "./Button";

interface ToolCopy {
  /** Accurate per-tool sentence for what the agent is asking to do. */
  description: string;
  /** Accessible label for the payload block — "command" is wrong for a file op. */
  payloadLabel: string;
  /** True only for the one tool that changes the target host (remote.file.write). */
  mutating?: boolean;
}

// Keyed by the PersistedInvocation `tool` field (ADR-0071 §D1's four registered
// tools as of v0.14.0). Unknown/absent tool falls back to the caller's own
// description/aria-label so older callers (session-start) are unaffected.
const TOOL_COPY: Record<string, ToolCopy> = {
  "remote.exec": {
    description: "An AI agent is requesting to run a command on a governed resource.",
    payloadLabel: "command",
  },
  "remote.file.read": {
    description: "An AI agent is requesting to read a file from a governed resource.",
    payloadLabel: "file descriptor",
  },
  "remote.file.list": {
    description: "An AI agent is requesting to list a directory on a governed resource.",
    payloadLabel: "directory descriptor",
  },
  "remote.file.write": {
    description:
      "An AI agent is requesting to write a file on a governed resource — this MUTATES the target host.",
    payloadLabel: "file descriptor",
    mutating: true,
  },
};

const DEFAULT_DESCRIPTION = "This session requires approval before it can start.";

export interface ApprovalPromptProps {
  /** The resource the parked action targets (shown for operator context). */
  resourceId?: string;
  /** The agent that requested the action (invocation approval; ADR-0057/0059). */
  agent?: string;
  /** The exact command awaiting approval — shown verbatim for informed consent (ADR-0059). */
  command?: string;
  /**
   * The invocation's persisted tool name (e.g. "remote.exec", "remote.file.write";
   * ADR-0071 §D1). Drives the per-tool description, payload aria-label, and the
   * write-only mutating affordance (T-143, FE-43). Omit for the non-invocation
   * (session-start) use of this component.
   */
  tool?: string;
  /** Heading override; defaults to "Approval required". */
  title?: string;
  /** Body copy override; defaults to the tool-derived copy, or the v0.2.0 session-start sentence. */
  description?: string;
  /** True while an approve/deny request is in flight — disables the actions. */
  busy?: boolean;
  /** A resolve error to surface; null/undefined hides the alert. */
  error?: string | null;
  onApprove: () => void;
  onDeny: () => void;
  className?: string;
}

export function ApprovalPrompt({
  resourceId,
  agent,
  command,
  tool,
  title = "Approval required",
  description,
  busy = false,
  error,
  onApprove,
  onDeny,
  className,
}: ApprovalPromptProps) {
  const copy = tool ? TOOL_COPY[tool] : undefined;
  const resolvedDescription = description ?? copy?.description ?? DEFAULT_DESCRIPTION;
  const payloadLabel = copy?.payloadLabel ?? "command";
  // Exec's rendering is pixel-identical to before this ticket (overflow-x-auto,
  // single-line scroll). File descriptors carry a 64-hex digest (§D3 example:
  // "remote.file.write /etc/nginx/nginx.conf (4,218 bytes, sha256:9f2c…a41b, mode
  // 0644)") that must stay fully readable/selectable without a horizontal
  // scrollbar, so those wrap instead.
  const payloadClassName =
    copy && tool !== "remote.exec"
      ? "whitespace-pre-wrap break-all rounded-md bg-muted px-3 py-2 text-sm font-mono text-foreground"
      : "overflow-x-auto rounded-md bg-muted px-3 py-2 text-sm font-mono text-foreground";

  return (
    <section
      role="region"
      aria-label="approval required"
      aria-busy={busy}
      className={`mx-auto max-w-md space-y-4 rounded-md border border-border bg-background p-6 ${className ?? ""}`}
    >
      <div className="space-y-1">
        <div className="flex items-center gap-2">
          <h2 className="text-lg font-semibold">{title}</h2>
          {copy?.mutating ? (
            <span
              role="status"
              className="inline-flex items-center rounded-full border border-destructive/40 bg-destructive/10 px-2 py-0.5 text-xs font-medium text-destructive"
            >
              Mutates target host
            </span>
          ) : null}
        </div>
        <p className="text-sm text-muted-foreground">
          {resolvedDescription}
          {agent ? (
            <>
              {" "}
              Agent <span className="font-mono">{agent}</span>.
            </>
          ) : null}
          {resourceId ? (
            <>
              {" "}
              Resource <span className="font-mono">{resourceId}</span>.
            </>
          ) : null}
        </p>
        {copy?.mutating ? (
          <p className="text-xs text-muted-foreground">
            You are approving the path, size, digest and mode shown below — not the file's contents.
          </p>
        ) : null}
      </div>

      {command ? (
        <pre className={payloadClassName} aria-label={payloadLabel}>
          {command}
        </pre>
      ) : null}

      {error ? (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      ) : null}

      <div className="flex gap-2">
        <Button variant="primary" size="md" type="button" onClick={onApprove} disabled={busy}>
          {busy ? "Working…" : "Approve"}
        </Button>
        <Button variant="destructive" size="md" type="button" onClick={onDeny} disabled={busy}>
          Deny
        </Button>
      </div>
    </section>
  );
}
