// Workstreams route — the operator's chartered-Workstream surface (FE-20; ADR-0063). A
// Workstream is a standing objective whose IMMUTABLE plan template fires governed Runs on
// a schedule with nobody present at launch, bounded by its auto-issued delegation grant.
// Chartering is the ONE consent act (GOV-8): objective + template + scope + schedule +
// bounds, reviewed together; editing the work = retire + re-charter. Pause is soft (ticks
// stop, in-flight Runs complete); retire is the kill switch (revokes the grant + cancels
// its in-flight Runs). The list/detail joins the charter's grant (expiry/bounds) and its
// Runs (last fire + history — the EVD-4 scoped-memory read) CLIENT-SIDE from one page of
// /v1/grants and /v1/runs (the ?grantId server filter is deferred; trigger: history
// beyond one page). This UI drives only the audited control plane.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Workstream = components["schemas"]["Workstream"];
type Grant = components["schemas"]["Grant"];
type Run = components["schemas"]["Run"];
type Resource = components["schemas"]["Resource"];
type TemplateStep = { resourceId: string; resourceName: string; command: string };

export function Workstreams() {
  const [workstreams, setWorkstreams] = useState<Workstream[]>([]);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);

  // The charter form (the one consent act).
  const [label, setLabel] = useState("");
  const [objective, setObjective] = useState("");
  const [scope, setScope] = useState<{ id: string; name: string; type?: string }[]>([]);
  const [scopeResourceId, setScopeResourceId] = useState("");
  const [steps, setSteps] = useState<TemplateStep[]>([]);
  const [stepResourceId, setStepResourceId] = useState("");
  const [command, setCommand] = useState("");
  const [verbs, setVerbs] = useState("exec");
  const [ttlSeconds, setTtlSeconds] = useState(86400);
  const [intervalSeconds, setIntervalSeconds] = useState(300);
  const [maxStepsPerRun, setMaxStepsPerRun] = useState(10);
  const [maxConcurrentRuns, setMaxConcurrentRuns] = useState(2);
  const [stepBudget, setStepBudget] = useState(50);
  const [budgetWindowSeconds, setBudgetWindowSeconds] = useState(0); // 0 → server defaults to the interval

  const [chartering, setChartering] = useState(false);
  const [verbBusy, setVerbBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<Workstream | null>(null);

  const loadAll = useCallback(async () => {
    setLoading(true);
    const [ws, gr, rn] = await Promise.all([
      apiClient.GET("/v1/workstreams", {}),
      apiClient.GET("/v1/grants", {}),
      apiClient.GET("/v1/runs", {}),
    ]);
    if (ws.error || !ws.data) {
      setError("Could not load workstreams.");
      setWorkstreams([]);
    } else {
      setWorkstreams(ws.data.items ?? []);
      setError(null);
    }
    setGrants(gr.data?.items ?? []);
    setRuns(rn.data?.items ?? []);
    setLoading(false);
  }, []);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  // Resource picker (FE-12): the Sessions/Runs/Grants pattern.
  useEffect(() => {
    void (async () => {
      const { data } = await apiClient.GET("/v1/resources", {});
      setResources(data?.items ?? []);
    })();
  }, []);

  // Detail: the immutable template rides GET /v1/workstreams/{id} only.
  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    void (async () => {
      const { data } = await apiClient.GET("/v1/workstreams/{id}", { params: { path: { id: selected } } });
      if (data) setDetail(data);
    })();
  }, [selected]);

  const grantOf = (ws: Workstream) => grants.find((g) => g.id === ws.grantId);
  const runsOf = (ws: Workstream) => runs.filter((r) => r.grantId === ws.grantId);
  const lastRunOf = (ws: Workstream) => {
    const rs = runsOf(ws);
    return rs.length > 0 ? rs[rs.length - 1] : undefined; // /v1/runs is id-ascending (uuid v7 ≈ creation order)
  };

  function addScope() {
    const id = scopeResourceId.trim();
    if (!id || scope.some((r) => r.id === id)) return;
    const res = resources.find((r) => r.id === id);
    setScope((p) => [...p, { id, name: res?.name ?? id, type: res?.type }]);
    setScopeResourceId("");
  }

  function addStep() {
    const id = stepResourceId.trim();
    const cmd = command.trim();
    if (!id || !cmd) return;
    const name = resources.find((r) => r.id === id)?.name ?? id;
    setSteps((p) => [...p, { resourceId: id, resourceName: name, command: cmd }]);
    setCommand("");
  }

  async function onCharter() {
    if (!objective.trim() || scope.length === 0 || steps.length === 0) return;
    setChartering(true);
    setError(null);
    try {
      const body: components["schemas"]["WorkstreamCreate"] = {
        label: label.trim(),
        objective: objective.trim(),
        resources: scope.map((r) => r.id),
        verbs: verbs.split(",").map((v) => v.trim()).filter(Boolean),
        ttlSeconds,
        intervalSeconds,
        maxStepsPerRun,
        maxConcurrentRuns,
        stepBudget,
        steps: steps.map((s) => ({ resourceId: s.resourceId, command: s.command })),
      };
      if (budgetWindowSeconds > 0) body.budgetWindowSeconds = budgetWindowSeconds;
      const { error: apiErr } = await apiClient.POST("/v1/workstreams", { body });
      if (apiErr) {
        setError(apiErr.message ? `Could not charter the workstream: ${apiErr.message}` : "Could not charter the workstream.");
        return;
      }
      setLabel("");
      setObjective("");
      setScope([]);
      setSteps([]);
      await loadAll();
    } finally {
      setChartering(false);
    }
  }

  async function onVerb(id: string, verb: "pause" | "resume" | "retire") {
    setVerbBusy(id);
    setError(null);
    try {
      const path =
        verb === "pause" ? "/v1/workstreams/{id}/pause" : verb === "resume" ? "/v1/workstreams/{id}/resume" : "/v1/workstreams/{id}/retire";
      const { error: apiErr } = await apiClient.POST(path, { params: { path: { id } } });
      if (apiErr) setError(`Could not ${verb} the workstream — its state may have changed. Refresh and try again.`);
      await loadAll();
      if (selected === id) {
        const { data } = await apiClient.GET("/v1/workstreams/{id}", { params: { path: { id } } });
        if (data) setDetail(data);
      }
    } finally {
      setVerbBusy(null);
    }
  }

  const numberField = (labelText: string, value: number, set: (n: number) => void, opts?: { max?: number; min?: number; placeholder?: string }) => (
    <label className="text-xs text-muted-foreground">
      {labelText}
      <input
        type="number"
        aria-label={labelText}
        value={value}
        min={opts?.min ?? 1}
        max={opts?.max}
        placeholder={opts?.placeholder}
        onChange={(e) => set(Number(e.target.value))}
        className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
      />
    </label>
  );

  const resourceSelect = (ariaLabel: string, value: string, set: (v: string) => void, options: { id: string; name: string; type?: string }[]) => (
    <select
      aria-label={ariaLabel}
      value={value}
      onChange={(e) => set(e.target.value)}
      className="w-44 rounded-md border border-input bg-background px-3 py-2 text-sm"
    >
      <option value="">{options.length === 0 ? "None available" : "Select a resource…"}</option>
      {options.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
          {r.type ? ` (${r.type})` : ""}
        </option>
      ))}
    </select>
  );

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Workstreams</h1>
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => void loadAll()} className="text-sm text-muted-foreground hover:underline">
            Refresh
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {/* Charter: the one consent act (GOV-8). */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Charter a workstream</h2>
        <form
          aria-label="charter workstream"
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            void onCharter();
          }}
        >
          <input
            aria-label="workstream label"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="label (optional)"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
          <input
            aria-label="objective"
            value={objective}
            onChange={(e) => setObjective(e.target.value)}
            placeholder="the standing objective (e.g. keep the fleet healthy)"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />

          {/* The charter scope — becomes the auto-issued grant's resources. */}
          <div className="flex gap-2">
            {resourceSelect("scope resource", scopeResourceId, setScopeResourceId, resources)}
            <button type="button" onClick={addScope} disabled={!scopeResourceId} className="rounded-md border border-border px-3 py-2 text-sm font-medium disabled:opacity-60">
              Add to scope
            </button>
          </div>
          {scope.length > 0 && (
            <ul aria-label="charter scope" className="divide-y divide-border rounded-md border border-border">
              {scope.map((r) => (
                <li key={r.id} className="flex items-center justify-between p-2 text-sm">
                  <span className="text-muted-foreground">{r.name}</span>
                  <button
                    type="button"
                    onClick={() => {
                      setScope((p) => p.filter((x) => x.id !== r.id));
                      setSteps((p) => p.filter((s) => s.resourceId !== r.id));
                    }}
                    className="rounded-md border border-border px-2 py-1 text-xs"
                  >
                    Remove
                  </button>
                </li>
              ))}
            </ul>
          )}

          {/* The IMMUTABLE template. Step resources are limited to the scope, so
              template ⊆ scope by construction (the api still 422s the invariant). */}
          <div className="flex gap-2">
            {resourceSelect("step resource", stepResourceId, setStepResourceId, scope)}
            <input
              aria-label="step command"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="command to run each tick"
              className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
            />
            <button type="button" onClick={addStep} disabled={!stepResourceId || !command.trim()} className="rounded-md border border-border px-3 py-2 text-sm font-medium disabled:opacity-60">
              Add step
            </button>
          </div>
          {steps.length > 0 && (
            <ol aria-label="template steps" className="divide-y divide-border rounded-md border border-border">
              {steps.map((s, i) => (
                <li key={i} className="flex items-start justify-between gap-3 p-3 text-sm">
                  <div className="min-w-0">
                    <span className="text-muted-foreground">
                      {i + 1}. {s.resourceName}
                    </span>
                    <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground">{s.command}</pre>
                  </div>
                  <button type="button" onClick={() => setSteps((p) => p.filter((_, idx) => idx !== i))} className="rounded-md border border-border px-2 py-1 text-xs">
                    Remove
                  </button>
                </li>
              ))}
            </ol>
          )}

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <label className="text-xs text-muted-foreground">
              Verbs
              <input aria-label="verbs" value={verbs} onChange={(e) => setVerbs(e.target.value)} className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm" />
            </label>
            {/* No client-side floor hint: the schedule floor is env-configured
                (60s production default; e2e/smoke lower it — AIREOS_WORKSTREAM_MIN_
                INTERVAL_SECONDS) and the server is authoritative — a sub-floor value
                422s with an inline error (below). A hardcoded client min here would
                silently diverge from whatever the server actually enforces. */}
            {numberField("Interval seconds", intervalSeconds, setIntervalSeconds)}
            {numberField("TTL seconds", ttlSeconds, setTtlSeconds)}
            {numberField("Max steps per run", maxStepsPerRun, setMaxStepsPerRun)}
            {/* api enforces the ceiling (422 above the gateway in-flight cap, PERF-7). */}
            {numberField("Max concurrent runs", maxConcurrentRuns, setMaxConcurrentRuns, { max: 4 })}
            {numberField("Step budget", stepBudget, setStepBudget)}
            {numberField("Budget window seconds", budgetWindowSeconds, setBudgetWindowSeconds, { min: 0, placeholder: "0 = the interval" })}
          </div>

          {/* GOV-8: the consent copy — what chartering authorizes, stated at the act. */}
          <p className="text-xs text-muted-foreground">
            Chartering consents to <strong>unattended, scheduled execution</strong> of exactly this template within this
            scope — every interval, with nobody present at launch — until you retire it or its TTL expires. The template
            is immutable (to change it, retire and re-charter). Steps on approval-flagged resources still park for a
            live approval each time.
          </p>

          <button
            type="submit"
            disabled={chartering || !objective.trim() || scope.length === 0 || steps.length === 0}
            className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
          >
            {chartering ? "Chartering…" : "Charter workstream"}
          </button>
        </form>
      </section>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {/* The operator's charters: status / next fire / last Run / expiry (joined client-side). */}
      <section className="rounded-md border border-border">
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : workstreams.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">No workstreams chartered.</p>
        ) : (
          <ul className="divide-y divide-border">
            {workstreams.map((ws) => {
              const g = grantOf(ws);
              const last = lastRunOf(ws);
              return (
                <li key={ws.id} className="space-y-2 p-3 text-sm">
                  <div className="flex items-center justify-between">
                    <button type="button" onClick={() => setSelected((cur) => (cur === ws.id ? null : ws.id))} className="min-w-0 text-left hover:underline">
                      <span className="font-medium">{ws.label || ws.objective}</span>
                    </button>
                    <div className="flex items-center gap-3">
                      <span className="text-muted-foreground">{ws.status}</span>
                      {ws.status === "active" && (
                        <button onClick={() => void onVerb(ws.id, "pause")} disabled={verbBusy === ws.id} className="rounded-md border border-border px-2 py-1 text-xs font-medium disabled:opacity-60">
                          Pause
                        </button>
                      )}
                      {ws.status === "paused" && (
                        <button onClick={() => void onVerb(ws.id, "resume")} disabled={verbBusy === ws.id} className="rounded-md border border-border px-2 py-1 text-xs font-medium disabled:opacity-60">
                          Resume
                        </button>
                      )}
                      {ws.status !== "retired" && (
                        <button onClick={() => void onVerb(ws.id, "retire")} disabled={verbBusy === ws.id} className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground disabled:opacity-60">
                          {verbBusy === ws.id ? "Working…" : "Retire"}
                        </button>
                      )}
                    </div>
                  </div>
                  <p className="text-xs text-muted-foreground">
                    every {ws.intervalSeconds}s
                    {ws.status === "active" ? (
                      <>
                        {" "}
                        · next fire <span className="font-mono">{ws.nextFireAt}</span>
                      </>
                    ) : null}
                    {" · last run "}
                    {last ? <span className="font-mono">{last.status}</span> : "never fired"}
                    {g ? (
                      <>
                        {" "}
                        · expires <span className="font-mono">{g.expiresAt}</span>
                      </>
                    ) : null}
                  </p>

                  {selected === ws.id && (
                    <div className="space-y-2 rounded-md border border-border p-3">
                      <p className="text-xs text-muted-foreground">{detail?.objective ?? ws.objective}</p>
                      {g ? (
                        <p className="text-xs text-muted-foreground">
                          scope {g.resources.join(", ")} · verbs {g.verbs.join(", ")} · ≤{g.maxStepsPerRun} steps/run · ≤{g.maxConcurrentRuns} concurrent · budget {g.stepBudget}
                        </p>
                      ) : null}
                      <ol aria-label="charter template" className="divide-y divide-border rounded-md border border-border">
                        {(detail?.steps ?? []).map((s, i) => (
                          <li key={i} className="p-2 text-xs">
                            <span className="font-mono text-muted-foreground">
                              {i + 1}. {s.resourceId}
                            </span>
                            <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-2 py-1 font-mono text-foreground">{s.command}</pre>
                          </li>
                        ))}
                        {(detail?.steps ?? []).length === 0 && <li className="p-2 text-xs text-muted-foreground">Loading template…</li>}
                      </ol>
                      {/* EVD-4: the charter's run history (client-side join on grantId; first page). Each
                          run id is the evidence anchor — open it on /runs for per-step invocation ids. */}
                      <div aria-label="run history" className="space-y-1">
                        <p className="text-xs font-medium">
                          Run history{" "}
                          <Link to="/runs" className="font-normal text-primary hover:underline">
                            (view on Runs →)
                          </Link>
                        </p>
                        {runsOf(ws).length === 0 ? (
                          <p className="text-xs text-muted-foreground">No runs yet — first fire is one interval after chartering.</p>
                        ) : (
                          <ul className="divide-y divide-border rounded-md border border-border">
                            {runsOf(ws)
                              .slice()
                              .reverse()
                              .map((r) => (
                                <li key={r.id} className="flex items-center justify-between p-2 text-xs">
                                  <span className="font-mono text-muted-foreground">{r.id}</span>
                                  <span>{r.status}</span>
                                </li>
                              ))}
                          </ul>
                        )}
                      </div>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
