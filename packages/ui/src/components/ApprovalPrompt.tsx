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

import { Button } from "./Button";

export interface ApprovalPromptProps {
  /** The resource the parked action targets (shown for operator context). */
  resourceId?: string;
  /** The agent that requested the action (invocation approval; ADR-0057/0059). */
  agent?: string;
  /** The exact command awaiting approval — shown verbatim for informed consent (ADR-0059). */
  command?: string;
  /** Heading override; defaults to "Approval required". */
  title?: string;
  /** Body copy override; defaults to the v0.2.0 session-start sentence. */
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
  title = "Approval required",
  description = "This session requires approval before it can start.",
  busy = false,
  error,
  onApprove,
  onDeny,
  className,
}: ApprovalPromptProps) {
  return (
    <section
      role="region"
      aria-label="approval required"
      aria-busy={busy}
      className={`mx-auto max-w-md space-y-4 rounded-md border border-border bg-background p-6 ${className ?? ""}`}
    >
      <div className="space-y-1">
        <h2 className="text-lg font-semibold">{title}</h2>
        <p className="text-sm text-muted-foreground">
          {description}
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
      </div>

      {command ? (
        <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 text-sm font-mono text-foreground" aria-label="command">
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
