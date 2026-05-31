// SessionView route — the live terminal for one session. Mounts <TerminalView>
// against the broker WebSocket channel (authenticated by the aireos_session
// cookie via the broker's CookieToAuthHeader, T-018a) and offers an End action
// (services/api control plane, which the broker observes to tear down + emit
// session.close, T-017b).

import { useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { TerminalView, type WSStatus } from "@aireos/ui";
import { apiClient } from "../api";

// Same-origin WS URL for the broker channel (ws:// in dev via the Vite proxy,
// wss:// in prod behind the gateway). The session id is path-encoded.
function brokerChannelUrl(id: string): string {
  const base = window.location.origin.replace(/^http/, "ws");
  return `${base}/v1/sessions/${encodeURIComponent(id)}/channel`;
}

export function SessionView() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [status, setStatus] = useState<WSStatus>("idle");
  const [ending, setEnding] = useState(false);

  async function onEnd() {
    setEnding(true);
    try {
      await apiClient.POST("/v1/sessions/{id}/end", { params: { path: { id } } });
      navigate("/sessions");
    } finally {
      setEnding(false);
    }
  }

  return (
    <div className="flex h-screen flex-col">
      <header className="flex items-center justify-between border-b border-border p-3">
        <div className="text-sm">
          Session <span className="font-mono">{id}</span>{" "}
          <span className="text-muted-foreground">· {status}</span>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => navigate("/sessions")}
            className="rounded-md border border-border px-3 py-1 text-sm"
          >
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
