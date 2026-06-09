// <ApprovalPrompt> — the human-in-the-loop approval panel for an action that
// requires explicit operator authorization (ADR-0054; ADR-0047 §FE-5). Reserved
// in the @aireos/ui barrel for "the first approval-touching action ticket" — this
// is it (T-031c). At v0.2.0 it gates a session-start to a requires_approval
// resource; v0.3.0 reuses it for MCP tool-call / mobile approval.
//
// Presentational only: the consumer (apps/web) owns the API call + routing and
// drives busy / error / onApprove / onDeny. Same boundary as <TerminalView>.

export interface ApprovalPromptProps {
  /** The resource the parked action targets (shown for operator context). */
  resourceId?: string;
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
        <h2 className="text-lg font-semibold">Approval required</h2>
        <p className="text-sm text-muted-foreground">
          This session requires approval before it can start.
          {resourceId ? (
            <>
              {" "}
              Resource <span className="font-mono">{resourceId}</span>.
            </>
          ) : null}
        </p>
      </div>

      {error ? (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      ) : null}

      <div className="flex gap-2">
        <button
          type="button"
          onClick={onApprove}
          disabled={busy}
          className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
        >
          {busy ? "Working…" : "Approve"}
        </button>
        <button
          type="button"
          onClick={onDeny}
          disabled={busy}
          className="rounded-md bg-destructive px-3 py-2 text-sm font-medium text-destructive-foreground disabled:opacity-60"
        >
          Deny
        </button>
      </div>
    </section>
  );
}
