import { Link } from "react-router-dom";
import { CapabilityGate, useCapability } from "@aireos/ui";

// ADR-0068 §D5: a live "approvals@1" paired session's capability denies
// launching Runs, issuing/revoking grants, registering/removing context
// sources, minting agent tokens, and opening terminal sessions — so this
// deny-list hides exactly those Dashboard links from a paired session,
// leaving only the approval surface (Invocations) and the audit read
// surface visible. A desktop session has no `scope` at all, so it is never
// affected: <CapabilityGate> renders its children as a bare Fragment, adding
// no wrapper element, so the desktop Dashboard's rendered output is
// byte-identical to before this ticket.
const PAIRED_DENIED_SCOPES = ["approvals@1"];

/**
 * Dashboard — minimal post-login surface at T-007c.
 *
 * Real cockpit content (resource picker, session viewer, audit timeline, etc.
 * — see docs/deep-dive/10-ui-ux-specification.md §"Remote Cockpit screens")
 * lands with the feature tickets that own each surface. This route exists
 * so the auth round-trip (login → cookies → /v1/auth/me → capability state)
 * has a verifiable endpoint and so the route guard in App.tsx has
 * something to protect.
 */
export function Dashboard() {
  const { capability } = useCapability();
  const paired = capability?.scope === "approvals@1";
  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">AI Remote Engineering OS</h1>
        <p className="text-sm text-muted-foreground">
          Signed in as <span className="font-mono">{capability?.subject ?? "—"}</span> on
          edge <span className="font-mono">{capability?.edge ?? "—"}</span>, deployment
          profile <span className="font-mono">{capability?.deploymentProfile ?? "—"}</span>.
        </p>
        {paired && (
          <p className="text-sm text-muted-foreground">
            This is a paired device session — limited to approvals and the audit timeline.
          </p>
        )}
      </header>

      <section className="rounded-md border border-border bg-background p-4">
        <h2 className="text-sm font-medium">Operator actions</h2>
        <CapabilityGate
          edge="human-agent"
          fallback={
            <p className="mt-2 text-sm text-muted-foreground">
              Your capability edge does not include operator actions.
            </p>
          }
        >
          <CapabilityGate edge="human-agent" deniedScopes={PAIRED_DENIED_SCOPES}>
            <p className="mt-2 text-sm">
              <Link to="/sessions" className="font-medium text-primary hover:underline">
                Sessions →
              </Link>{" "}
              start and attach to a recorded SSH terminal.
            </p>
          </CapabilityGate>
          <p className="mt-2 text-sm">
            <Link to="/invocations" className="font-medium text-primary hover:underline">
              Pending tool invocations →
            </Link>{" "}
            approve or deny the commands AI agents request on governed resources.
          </p>
          <CapabilityGate edge="human-agent" deniedScopes={PAIRED_DENIED_SCOPES}>
            <p className="mt-2 text-sm">
              <Link to="/runs" className="font-medium text-primary hover:underline">
                Runs →
              </Link>{" "}
              compose, launch, and watch governed multi-step remote.exec runs.
            </p>
            <p className="mt-2 text-sm">
              <Link to="/grants" className="font-medium text-primary hover:underline">
                Delegation grants →
              </Link>{" "}
              issue and revoke the scoped grants that let AI agents launch governed runs on their own.
            </p>
            <p className="mt-2 text-sm">
              <Link to="/workstreams" className="font-medium text-primary hover:underline">
                Workstreams →
              </Link>{" "}
              charter standing objectives that fire governed runs on a schedule, bounded by their own grant.
            </p>
            <p className="mt-2 text-sm">
              <Link to="/memories" className="font-medium text-primary hover:underline">
                Memory →
              </Link>{" "}
              read, author, redact, pin, and export the operator's scoped, forgettable memory —
              the mutable projection beside the immutable audit chain.
            </p>
          </CapabilityGate>
          <p className="mt-2 text-sm">
            <Link to="/audit" className="font-medium text-primary hover:underline">
              Audit timeline →
            </Link>{" "}
            review the chained, append-only evidence log. The resource picker
            lands with its feature ticket.
          </p>
          <CapabilityGate edge="human-agent" deniedScopes={PAIRED_DENIED_SCOPES}>
            <p className="mt-2 text-sm">
              <Link to="/pairing" className="font-medium text-primary hover:underline">
                Device pairing →
              </Link>{" "}
              pair a phone with a scoped, revocable approval-only session via QR code.
            </p>
          </CapabilityGate>
        </CapabilityGate>
      </section>
    </div>
  );
}
