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
import { MemoryRow, SHARE_CONFIRM_COPY, type MemoryView } from "./Memories";

type Workstream = components["schemas"]["Workstream"];
type Grant = components["schemas"]["Grant"];
type Run = components["schemas"]["Run"];
type Resource = components["schemas"]["Resource"];
type ContextSource = components["schemas"]["ContextSource"];
type TemplateStep = { resourceId: string; resourceName: string; command: string };
type WorkstreamKind = components["schemas"]["WorkstreamCreate"]["kind"] & string;

// GOV-11 (T-100b, ADR-0066 §1): the context-source consent surface — mirrors the tone
// of the v0.9 memory-share confirm copy (SHARE_CONFIRM_COPY above), naming the
// prompt-injection risk honestly rather than burying it in fine print. Shown verbatim
// in the confirm dialog BEFORE a register or a remove commits (no dark patterns).
export const CONTEXT_SOURCE_REGISTER_CONFIRM_COPY =
  "Registering this directory makes its content readable by this charter's composer agent — a prompt-injection and exfiltration risk you are explicitly accepting. Register only content you intend the agent to see. A plan grounded in it still parks for your review before anything runs.";
export const CONTEXT_SOURCE_REMOVE_CONFIRM_COPY =
  "Remove this context source? This stops future agent reads of this directory — it does not and cannot recall anything the agent may already have read from it.";

/** FE-26: a null lastIndexedAt is "never indexed / pending" (the first sweep hasn't run
 * yet); otherwise a coarse relative "last indexed …ago" (the staleness signal, CTX-3). */
function relativeAgo(iso: string): string {
  const diffSec = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  if (diffSec < 5) return "just now";
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.round(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  return `${Math.round(diffHr / 24)}d ago`;
}

export function Workstreams() {
  const [workstreams, setWorkstreams] = useState<Workstream[]>([]);
  const [grants, setGrants] = useState<Grant[]>([]);
  const [runs, setRuns] = useState<Run[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);

  // The charter form (the one consent act).
  const [label, setLabel] = useState("");
  const [objective, setObjective] = useState("");
  const [kind, setKind] = useState<WorkstreamKind>("template");
  const [composer, setComposer] = useState(""); // required for kind=adaptive (D4)
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
  const [memVerbBusy, setMemVerbBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<Workstream | null>(null);
  const [memories, setMemories] = useState<MemoryView[]>([]);

  // FE-26 (ADR-0066 §1, T-100b): a charter's bound context sources — fetched fresh
  // whenever the detail opens (the FE-22 memory precedent — no auto-poll).
  const [contextSources, setContextSources] = useState<ContextSource[]>([]);
  const [csRootPath, setCsRootPath] = useState("");
  const [csRegistering, setCsRegistering] = useState(false);
  const [csVerbBusy, setCsVerbBusy] = useState<string | null>(null);

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

  // FE-22: the charter's scoped memory (accrued pointers + any authored notes) — the
  // v0.7 EVD-1 read-time slice promoted to read the persisted store, fetched once when
  // the detail opens (no auto-poll; visit /memories for the full operator surface).
  useEffect(() => {
    if (!selected) {
      setMemories([]);
      return;
    }
    void (async () => {
      const { data } = await apiClient.GET("/v1/memories", {
        params: { query: { scopeType: "workstream", scopeId: selected } },
      });
      setMemories(data ?? []);
    })();
  }, [selected]);

  // FE-26: the charter's bound context sources (CTX-1) — same fetch-on-select shape as
  // the FE-22 scoped-memory effect above.
  const loadContextSources = useCallback(async (workstreamId: string) => {
    const { data } = await apiClient.GET("/v1/context-sources", { params: { query: { workstreamId } } });
    setContextSources(data ?? []);
  }, []);

  useEffect(() => {
    if (!selected) {
      setContextSources([]);
      return;
    }
    void loadContextSources(selected);
  }, [selected, loadContextSources]);

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

  // D4 (ADR-0065 §4): a template charter needs >=1 step (the immutable plan); an
  // adaptive charter needs a named composer instead and carries NO steps.
  const charterReady =
    Boolean(objective.trim()) && scope.length > 0 && (kind === "adaptive" ? Boolean(composer.trim()) : steps.length > 0);

  async function onCharter() {
    if (!charterReady) return;
    setChartering(true);
    setError(null);
    try {
      const body: components["schemas"]["WorkstreamCreate"] = {
        label: label.trim(),
        objective: objective.trim(),
        kind, // always explicit — "template" here is byte-identical to the pre-v0.9 implicit default
        resources: scope.map((r) => r.id),
        verbs: verbs.split(",").map((v) => v.trim()).filter(Boolean),
        ttlSeconds,
        intervalSeconds,
        maxStepsPerRun,
        maxConcurrentRuns,
        stepBudget,
      };
      if (budgetWindowSeconds > 0) body.budgetWindowSeconds = budgetWindowSeconds;
      if (kind === "adaptive") {
        body.composer = composer.trim();
      } else {
        body.steps = steps.map((s) => ({ resourceId: s.resourceId, command: s.command }));
      }
      const { error: apiErr } = await apiClient.POST("/v1/workstreams", { body });
      if (apiErr) {
        setError(apiErr.message ? `Could not charter the workstream: ${apiErr.message}` : "Could not charter the workstream.");
        return;
      }
      setLabel("");
      setObjective("");
      setComposer("");
      setScope([]);
      setSteps([]);
      await loadAll();
    } finally {
      setChartering(false);
    }
  }

  // R2/GOV-10 (MEM-6, T-091): the read-only FE-22 scoped-memory promotion gains the
  // share control (FE-25) — redact/pin stay off this surface (unchanged), but sharing/
  // unsharing what the composer can read belongs exactly where the composer relationship
  // is chartered. Same confirm copy as /memories.
  async function onToggleShare(id: string, shared: boolean) {
    if (!window.confirm(SHARE_CONFIRM_COPY)) return;
    setMemVerbBusy(id);
    setError(null);
    try {
      const path = shared ? "/v1/memories/{id}/unshare" : "/v1/memories/{id}/share";
      const { data, error: apiErr } = await apiClient.POST(path, { params: { path: { id } } });
      if (apiErr) setError(`Could not ${shared ? "unshare" : "share"} the memory. Refresh and try again.`);
      else if (data) setMemories((m) => m.map((x) => (x.id === id ? data : x)));
    } finally {
      setMemVerbBusy(null);
    }
  }

  // FE-26/GOV-11 (T-100b, ADR-0066 §1): register a charter-bound context source. The
  // GOV-11 confirm copy fires BEFORE the POST commits — no dark patterns.
  async function onRegisterContextSource() {
    const rootPath = csRootPath.trim();
    if (!rootPath || !selected) return;
    if (!window.confirm(CONTEXT_SOURCE_REGISTER_CONFIRM_COPY)) return;
    setCsRegistering(true);
    setError(null);
    try {
      const { data, error: apiErr } = await apiClient.POST("/v1/context-sources", {
        body: { rootPath, workstreamId: selected },
      });
      if (apiErr || !data) {
        setError(
          apiErr?.message ? `Could not register the context source: ${apiErr.message}` : "Could not register the context source.",
        );
        return;
      }
      setCsRootPath("");
      setContextSources((cs) => [...cs, data]);
    } finally {
      setCsRegistering(false);
    }
  }

  // GOV-11: removal stops future agent reads but cannot recall past ones — stated at
  // the act, same confirm-before-commit shape as the memory redact/share verbs.
  async function onRemoveContextSource(id: string) {
    if (!window.confirm(CONTEXT_SOURCE_REMOVE_CONFIRM_COPY)) return;
    setCsVerbBusy(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.DELETE("/v1/context-sources/{id}", { params: { path: { id } } });
      if (apiErr) {
        setError("Could not remove the context source. Refresh and try again.");
        return;
      }
      setContextSources((cs) => cs.filter((s) => s.id !== id));
    } finally {
      setCsVerbBusy(null);
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

          {/* D4 (ADR-0065 §4): kind picker — template (default, byte-identical existing
              flow) vs. adaptive (no step editor; a named composer authors each tick's
              plan instead). */}
          <div className="flex gap-2">
            <select
              aria-label="workstream kind"
              value={kind}
              onChange={(e) => setKind(e.target.value as WorkstreamKind)}
              className="w-40 rounded-md border border-input bg-background px-3 py-2 text-sm"
            >
              <option value="template">Template</option>
              <option value="adaptive">Adaptive</option>
            </select>
            {kind === "adaptive" && (
              <input
                aria-label="composer agent"
                value={composer}
                onChange={(e) => setComposer(e.target.value)}
                placeholder="composer agent subject (e.g. agent:ops-planner)"
                className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
              />
            )}
          </div>

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

          {/* The IMMUTABLE template — template charters only (D4); an adaptive charter
              hides this entirely — the composer authors each tick's plan instead, and
              zero template steps is a structural requirement (the store's kind matrix,
              422 on a mismatch). Step resources are limited to the scope, so
              template ⊆ scope by construction (the api still 422s the invariant). */}
          {kind === "template" && (
            <>
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
            </>
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

          {/* GOV-8 (template) / GOV-10 (adaptive): the consent copy — what chartering
              authorizes, stated at the act. No dark patterns. */}
          {kind === "template" ? (
            <p className="text-xs text-muted-foreground">
              Chartering consents to <strong>unattended, scheduled execution</strong> of exactly this template within this
              scope — every interval, with nobody present at launch — until you retire it or its TTL expires. The template
              is immutable (to change it, retire and re-charter). Steps on approval-flagged resources still park for a
              live approval each time.
            </p>
          ) : (
            <p className="text-xs text-muted-foreground">
              Chartering consents to letting <strong>{composer.trim() || "the named agent"}</strong> author each tick's
              plan within this scope. That plan <strong>always parks for your review</strong> before anything runs —
              nothing executes unattended. The agent can read this charter's accrued memory (content-free evidence
              pointers) plus anything you explicitly share on <Link to="/memories" className="text-primary hover:underline">Memory</Link>.
            </p>
          )}

          <button
            type="submit"
            disabled={chartering || !charterReady}
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
                      <div className="flex items-center gap-2">
                        <span className="rounded-md border border-border px-2 py-0.5 text-xs font-medium">{detail?.kind ?? ws.kind ?? "template"}</span>
                        {(detail?.kind ?? ws.kind) === "adaptive" && (detail?.composer ?? ws.composer) ? (
                          <span className="text-xs text-muted-foreground">
                            composer <span className="font-mono">{detail?.composer ?? ws.composer}</span>
                          </span>
                        ) : null}
                      </div>
                      <p className="text-xs text-muted-foreground">{detail?.objective ?? ws.objective}</p>
                      {g ? (
                        <p className="text-xs text-muted-foreground">
                          scope {g.resources.join(", ")} · verbs {g.verbs.join(", ")} · ≤{g.maxStepsPerRun} steps/run · ≤{g.maxConcurrentRuns} concurrent · budget {g.stepBudget}
                        </p>
                      ) : null}
                      {(detail?.kind ?? ws.kind) === "adaptive" ? (
                        <p className="text-xs text-muted-foreground">Adaptive charter — no immutable template; the named composer authors each tick's plan.</p>
                      ) : (
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
                      )}
                      {/* D3 (ADR-0065 §5): the inbound plan-request channel's bounded history —
                          open (with its TTL), submitted (its Run), or expired. Adaptive charters
                          only (a template charter never opens one). */}
                      {(detail?.kind ?? ws.kind) === "adaptive" && (
                        <div aria-label="plan requests" className="space-y-1">
                          <p className="text-xs font-medium">Plan requests</p>
                          {(detail?.planRequests ?? []).length === 0 ? (
                            <p className="text-xs text-muted-foreground">No plan requests yet.</p>
                          ) : (
                            <ul className="divide-y divide-border rounded-md border border-border">
                              {(detail?.planRequests ?? []).map((pr) => (
                                <li key={pr.id} className="flex items-center justify-between p-2 text-xs">
                                  <span className="font-mono text-muted-foreground">{pr.id}</span>
                                  {pr.status === "open" ? (
                                    <span>
                                      open · expires <span className="font-mono">{pr.expiresAt}</span>
                                    </span>
                                  ) : pr.status === "submitted" && pr.runId ? (
                                    <span>
                                      submitted ·{" "}
                                      <Link to="/runs" className="text-primary hover:underline">
                                        run <span className="font-mono">{pr.runId}</span>
                                      </Link>
                                    </span>
                                  ) : (
                                    <span>{pr.status}</span>
                                  )}
                                </li>
                              ))}
                            </ul>
                          )}
                        </div>
                      )}
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

                      {/* FE-22: the persisted-store promotion of the EVD-1 slice —
                          accrued pointers beside any authored notes, read-only here. */}
                      <div aria-label="scoped memory" className="space-y-1">
                        <p className="text-xs font-medium">
                          Scoped memory{" "}
                          <Link to="/memories" className="font-normal text-primary hover:underline">
                            (view on Memory →)
                          </Link>
                        </p>
                        {memories.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No memory recorded yet.</p>
                        ) : (
                          <ul className="divide-y divide-border rounded-md border border-border">
                            {memories.map((m) => (
                              <MemoryRow key={m.id} m={m} onToggleShare={onToggleShare} busy={memVerbBusy === m.id} />
                            ))}
                          </ul>
                        )}
                      </div>

                      {/* FE-26 (ADR-0066 §1, T-100b): the charter's bound context
                          sources — a source's content becomes part of what this
                          charter's composer agent can search (context.search). */}
                      <div aria-label="context sources" className="space-y-2">
                        <p className="text-xs font-medium">Context sources</p>
                        {contextSources.length === 0 ? (
                          <p className="text-xs text-muted-foreground">No context sources registered.</p>
                        ) : (
                          <ul className="divide-y divide-border rounded-md border border-border">
                            {contextSources.map((cs) => (
                              <li key={cs.id} className="space-y-1 p-2 text-xs">
                                <div className="flex items-center justify-between gap-2">
                                  <span className="min-w-0 truncate font-mono text-foreground">{cs.rootPath}</span>
                                  <span
                                    className={`shrink-0 rounded-md border px-2 py-0.5 font-medium ${
                                      cs.status === "errored"
                                        ? "border-destructive/40 bg-destructive/10 text-destructive"
                                        : "border-border text-muted-foreground"
                                    }`}
                                  >
                                    {cs.status}
                                  </span>
                                </div>
                                <div className="flex items-center justify-between gap-2 text-muted-foreground">
                                  <span>{cs.lastIndexedAt ? `last indexed ${relativeAgo(cs.lastIndexedAt)}` : "never indexed / pending"}</span>
                                  <button
                                    type="button"
                                    onClick={() => void onRemoveContextSource(cs.id)}
                                    disabled={csVerbBusy === cs.id}
                                    className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground disabled:opacity-60"
                                  >
                                    {csVerbBusy === cs.id ? "Working…" : "Remove"}
                                  </button>
                                </div>
                              </li>
                            ))}
                          </ul>
                        )}

                        <form
                          aria-label="register context source"
                          className="flex gap-2"
                          onSubmit={(e) => {
                            e.preventDefault();
                            void onRegisterContextSource();
                          }}
                        >
                          <input
                            aria-label="root path"
                            value={csRootPath}
                            onChange={(e) => setCsRootPath(e.target.value)}
                            placeholder="/docs/runbooks (the directory to index)"
                            className="flex-1 rounded-md border border-input bg-background px-2 py-1 text-xs font-mono"
                          />
                          <button
                            type="submit"
                            disabled={csRegistering || !csRootPath.trim()}
                            className="rounded-md bg-primary px-3 py-1 text-xs font-medium text-primary-foreground disabled:opacity-60"
                          >
                            {csRegistering ? "Registering…" : "Register"}
                          </button>
                        </form>
                        {/* GOV-11: the consent copy stated at the surface, beside the
                            act (the confirm dialog restates it before the POST commits). */}
                        <p className="text-xs text-muted-foreground">
                          Registering a directory here makes its content <strong>readable by this charter's composer
                          agent</strong> — register only content you intend the agent to see. Removing a source stops
                          future agent reads but cannot recall past ones.
                        </p>
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
