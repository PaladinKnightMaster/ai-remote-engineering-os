// Runs route — the operator's governed multi-step Run surface (FE-14/15; ADR-0061).
// An operator composes an ordered plan of remote.exec steps, reviews the FULL plan
// verbatim (the informed-launch gate, GOV-1) before launching, then watches per-step
// status and can cancel a pending/running Run (DUR-5). Drives only the audited control
// plane: POST /v1/runs hands the plan to the api sequencer, which runs each step THROUGH
// the gateway (the sole governed-step orchestrator) — a flagged step still parks on the
// /invocations console for approval. This UI never dials a resource directly.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type Run = components["schemas"]["Run"];
type Resource = components["schemas"]["Resource"];
type PlanStep = { resourceId: string; resourceName: string; command: string };

// DUR-5: only a pending/running Run is cancelable; the rest are terminal.
const TERMINAL = new Set(["completed", "failed", "aborted", "canceled"]);
const isTerminal = (status: string) => TERMINAL.has(status);
const isCancelable = (status: string) => status === "pending" || status === "running";

export function Runs() {
  const [runs, setRuns] = useState<Run[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [plan, setPlan] = useState<PlanStep[]>([]);
  const [resourceId, setResourceId] = useState("");
  const [command, setCommand] = useState("");
  const [reviewing, setReviewing] = useState(false);
  const [launching, setLaunching] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<Run | null>(null);
  const [cancelingId, setCancelingId] = useState<string | null>(null);
  const [resolvingId, setResolvingId] = useState<string | null>(null); // approve/deny a 'proposed' Run (FE-24)

  const loadRuns = useCallback(async () => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/runs", {});
    if (apiErr || !data) {
      setError("Could not load runs.");
      setRuns([]);
    } else {
      setRuns(data.items ?? []);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void loadRuns();
  }, [loadRuns]);

  // Resource picker (FE-12): reuse the Sessions pattern so the operator selects a
  // resource by name instead of pasting a UUID. Read-only; failure leaves it empty.
  useEffect(() => {
    void (async () => {
      const { data } = await apiClient.GET("/v1/resources", {});
      setResources(data?.items ?? []);
    })();
  }, []);

  // Poll the selected Run's per-step status until it reaches a terminal state.
  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    let active = true;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      const { data } = await apiClient.GET("/v1/runs/{id}", { params: { path: { id: selected } } });
      if (!active) return;
      if (data) setDetail(data);
      if (data && isTerminal(data.status)) {
        void loadRuns(); // the list status caught up; stop polling.
        return;
      }
      timer = setTimeout(() => void tick(), 2000);
    };
    void tick();
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [selected, loadRuns]);

  function addStep() {
    const id = resourceId.trim();
    const cmd = command.trim();
    if (!id || !cmd) return;
    const name = resources.find((r) => r.id === id)?.name ?? id;
    setPlan((p) => [...p, { resourceId: id, resourceName: name, command: cmd }]);
    setCommand("");
    setReviewing(false);
  }

  function removeStep(i: number) {
    setPlan((p) => p.filter((_, idx) => idx !== i));
    setReviewing(false);
  }

  async function onConfirmLaunch() {
    if (plan.length === 0) return;
    setLaunching(true);
    setError(null);
    try {
      const { data, error: apiErr } = await apiClient.POST("/v1/runs", {
        body: { steps: plan.map((s) => ({ resourceId: s.resourceId, command: s.command })) },
      });
      if (apiErr || !data) {
        setError(apiErr?.message ? `Could not launch the run: ${apiErr.message}` : "Could not launch the run.");
        return;
      }
      setPlan([]);
      setReviewing(false);
      setSelected(data.id); // watch the new Run's per-step status
      await loadRuns();
    } finally {
      setLaunching(false);
    }
  }

  async function onCancel(id: string) {
    setCancelingId(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/runs/{id}/cancel", {
        params: { path: { id } },
      });
      if (apiErr) {
        setError("Could not cancel the run — it may have already finished. Refresh and try again.");
      }
      await loadRuns();
      if (selected === id) {
        const { data } = await apiClient.GET("/v1/runs/{id}", { params: { path: { id } } });
        if (data) setDetail(data);
      }
    } finally {
      setCancelingId(null);
    }
  }

  // FE-24 (RUN-6, ADR-0065 §6): resolve a 'proposed' Run — the plan-preview park's
  // consent surface. Approve restates the grant it launches under (GOV-10: consent
  // binds to THIS tick's reviewed plan); deny is equally first-class, no extra
  // friction. 403 (a dead bounding grant) and 409 (already resolved by a race) are
  // surfaced honestly rather than as a generic failure.
  async function onResolveProposed(id: string, decision: "approve" | "deny") {
    if (decision === "approve" && !window.confirm("Approving launches this agent-composed plan, exactly as shown below, under the charter's grant.")) {
      return;
    }
    setResolvingId(id);
    setError(null);
    try {
      const path = decision === "approve" ? "/v1/runs/{id}/approve" : "/v1/runs/{id}/deny";
      const { error: apiErr } = await apiClient.POST(path, { params: { path: { id } } });
      if (apiErr) {
        setError(
          apiErr.message
            ? `Could not ${decision} the run: ${apiErr.message}`
            : `Could not ${decision} the run — it may no longer be proposed. Refresh and try again.`,
        );
      }
      await loadRuns();
      if (selected === id) {
        const { data } = await apiClient.GET("/v1/runs/{id}", { params: { path: { id } } });
        if (data) setDetail(data);
      }
    } finally {
      setResolvingId(null);
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Runs</h1>
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" type="button" onClick={() => void loadRuns()}>
            Refresh
          </Button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {/* Compose: build an ordered plan of remote.exec steps. */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Compose a run</h2>
        <form
          aria-label="compose run step"
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addStep();
          }}
        >
          <select
            name="resourceId"
            aria-label="resource"
            value={resourceId}
            onChange={(e) => setResourceId(e.target.value)}
            className="w-44 rounded-md border border-input bg-background px-3 py-2 text-sm"
          >
            <option value="">
              {resources.length === 0 ? "No resources" : "Select a resource…"}
            </option>
            {resources.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name} ({r.type})
              </option>
            ))}
          </select>
          <input
            name="command"
            aria-label="command"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="command to run"
            className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
          />
          <Button variant="default" size="md" type="submit" disabled={!resourceId || !command.trim()}>
            Add step
          </Button>
        </form>

        {plan.length > 0 && (
          <ol className="divide-y divide-border rounded-md border border-border">
            {plan.map((s, i) => (
              <li key={i} className="flex items-start justify-between gap-3 p-3 text-sm">
                <div className="min-w-0">
                  <span className="text-muted-foreground">
                    {i + 1}. {s.resourceName}
                  </span>
                  <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground">{s.command}</pre>
                </div>
                <Button variant="default" size="xs" onClick={() => removeStep(i)}>
                  Remove
                </Button>
              </li>
            ))}
          </ol>
        )}

        {plan.length > 0 && !reviewing && (
          <Button variant="primary" size="md" type="button" onClick={() => setReviewing(true)}>
            Review &amp; launch
          </Button>
        )}
      </section>

      {/* Informed launch (GOV-1): the full ordered plan, every command verbatim, is
          shown for review before the operator authorizes unattended execution. */}
      {reviewing && (
        <section role="region" aria-label="launch review" className="space-y-3 rounded-md border border-border bg-background p-4">
          <div className="space-y-1">
            <h2 className="text-lg font-semibold">Review &amp; launch</h2>
            <p className="text-sm text-muted-foreground">
              Launching authorizes unattended execution of every step below, in order. A flagged
              step still parks for approval on the Pending tool invocations console.
            </p>
          </div>
          <ol className="space-y-2">
            {plan.map((s, i) => (
              <li key={i} className="text-sm">
                <span className="text-muted-foreground">
                  Step {i + 1} · {s.resourceName}
                </span>
                <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground" aria-label="command">{s.command}</pre>
              </li>
            ))}
          </ol>
          <div className="flex gap-2">
            <Button variant="primary" size="md" type="button" onClick={() => void onConfirmLaunch()} disabled={launching}>
              {launching ? "Launching…" : "Confirm launch"}
            </Button>
            <Button variant="default" size="md" type="button" onClick={() => setReviewing(false)} disabled={launching}>
              Back
            </Button>
          </div>
        </section>
      )}

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {/* The operator's Runs + per-step status. */}
      <section className="rounded-md border border-border">
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : runs.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">No runs yet.</p>
        ) : (
          <ul className="divide-y divide-border">
            {runs.map((r) => (
              <li key={r.id} className="space-y-2 p-3 text-sm">
                <div className="flex items-center justify-between">
                  {/* T-117/FE-37: excluded from the Button migration — this is a
                      clickable table-row toggle (expand/collapse detail), not a
                      button that PRESENTS as a button (no border/background/
                      padding, mono id text). FE-37's own wording ("every
                      <button> that presents as a button") does not reach it. */}
                  <button
                    type="button"
                    onClick={() => setSelected((cur) => (cur === r.id ? null : r.id))}
                    className="min-w-0 text-left font-mono hover:underline"
                  >
                    {r.id}
                  </button>
                  <div className="flex items-center gap-3">
                    {/* FE-24: 'proposed' is a distinct chip — the plan-preview park (RUN-6). */}
                    {r.status === "proposed" ? (
                      <span className="rounded-md border border-border px-2 py-0.5 text-xs font-medium">proposed</span>
                    ) : (
                      <span className="text-muted-foreground">{r.status}</span>
                    )}
                    {isCancelable(r.status) && (
                      <Button
                        variant="destructive"
                        size="xs"
                        onClick={() => void onCancel(r.id)}
                        disabled={cancelingId === r.id}
                      >
                        {cancelingId === r.id ? "Canceling…" : "Cancel"}
                      </Button>
                    )}
                  </div>
                </div>

                {/* FE-19 (ADR-0062): a delegated Run is agent-driven under an operator's grant. */}
                {r.actor ? (
                  <p className="text-xs text-muted-foreground">
                    Driven by <span className="font-mono">{r.actor}</span>
                    {r.grantId ? (
                      <>
                        {" "}
                        · grant <span className="font-mono">{r.grantId}</span>
                      </>
                    ) : null}
                  </p>
                ) : null}

                {selected === r.id && r.status === "proposed" ? (
                  // FE-24 (RUN-6, GOV-10, ADR-0065 §6): the plan-preview review — every
                  // step's resource + the D2 command, verbatim, before approving launches
                  // it under the charter's grant. The ApprovalPrompt visual language
                  // (monospace command block, primary/destructive action pair) without
                  // reusing the component itself (it is shaped for one resource/command
                  // pair, not an ordered multi-step plan).
                  <section role="region" aria-label="plan preview" className="space-y-3 rounded-md border border-border bg-background p-3">
                    <p className="text-xs text-muted-foreground">
                      An agent-composed plan is parked for your review. Approving launches every step below, in
                      order, under the charter's grant — nothing runs until you decide. Deny is equally first-class.
                    </p>
                    <ol aria-label="proposed plan" className="space-y-2">
                      {(detail?.steps ?? []).map((s) => (
                        <li key={s.stepIndex} className="text-xs">
                          <span className="text-muted-foreground">
                            Step {s.stepIndex + 1} · {s.resourceId}
                          </span>
                          <pre className="mt-1 overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground" aria-label="command">
                            {s.command}
                          </pre>
                        </li>
                      ))}
                      {(detail?.steps ?? []).length === 0 && <li className="text-xs text-muted-foreground">Loading plan…</li>}
                    </ol>
                    <div className="flex gap-2">
                      <Button
                        variant="primary"
                        size="md"
                        type="button"
                        onClick={() => void onResolveProposed(r.id, "approve")}
                        disabled={resolvingId === r.id}
                      >
                        {resolvingId === r.id ? "Working…" : "Approve"}
                      </Button>
                      <Button
                        variant="destructive"
                        size="md"
                        type="button"
                        onClick={() => void onResolveProposed(r.id, "deny")}
                        disabled={resolvingId === r.id}
                      >
                        Deny
                      </Button>
                    </div>
                  </section>
                ) : (
                  selected === r.id && (
                    <ol aria-label="run steps" className="divide-y divide-border rounded-md border border-border">
                      {(detail?.steps ?? []).map((s) => (
                        <li key={s.stepIndex} className="flex items-center justify-between gap-3 p-2 text-xs">
                          <span className="font-mono text-muted-foreground">
                            {s.stepIndex + 1}. {s.resourceId}
                          </span>
                          <span>
                            {s.status}
                            {s.outcome ? ` · ${s.outcome}` : ""}
                          </span>
                        </li>
                      ))}
                      {(detail?.steps ?? []).length === 0 && (
                        <li className="p-2 text-xs text-muted-foreground">Loading steps…</li>
                      )}
                    </ol>
                  )
                )}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
