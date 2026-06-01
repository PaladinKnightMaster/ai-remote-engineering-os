import { Link } from "react-router-dom";
import { CapabilityGate, useCapability } from "@aireos/ui";

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
  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="space-y-1">
        <h1 className="text-2xl font-semibold">AI Remote Engineering OS</h1>
        <p className="text-sm text-muted-foreground">
          Signed in as <span className="font-mono">{capability?.subject ?? "—"}</span> on
          edge <span className="font-mono">{capability?.edge ?? "—"}</span>.
        </p>
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
          <p className="mt-2 text-sm">
            <Link to="/sessions" className="font-medium text-primary hover:underline">
              Sessions →
            </Link>{" "}
            start and attach to a recorded SSH terminal.
          </p>
          <p className="mt-2 text-sm">
            <Link to="/audit" className="font-medium text-primary hover:underline">
              Audit timeline →
            </Link>{" "}
            review the chained, append-only evidence log. The resource picker
            lands with its feature ticket.
          </p>
        </CapabilityGate>
      </section>
    </div>
  );
}
