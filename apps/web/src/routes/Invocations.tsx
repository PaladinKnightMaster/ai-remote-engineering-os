// Invocations route — the operator's "pending tool invocations" surface (FE-11;
// ADR-0057 §4 / ADR-0059). It lists MCP tool invocations parked pending approval
// (GET /v1/invocations, proxied by services/api to the gateway) and lets the
// operator approve/deny each one, reusing <ApprovalPrompt>. The approval is enforced
// server-side (api → gateway → policy); this UI only drives the audited control
// plane. Each row shows the EXACT command (ADR-0059) so consent is informed.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ApprovalPrompt } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type PendingInvocation = components["schemas"]["PendingInvocation"];

export function Invocations() {
  const [invocations, setInvocations] = useState<PendingInvocation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [resolvingId, setResolvingId] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/invocations", {});
    if (apiErr || !data) {
      setError("Could not load pending invocations.");
      setInvocations([]);
    } else {
      setInvocations(data.invocations ?? []);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function onResolve(id: string, decision: "approve" | "deny") {
    setResolvingId(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/invocations/{id}/resolve", {
        params: { path: { id } },
        body: { decision },
      });
      if (apiErr) {
        setError("Could not resolve the invocation — it may have expired. Refresh and try again.");
      }
      await load(); // refetch: a resolved invocation drops out of the pending list.
    } finally {
      setResolvingId(null);
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Pending tool invocations</h1>
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => void load()} className="text-sm text-muted-foreground hover:underline">
            Refresh
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

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : invocations.length === 0 ? (
        <p className="text-sm text-muted-foreground">No tool invocations are awaiting approval.</p>
      ) : (
        <ul className="space-y-4">
          {invocations.map((inv) => (
            <li key={inv.id}>
              <ApprovalPrompt
                title="Tool invocation approval"
                description="An AI agent is requesting to run a command on a governed resource."
                agent={inv.agent}
                resourceId={inv.resourceId}
                command={inv.command}
                busy={resolvingId === inv.id}
                onApprove={() => void onResolve(inv.id, "approve")}
                onDeny={() => void onResolve(inv.id, "deny")}
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
