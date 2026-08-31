// PolicyRules route — the operator's policy-rule authoring surface (T-152; FE-24;
// ADR-0074). policy-service evaluates operator-authored rules over the tuple
// (subject, edge, action, target) — each field an exact value or the wildcard "*" — to
// an outcome of allow | requires_approval | deny (§Decision cl.1). Rules are
// operator-authored only; no agent, run, charter or grant may create, edit or enable
// one. Evaluation takes the MOST SEVERE match across every enabled rule — deny >
// requires_approval > allow — and specificity does NOT override severity: a wildcard
// deny beats a fully-specific allow, with no tiebreak at all (§Decision cl.2).
//
// FE-25 (block): a rule's blast radius must be visible BEFORE it is saved. FE-25a (the
// T-152 pre-authoring audit) ratified how: rendered INLINE and always-visible in the
// create form below, not a window.confirm — a modal only satisfies "before" at the last
// instant, inline satisfies it continuously, and the actual hazard is comprehension
// (an operator who writes `deny * * * *` must understand their next, more-specific
// allow will NOT rescue it) which a confirm dialog cannot teach. See
// describeBlastRadius below, which names every wildcarded field as what it is and adds
// the severity-beats-specificity warning for a wildcard-bearing deny. This route
// deliberately does NOT add a seventh window.confirm site to the recorded B-018/B-037
// debt (six live call sites in Memories/Runs/Workstreams) — FE-25a's own copy says a
// confirm-on-submit MAY be layered on for house consistency, but it is optional, and
// the inline rendering already satisfies the block criterion on its own.
//
// FE-26: an invocation's decision cannot yet be attributed to the rule that produced
// it — PendingInvocation and AuditEvent carry no ruleId on the wire (checked against
// packages/shared-types/src/generated/openapi.ts: no such field exists anywhere in the
// generated types), because naming it would extend the wire schema and change what the
// chained hash attests to (a deferred ADR, not an oversight — see the T-152 brief).
// This route shows every rule's label so it exists to be read; it does not fabricate a
// client-side link from a decision back to a rule.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type PolicyRule = components["schemas"]["PolicyRule"];
type Outcome = PolicyRule["outcome"];

const WILDCARD = "*";
const FIELD_NAMES = ["subject", "edge", "action", "target"] as const;

/**
 * FE-25 / FE-25a — the blast-radius sentence rendered inline in the create form.
 * Returns null for a fully-specific rule (no field wildcarded): there is nothing to
 * warn about, and the mutation proof for FE-25 asserts exactly that this text is
 * ABSENT in that case. Exported so the test file can exercise it directly as well as
 * through the rendered form.
 */
export function describeBlastRadius(
  subject: string,
  edge: string,
  action: string,
  target: string,
  outcome: Outcome,
): string | null {
  const values = { subject: subject.trim(), edge: edge.trim(), action: action.trim(), target: target.trim() };
  const hasWildcard = FIELD_NAMES.some((name) => values[name] === WILDCARD);
  if (!hasWildcard) return null;

  const matches = FIELD_NAMES.map((name) => {
    const v = values[name];
    if (v === WILDCARD) return `every ${name}`;
    if (!v) return `an unset ${name}`;
    return `${name} "${v}"`;
  }).join(", ");

  let text = `This rule matches ${matches}.`;
  if (outcome === "deny") {
    text +=
      " Because it DENIES and severity beats specificity, no more-specific ALLOW rule written " +
      "afterward can override it — every matching invocation stays blocked.";
  }
  return text;
}

export function PolicyRules() {
  const [rules, setRules] = useState<PolicyRule[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const [label, setLabel] = useState("");
  const [subject, setSubject] = useState("");
  const [edge, setEdge] = useState("");
  const [action, setAction] = useState("");
  const [target, setTarget] = useState("");
  const [outcome, setOutcome] = useState<Outcome>("requires_approval");
  const [creating, setCreating] = useState(false);

  const loadRules = useCallback(async () => {
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/policy-rules", {});
    if (apiErr || !data) {
      setError("Could not load policy rules.");
      setRules([]);
    } else {
      setRules(data);
      setError(null);
    }
    setLoading(false);
  }, []);

  useEffect(() => {
    void loadRules();
  }, [loadRules]);

  const blastRadius = useMemo(
    () => describeBlastRadius(subject, edge, action, target, outcome),
    [subject, edge, action, target, outcome],
  );

  async function onCreate() {
    if (!label.trim() || !subject.trim() || !edge.trim() || !action.trim() || !target.trim()) return;
    setCreating(true);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/policy-rules", {
        body: {
          label: label.trim(),
          subject: subject.trim(),
          edge: edge.trim(),
          action: action.trim(),
          target: target.trim(),
          outcome,
        },
      });
      if (apiErr) {
        setError(apiErr.message ? `Could not create the rule: ${apiErr.message}` : "Could not create the rule.");
        return;
      }
      setLabel("");
      setSubject("");
      setEdge("");
      setAction("");
      setTarget("");
      setOutcome("requires_approval");
      await loadRules();
    } finally {
      setCreating(false);
    }
  }

  async function onToggleEnabled(rule: PolicyRule) {
    setBusyId(rule.id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.PATCH("/v1/policy-rules/{id}", {
        params: { path: { id: rule.id } },
        body: { enabled: !rule.enabled },
      });
      if (apiErr) setError("Could not update the rule. Refresh and try again.");
      await loadRules();
    } finally {
      setBusyId(null);
    }
  }

  async function onDelete(id: string) {
    setBusyId(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.DELETE("/v1/policy-rules/{id}", { params: { path: { id } } });
      if (apiErr) setError("Could not delete the rule. Refresh and try again.");
      await loadRules();
    } finally {
      setBusyId(null);
    }
  }

  const textField = (labelText: string, value: string, set: (v: string) => void, placeholder: string) => (
    <label className="text-xs text-muted-foreground">
      {labelText}
      <input
        aria-label={labelText}
        value={value}
        onChange={(e) => set(e.target.value)}
        placeholder={placeholder}
        className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm font-mono"
      />
    </label>
  );

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Policy rules</h1>
        <div className="flex items-center gap-3">
          <Button variant="ghost" size="sm" type="button" onClick={() => void loadRules()}>
            Refresh
          </Button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {/* Author a rule: operator-only (ADR-0074 cl.1). */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Author a rule</h2>
        <form
          aria-label="author rule"
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            void onCreate();
          }}
        >
          <input
            aria-label="label"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="label (e.g. block prod-db writes)"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            {textField("Subject", subject, setSubject, "agent:bot-1 or *")}
            {textField("Edge", edge, setEdge, "agent-remote-session or *")}
            {textField("Action", action, setAction, "remote.exec or *")}
            {textField("Target", target, setTarget, "res-1 or *")}
          </div>

          <label className="text-xs text-muted-foreground">
            Outcome
            <select
              aria-label="outcome"
              value={outcome}
              onChange={(e) => setOutcome(e.target.value as Outcome)}
              className="mt-1 block w-48 rounded-md border border-input bg-background px-2 py-1 text-sm"
            >
              <option value="allow">allow</option>
              <option value="requires_approval">requires_approval</option>
              <option value="deny">deny</option>
            </select>
          </label>

          {/* FE-25 (block) / FE-25a: blast radius, inline and always-visible while the
              rule is still being composed — not a confirm dialog at submit time. */}
          {blastRadius && (
            <p
              role="note"
              aria-label="blast radius"
              className="rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-800 dark:text-amber-400"
            >
              {blastRadius}
            </p>
          )}

          <Button
            variant="primary"
            size="md"
            type="submit"
            disabled={
              creating || !label.trim() || !subject.trim() || !edge.trim() || !action.trim() || !target.trim()
            }
          >
            {creating ? "Creating…" : "Create rule"}
          </Button>
        </form>
      </section>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {/* Authored rules (enabled and disabled alike — CON-2, no pagination). */}
      <section className="rounded-md border border-border">
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : rules.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">
            No policy rules authored. An empty table falls back to today&rsquo;s per-resource approval
            behavior.
          </p>
        ) : (
          <ul className="divide-y divide-border">
            {rules.map((r) => (
              <li key={r.id} className="space-y-1 p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="min-w-0 font-medium">{r.label}</span>
                  <div className="flex items-center gap-3">
                    <span className="text-muted-foreground">{r.outcome}</span>
                    <span className="text-muted-foreground">{r.enabled ? "enabled" : "disabled"}</span>
                    <Button
                      variant="default"
                      size="xs"
                      onClick={() => void onToggleEnabled(r)}
                      disabled={busyId === r.id}
                    >
                      {r.enabled ? "Disable" : "Enable"}
                    </Button>
                    <Button variant="destructive" size="xs" onClick={() => void onDelete(r.id)} disabled={busyId === r.id}>
                      Delete
                    </Button>
                  </div>
                </div>
                <p className="font-mono text-xs text-muted-foreground">
                  {r.subject} · {r.edge} · {r.action} · {r.target}
                </p>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
