// Grants route — the operator's delegation-grant surface (FE-17; ADR-0062). An operator
// issues a scoped, revocable delegation grant to an agent (which resources it may run, with
// which verbs, bounded by TTL / steps-per-run / concurrency / budget), lists grants, and
// revokes one (DUR-7: revoking also cancels the grant's in-flight Runs). A grant is the human
// consent that lets an agent launch governed Runs on its own (run.launch) within these bounds
// — it replaces per-plan informed launch. This UI drives only the audited control plane.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Grant = components["schemas"]["Grant"];
type Resource = components["schemas"]["Resource"];

export function Grants() {
  const [grants, setGrants] = useState<Grant[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [agent, setAgent] = useState("");
  const [picked, setPicked] = useState<{ id: string; name: string }[]>([]);
  const [resourceId, setResourceId] = useState("");
  const [verbs, setVerbs] = useState("exec");
  const [ttlSeconds, setTtlSeconds] = useState(3600);
  const [maxStepsPerRun, setMaxStepsPerRun] = useState(10);
  const [maxConcurrentRuns, setMaxConcurrentRuns] = useState(4);
  const [stepBudget, setStepBudget] = useState(50);
  const [issuing, setIssuing] = useState(false);
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const loadGrants = useCallback(async () => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/grants", {});
    if (apiErr || !data) {
      setError("Could not load grants.");
      setGrants([]);
    } else {
      setGrants(data.items ?? []);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void loadGrants();
  }, [loadGrants]);

  // Resource picker (FE-12): select by name, not a pasted UUID (the Sessions/Runs pattern).
  useEffect(() => {
    void (async () => {
      const { data } = await apiClient.GET("/v1/resources", {});
      setResources(data?.items ?? []);
    })();
  }, []);

  function addResource() {
    const id = resourceId.trim();
    if (!id || picked.some((r) => r.id === id)) return;
    const name = resources.find((r) => r.id === id)?.name ?? id;
    setPicked((p) => [...p, { id, name }]);
    setResourceId("");
  }

  function removeResource(id: string) {
    setPicked((p) => p.filter((r) => r.id !== id));
  }

  async function onIssue() {
    if (!agent.trim() || picked.length === 0) return;
    setIssuing(true);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/grants", {
        body: {
          agent: agent.trim(),
          resources: picked.map((r) => r.id),
          verbs: verbs.split(",").map((v) => v.trim()).filter(Boolean),
          ttlSeconds,
          maxStepsPerRun,
          maxConcurrentRuns,
          stepBudget,
        },
      });
      if (apiErr) {
        setError(apiErr.message ? `Could not issue the grant: ${apiErr.message}` : "Could not issue the grant.");
        return;
      }
      setAgent("");
      setPicked([]);
      await loadGrants();
    } finally {
      setIssuing(false);
    }
  }

  async function onRevoke(id: string) {
    setRevokingId(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/grants/{id}/revoke", { params: { path: { id } } });
      if (apiErr) setError("Could not revoke the grant — it may already be revoked. Refresh and try again.");
      await loadGrants();
    } finally {
      setRevokingId(null);
    }
  }

  const numberField = (label: string, value: number, set: (n: number) => void, max?: number) => (
    <label className="text-xs text-muted-foreground">
      {label}
      <input
        type="number"
        aria-label={label}
        value={value}
        min={1}
        max={max}
        onChange={(e) => set(Number(e.target.value))}
        className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
      />
    </label>
  );

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Delegation grants</h1>
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => void loadGrants()} className="text-sm text-muted-foreground hover:underline">
            Refresh
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {/* Issue a grant: authorize an agent to launch governed Runs within these bounds. */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Issue a grant</h2>
        <form
          aria-label="issue grant"
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            void onIssue();
          }}
        >
          <input
            name="agent"
            aria-label="agent"
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
            placeholder="agent subject (e.g. agent:bot-1)"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
          />

          <div className="flex gap-2">
            <select
              aria-label="resource"
              value={resourceId}
              onChange={(e) => setResourceId(e.target.value)}
              className="w-44 rounded-md border border-input bg-background px-3 py-2 text-sm"
            >
              <option value="">{resources.length === 0 ? "No resources" : "Select a resource…"}</option>
              {resources.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name} ({r.type})
                </option>
              ))}
            </select>
            <button type="button" onClick={addResource} disabled={!resourceId} className="rounded-md border border-border px-3 py-2 text-sm font-medium disabled:opacity-60">
              Add resource
            </button>
          </div>
          {picked.length > 0 && (
            <ul aria-label="granted resources" className="divide-y divide-border rounded-md border border-border">
              {picked.map((r) => (
                <li key={r.id} className="flex items-center justify-between p-2 text-sm">
                  <span className="text-muted-foreground">{r.name}</span>
                  <button type="button" onClick={() => removeResource(r.id)} className="rounded-md border border-border px-2 py-1 text-xs">
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <label className="text-xs text-muted-foreground">
              Verbs
              <input
                aria-label="verbs"
                value={verbs}
                onChange={(e) => setVerbs(e.target.value)}
                className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
              />
            </label>
            {numberField("TTL seconds", ttlSeconds, setTtlSeconds)}
            {numberField("Max steps per run", maxStepsPerRun, setMaxStepsPerRun)}
            {/* api enforces the ceiling (422 above the gateway in-flight cap, PERF-7); the
                max attr keeps the browser's stepper honest so the form can't compose it. */}
            {numberField("Max concurrent runs", maxConcurrentRuns, setMaxConcurrentRuns, 4)}
            {numberField("Step budget", stepBudget, setStepBudget)}
          </div>

          <button
            type="submit"
            disabled={issuing || !agent.trim() || picked.length === 0}
            className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
          >
            {issuing ? "Issuing…" : "Issue grant"}
          </button>
        </form>
      </section>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {/* Issued grants (active + revoked). */}
      <section className="rounded-md border border-border">
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : grants.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">No grants issued.</p>
        ) : (
          <ul className="divide-y divide-border">
            {grants.map((g) => (
              <li key={g.id} className="space-y-1 p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="min-w-0 font-mono">{g.agent}</span>
                  <div className="flex items-center gap-3">
                    <span className="text-muted-foreground">{g.status}</span>
                    {g.status === "active" && (
                      <button
                        onClick={() => void onRevoke(g.id)}
                        disabled={revokingId === g.id}
                        className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground disabled:opacity-60"
                      >
                        {revokingId === g.id ? "Revoking…" : "Revoke"}
                      </button>
                    )}
                  </div>
                </div>
                <p className="text-xs text-muted-foreground">
                  {g.resources.join(", ")} · verbs {g.verbs.join(", ")} · ≤{g.maxStepsPerRun} steps/run · ≤
                  {g.maxConcurrentRuns} concurrent · budget {g.stepBudget} · expires{" "}
                  <span className="font-mono">{g.expiresAt}</span>
                </p>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
