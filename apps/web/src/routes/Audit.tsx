// Audit route — the operator audit-timeline view (T-024-b). Reads the chained,
// append-only evidence log through services/api's read-proxy
// (GET /v1/audit/events → audit-service, T-024-a). Closes the "operator visits
// the audit timeline" half of WD-1 step 8.
//
// Read-only by construction: the chain is append-only at the authority
// (ADR-0045) and the proxy exposes only GET. Forward-only cursor pagination
// ("Load more"); a manual "Refresh" re-reads from the head. No client-side
// filtering at v0.1.0 — filtering a partial page would misrepresent the chain.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type AuditEvent = components["schemas"]["AuditEvent"];

const PAGE_LIMIT = 50;

export function Audit() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // load fetches one page. reset=true starts a fresh read from the head
  // (Refresh); otherwise the page is appended (Load more).
  const load = useCallback(async (cursor?: string, reset = false) => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/audit/events", {
      params: { query: { limit: PAGE_LIMIT, cursor } },
    });
    if (apiErr || !data) {
      setError("Could not load the audit timeline.");
    } else {
      setError(null);
      setEvents((prev) => (reset ? data.items ?? [] : [...prev, ...(data.items ?? [])]));
      setNextCursor(data.page?.nextCursor || undefined);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void load(undefined, true);
  }, [load]);

  return (
    <div className="mx-auto max-w-5xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Audit timeline</h1>
        <div className="flex items-center gap-4">
          <button
            onClick={() => void load(undefined, true)}
            disabled={loading}
            className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-60"
          >
            {loading ? "Loading…" : "Refresh"}
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      <section className="overflow-hidden rounded-md border border-border">
        {events.length === 0 && !loading ? (
          <p className="p-4 text-sm text-muted-foreground">No audit events yet.</p>
        ) : (
          <table className="w-full text-left text-sm">
            <thead className="border-b border-border bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Actor</th>
                <th className="px-3 py-2 font-medium">Edge</th>
                <th className="px-3 py-2 font-medium">Target</th>
                <th className="px-3 py-2 font-medium">Outcome</th>
                <th className="px-3 py-2 font-medium">Evidence</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {events.map((e) => (
                <tr key={e.id}>
                  <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-muted-foreground">
                    {e.timestamp}
                  </td>
                  <td className="px-3 py-2 font-mono">{e.actor}</td>
                  <td className="px-3 py-2 text-muted-foreground">{e.edge}</td>
                  <td className="max-w-xs truncate px-3 py-2 font-mono" title={e.target}>
                    {e.target}
                  </td>
                  <td className="px-3 py-2">{e.outcome ?? "—"}</td>
                  <td
                    className="max-w-[16rem] truncate px-3 py-2 font-mono text-xs text-muted-foreground"
                    title={e.evidencePacketId}
                  >
                    {e.evidencePacketId ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {nextCursor && (
        <div className="flex justify-center">
          <button
            onClick={() => void load(nextCursor)}
            disabled={loading}
            className="rounded-md border border-border px-3 py-2 text-sm disabled:opacity-60"
          >
            {loading ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
    </div>
  );
}
