// Sessions route — list the operator's cockpit sessions, start a new one against
// a resource, and end one (ADR-0048 control plane via services/api; the live
// terminal is /sessions/:id). Drives only the audited path: create via REST →
// connect the broker WS from <SessionView>; never a direct/unaudited dial.

import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Session = components["schemas"]["Session"];
type Resource = components["schemas"]["Resource"];

export function Sessions() {
  const [sessions, setSessions] = useState<Session[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [resourceId, setResourceId] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [starting, setStarting] = useState(false);
  const navigate = useNavigate();

  const load = useCallback(async () => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/sessions", {});
    if (apiErr || !data) {
      setError("Could not load sessions.");
      setSessions([]);
    } else {
      setSessions(data.items ?? []);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Resource picker (FE-12): list resources once so the operator selects one by name
  // instead of pasting a raw UUID. Read-only; failure leaves an empty picker.
  useEffect(() => {
    void (async () => {
      const { data } = await apiClient.GET("/v1/resources", {});
      setResources(data?.items ?? []);
    })();
  }, []);

  async function onStart(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const id = resourceId.trim();
    if (!id) return;
    setStarting(true);
    setError(null);
    try {
      const { data, error: apiErr } = await apiClient.POST("/v1/sessions", {
        body: { resourceId: id, mode: "terminal" },
      });
      if (apiErr || !data) {
        setError("Could not start session — check the resource id.");
        return;
      }
      navigate(`/sessions/${data.id}`);
    } finally {
      setStarting(false);
    }
  }

  async function onEnd(id: string) {
    await apiClient.POST("/v1/sessions/{id}/end", { params: { path: { id } } });
    await load();
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Sessions</h1>
        <Link to="/" className="text-sm text-muted-foreground hover:underline">
          ← Dashboard
        </Link>
      </header>

      <form onSubmit={onStart} aria-label="start session" className="flex gap-2">
        <select
          name="resourceId"
          aria-label="resource"
          value={resourceId}
          onChange={(e) => setResourceId(e.target.value)}
          className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm"
        >
          <option value="">
            {resources.length === 0 ? "No resources available" : "Select a resource…"}
          </option>
          {resources.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name} ({r.type})
            </option>
          ))}
        </select>
        <button
          type="submit"
          disabled={starting || !resourceId}
          className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
        >
          {starting ? "Starting…" : "Start session"}
        </button>
      </form>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      <section className="rounded-md border border-border">
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : sessions.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">No sessions yet.</p>
        ) : (
          <ul className="divide-y divide-border">
            {sessions.map((s) => (
              <li key={s.id} className="flex items-center justify-between p-3 text-sm">
                <div className="min-w-0">
                  <Link to={`/sessions/${s.id}`} className="font-mono hover:underline">
                    {s.id}
                  </Link>
                  <span className="ml-2 text-muted-foreground">{s.status}</span>
                </div>
                {(s.status === "pending" || s.status === "active") && (
                  <button
                    onClick={() => onEnd(s.id)}
                    className="rounded-md border border-border px-2 py-1 text-xs"
                  >
                    End
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
