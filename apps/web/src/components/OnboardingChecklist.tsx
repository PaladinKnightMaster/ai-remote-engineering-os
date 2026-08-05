// OnboardingChecklist — the E-04 first-run checklist (T-118a; ADR-0068 §D8,
// release criteria ONB-1/ONB-2). First new file under apps/web/src/components/
// (previously only routes/ existed) — this is the first apps/web UI element
// that is not itself a route; packages/ui/src/components/ already establishes
// what that directory name means, so a future non-route apps/web element has
// a home instead of getting bolted onto a route file.
//
// Desk-only by construction: Dashboard.tsx renders this ONLY inside a
// <CapabilityGate deniedScopes={["approvals@1"]}> (the same gate T-115b built
// for Runs/Grants/Workstreams/Memories) — a paired phone session cannot create
// resources, launch Runs, or take almost anything else this panel points at,
// so it must never see the panel (ADR-0068 §D5).
//
// ONB-1: completion is DERIVED from live state on every mount — never cached,
// never a stored flag/column. Two conditions, not the three ADR-0068 §D8
// names:
//
//   1. Does a resource exist?             GET /v1/resources
//   2. Has a governed action completed?   GET /v1/audit/events?outcome=tool.result
//
// The third condition ADR-0068 §D8 names — "a connector credential" — is
// deliberately NOT a separate check, and no credential-presence endpoint is
// added for it. The API surface closed at T-115b (ADR-0068 §D11 — the last
// growth before the E-06 freeze), and one isn't needed anyway: a completed
// governed action SUBSUMES the credential. You cannot complete a
// `remote.exec` invocation — policy → approval → connector → broker — without
// a credential the connector actually decrypted and used successfully. A
// credential ROW existing would prove LESS than that (only that a secret is
// stored, not that it works). So condition 2 is strictly stronger than a
// credential-presence check would be — do not "restore" a third read here.
//
// `outcome=tool.result` is audit-service's outcome filter, which is EXACT
// match only (no wildcard/prefix — services/audit-service/internal/store/
// store.go binds it as `outcome = $n`), so it can't match "any *.completed"
// outcome across the product. "tool.result" is the one outcome every
// successfully completed governed invocation carries, fired ad-hoc or as a
// Run step (services/mcp-gateway/internal/store/store.go Finish/
// finalizeFrom) — the narrowest true signal for "a governed action has
// completed" reachable with the existing filter, no new preset or param
// (ADR-0068 §D11 forbids both).
//
// ONB-2: dismissal is a per-browser localStorage flag ONLY. The server
// records nothing about onboarding — no route, no column, no audit event.
// This is a deliberate asymmetry: the checklist's COMPLETION (above) is
// derived and can never be dismissed away; the PANEL'S VISIBILITY is pure
// presentation state. A dismissed panel on an un-seeded stack is still an
// un-seeded stack — dismissal hides the reminder, not the fact.

import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@aireos/ui";
import { apiClient } from "../api";

// Namespaced so a future apps/web local-storage key doesn't collide with it;
// no existing convention to match (Login.tsx explicitly avoids localStorage
// for auth state — this is unrelated presentation state, not a credential).
const DISMISSED_KEY = "aireos.onboarding.dismissed";

function readDismissed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(DISMISSED_KEY) === "1";
  } catch {
    // Storage can throw (private-browsing quota, disabled storage). Treat as
    // "not dismissed" — the worst case is the panel keeps showing, not that
    // it hides itself unexpectedly.
    return false;
  }
}

interface DerivedState {
  resourceExists: boolean;
  actionCompleted: boolean;
  loading: boolean;
}

// Recomputed fresh on every mount — see ONB-1 note above. Both reads fire
// together; a fetch error on either leaves that condition unmet (fails safe
// toward "still onboarding") rather than surfacing a route-level error state,
// matching how Grants/Runs/Sessions already treat their own /v1/resources
// picker fetch (ignore the error, default to empty).
function useDerivedOnboardingState(): DerivedState {
  const [resourceExists, setResourceExists] = useState(false);
  const [actionCompleted, setActionCompleted] = useState(false);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const [resources, actions] = await Promise.all([
        apiClient.GET("/v1/resources", {}),
        apiClient.GET("/v1/audit/events", {
          params: { query: { limit: 1, outcome: "tool.result" } },
        }),
      ]);
      if (cancelled) return;
      setResourceExists((resources.data?.items?.length ?? 0) > 0);
      setActionCompleted((actions.data?.items?.length ?? 0) > 0);
      setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return { resourceExists, actionCompleted, loading };
}

function ChecklistItem({ done, children }: { done: boolean; children: ReactNode }) {
  return (
    <li className="flex items-center gap-2 text-sm">
      <span aria-hidden="true" className={done ? "text-foreground" : "text-muted-foreground"}>
        {done ? "✓" : "○"}
      </span>
      <span className={done ? "text-foreground" : "text-muted-foreground"}>{children}</span>
      <span className="sr-only">{done ? "done" : "not done"}</span>
    </li>
  );
}

export function OnboardingChecklist() {
  const [dismissed, setDismissed] = useState(readDismissed);
  const { resourceExists, actionCompleted, loading } = useDerivedOnboardingState();

  if (dismissed) return null;

  const allDone = resourceExists && actionCompleted;

  function dismiss() {
    try {
      window.localStorage.setItem(DISMISSED_KEY, "1");
    } catch {
      // Storage can throw; the in-memory dismiss below still hides the panel
      // for the rest of this session even if it can't persist.
    }
    setDismissed(true);
  }

  return (
    <section aria-label="first-run checklist" className="rounded-md border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">Get started</h2>
        <Button variant="ghost" size="xs" onClick={dismiss}>
          Dismiss
        </Button>
      </div>
      <ul className="mt-2 space-y-1">
        <ChecklistItem done={resourceExists}>Register a resource</ChecklistItem>
        <ChecklistItem done={actionCompleted}>Complete a governed action</ChecklistItem>
      </ul>
      {loading && <p className="mt-2 text-xs text-muted-foreground">Checking current status…</p>}
      {!loading && allDone && (
        <p className="mt-2 text-xs text-muted-foreground">All set — dismiss whenever you&apos;re ready.</p>
      )}
    </section>
  );
}
