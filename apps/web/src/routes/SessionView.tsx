// SessionView route — one session's page. It first fetches the session to branch
// on status (ADR-0054): a 'pending_approval' session renders <ApprovalPrompt>
// (approve → the session becomes 'pending' and the live terminal mounts; deny →
// 'closed'); a 'pending'/'active' session mounts <TerminalView> against the broker
// WebSocket channel (cookie-authenticated, T-018a); a closed/terminated session
// shows why it ended. The approval is enforced server-side (services/api +
// policy-service); the broker rejects a 'pending_approval' session until api flips
// it to 'pending', so this UI only drives the audited control plane.

import { useCallback, useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { ApprovalPrompt, TerminalView, type WSStatus } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Session = components["schemas"]["Session"];

// Same-origin WS URL for the broker channel (ws:// in dev via the Vite proxy,
// wss:// in prod behind the gateway). The session id is path-encoded.
function brokerChannelUrl(id: string): string {
  const base = window.location.origin.replace(/^http/, "ws");
  return `${base}/v1/sessions/${encodeURIComponent(id)}/channel`;
}

export function SessionView() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<WSStatus>("idle");
  const [ending, setEnding] = useState(false);
  const [resolving, setResolving] = useState(false);
  const [resolveError, setResolveError] = useState<string | null>(null);

  const loadSession = useCallback(async () => {
    setLoading(true);
    const { data, error } = await apiClient.GET("/v1/sessions/{id}", { params: { path: { id } } });
    setSession(error || !data ? null : data);
    setLoading(false);
  }, [id]);

  useEffect(() => {
    void loadSession();
  }, [loadSession]);

  async function onEnd() {
    setEnding(true);
    try {
      await apiClient.POST("/v1/sessions/{id}/end", { params: { path: { id } } });
      navigate("/sessions");
    } finally {
      setEnding(false);
    }
  }

  async function onResolve(decision: "approve" | "deny") {
    setResolving(true);
    setResolveError(null);
    try {
      const { error } = await apiClient.POST("/v1/sessions/{id}/resolve", {
        params: { path: { id } },
        body: { decision },
      });
      if (error) {
        setResolveError("Could not resolve the approval — it may have expired. Try again.");
      }
      // Reconcile either way: approve → 'pending' (terminal); deny/expired → 'closed'.
      await loadSession();
    } finally {
      setResolving(false);
    }
  }

  if (loading) {
    return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  }

  if (!session) {
    return (
      <div className="space-y-3 p-6">
        <p className="text-sm text-muted-foreground">Session not found.</p>
        <button type="button" onClick={() => navigate("/sessions")} className="rounded-md border border-border px-3 py-1 text-sm">
          Back to sessions
        </button>
      </div>
    );
  }

  if (session.status === "pending_approval") {
    return (
      <div className="flex h-screen flex-col">
        <header className="flex items-center justify-between border-b border-border p-3">
          <div className="text-sm">
            Session <span className="font-mono">{id}</span> <span className="text-muted-foreground">· awaiting approval</span>
          </div>
          <button type="button" onClick={() => navigate("/sessions")} className="rounded-md border border-border px-3 py-1 text-sm">
            Back
          </button>
        </header>
        <div className="flex flex-1 items-center justify-center p-6">
          <ApprovalPrompt
            resourceId={session.resourceId}
            busy={resolving}
            error={resolveError}
            onApprove={() => void onResolve("approve")}
            onDeny={() => void onResolve("deny")}
          />
        </div>
      </div>
    );
  }

  if (session.status === "closed" || session.status === "terminated") {
    const notApproved = session.closedReason === "approval_denied" || session.closedReason === "approval_timeout";
    return (
      <div className="space-y-3 p-6">
        <p role="alert" className="text-sm text-muted-foreground">
          {notApproved ? "This session was not approved." : "This session has ended."}
          {session.closedReason ? <span className="text-muted-foreground"> ({session.closedReason})</span> : null}
        </p>
        <button type="button" onClick={() => navigate("/sessions")} className="rounded-md border border-border px-3 py-1 text-sm">
          Back to sessions
        </button>
      </div>
    );
  }

  // pending / active → the live terminal.
  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center justify-between border-b border-border p-3">
        <div className="text-sm">
          Session <span className="font-mono">{id}</span> <span className="text-muted-foreground">· {status}</span>
        </div>
        <div className="flex gap-2">
          <button type="button" onClick={() => navigate("/sessions")} className="rounded-md border border-border px-3 py-1 text-sm">
            Back
          </button>
          <button
            type="button"
            onClick={onEnd}
            disabled={ending}
            className="rounded-md bg-destructive px-3 py-1 text-sm text-destructive-foreground disabled:opacity-60"
          >
            {ending ? "Ending…" : "End session"}
          </button>
        </div>
      </header>
      {status === "closed" && (
        <div role="alert" className="border-b border-border bg-muted px-3 py-2 text-sm text-muted-foreground">
          Session ended (the connection closed). At v0.1.0 a dropped connection ends
          the session —{" "}
          <button type="button" onClick={() => navigate("/sessions")} className="font-medium text-primary hover:underline">
            start a new one
          </button>
          .
        </div>
      )}
      <TerminalView
        url={brokerChannelUrl(id)}
        onStatusChange={setStatus}
        className="min-h-0 flex-1 bg-background p-1"
      />
    </div>
  );
}
