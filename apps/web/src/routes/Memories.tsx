// Memories route — the operator's Persistent Scoped Memory surface (FE-22/FE-23; GOV-9;
// ADR-0064). Memory is the mutable, forgettable projection beside the immutable audit
// chain (ADR-0064 §3): a terminal Run accrues a content-free evidence pointer with
// nobody writing it, and the operator may additionally author free-text notes (sealed at
// rest, operator-only on read). Redact/expire/pin govern ONLY this projection — they
// never rewrite the audit chain, casts, or prior exports (the R2 forgetting boundary,
// stated here per GOV-9). This UI drives only the audited /v1/memories* control plane
// (human-agent + CSRF); it feeds no execution (memory never reaches an agent, SEC-31).

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useCapability } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

export type MemoryView = components["schemas"]["MemoryView"];
type MemoryCreate = components["schemas"]["MemoryCreate"];
type ScopeType = MemoryView["scopeType"];

const SCOPE_TYPES: ScopeType[] = ["operator", "workstream", "run"];

/** FE-23: "Expired" when expiresAt had already passed at the moment of redaction (the
 * sweep tombstoned it), else "Redacted" (an operator act). Never blank/empty content. */
function tombstoneLabel(m: MemoryView): "Expired" | "Redacted" {
  if (m.expiresAt && m.redactedAt && new Date(m.expiresAt).getTime() <= new Date(m.redactedAt).getTime()) {
    return "Expired";
  }
  return "Redacted";
}

// SHARE_CONFIRM_COPY is the GOV-10 consent copy (ADR-0065 §3, T-091), shown verbatim
// on EVERY share/unshare click (either direction pops the same explanation, so the
// operator is reminded of the unshare limit even while sharing, and vice versa) —
// naming the prompt-injection/exfiltration risk plainly, no dark patterns.
export const SHARE_CONFIRM_COPY =
  "Sharing places this note's content inside the composer agent's context — a prompt-injection and exfiltration risk you are explicitly accepting. Unsharing stops future reads but cannot recall past ones.";

/**
 * MemoryRow — the shared FE-23 row rendering, reused read-only by the Workstreams
 * charter-detail promotion (FE-22, no onRedact/onTogglePin/onToggleShare). Verb
 * buttons render only when their handler is supplied.
 *
 * R2 (GOV-10, MEM-6): the share control renders ONLY on authored rows (hasContent) —
 * an accrued content-free row is always agent-readable regardless of its agentVisible
 * flag (memory/store.go's ReadForAgent filter), so a share toggle there would be a
 * lying control; it gets a static hint instead. Tombstoned rows get neither (they are
 * gone from the agent plane).
 */
export function MemoryRow({
  m,
  onRedact,
  onTogglePin,
  onToggleShare,
  busy,
}: {
  m: MemoryView;
  onRedact?: (id: string) => void;
  onTogglePin?: (id: string, pinned: boolean) => void;
  onToggleShare?: (id: string, shared: boolean) => void;
  busy?: boolean;
}) {
  const tombstoned = Boolean(m.redactedAt);
  return (
    <li className={`space-y-1 p-3 text-sm ${tombstoned ? "opacity-60" : ""}`}>
      <div className="flex items-center justify-between gap-2">
        <span className="min-w-0 font-medium">{m.key || "—"}</span>
        <div className="flex items-center gap-2">
          {m.pinned && <span className="rounded-md border border-border px-2 py-0.5 text-xs font-medium">Pinned</span>}
          {!tombstoned && m.hasContent && m.agentVisible && (
            <span className="rounded-md border border-border px-2 py-0.5 text-xs font-medium">visible to agent</span>
          )}
          {tombstoned ? (
            <span className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground">
              {tombstoneLabel(m)} <span className="font-mono">{m.redactedAt}</span>
            </span>
          ) : m.expiresAt ? (
            <span className="text-xs text-muted-foreground">
              expires <span className="font-mono">{m.expiresAt}</span>
            </span>
          ) : null}
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        ref <span className="font-mono">{m.ref}</span> · {m.sourceTrace} · confidence {m.confidence}
      </p>
      {m.hasContent && !tombstoned && m.content !== undefined && (
        <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground">{m.content}</pre>
      )}
      {/* R2: accrued (content-free) rows are always readable by the composer — a
          share toggle there would misrepresent the actual (always-on) read gate. */}
      {!tombstoned && !m.hasContent && (
        <p className="text-xs text-muted-foreground">always visible to the composer — content-free evidence pointer</p>
      )}
      {(onRedact || onTogglePin || (onToggleShare && m.hasContent && !tombstoned)) && (
        <div className="flex gap-2">
          {onTogglePin && (
            <button
              type="button"
              onClick={() => onTogglePin(m.id, m.pinned)}
              disabled={busy}
              className="rounded-md border border-border px-2 py-1 text-xs font-medium disabled:opacity-60"
            >
              {busy ? "Working…" : m.pinned ? "Unpin" : "Pin"}
            </button>
          )}
          {onToggleShare && m.hasContent && !tombstoned && (
            <button
              type="button"
              onClick={() => onToggleShare(m.id, m.agentVisible)}
              disabled={busy}
              className="rounded-md border border-border px-2 py-1 text-xs font-medium disabled:opacity-60"
            >
              {busy ? "Working…" : m.agentVisible ? "Unshare" : "Share"}
            </button>
          )}
          {onRedact && !tombstoned && (
            <button
              type="button"
              onClick={() => onRedact(m.id)}
              disabled={busy}
              className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground disabled:opacity-60"
            >
              {busy ? "Working…" : "Redact"}
            </button>
          )}
        </div>
      )}
    </li>
  );
}

/** Renders the currently listed scope's rows to a deterministic markdown manifest (D2,
 * ratified — client-side, zero backend change). Stable field order; content only when
 * present and un-redacted (ADR-0064 §7: export renders the manifest on demand). */
function buildManifest(scopeType: string, scopeId: string, items: MemoryView[]): string {
  const lines: string[] = [`# Memories — ${scopeType}:${scopeId}`, ""];
  for (const m of items) {
    lines.push(`## ${m.id}`);
    lines.push(`- scope: ${m.scopeType}:${m.scopeId}`);
    lines.push(`- key: ${m.key ?? "-"}`);
    lines.push(`- ref: ${m.ref}`);
    lines.push(`- sourceTrace: ${m.sourceTrace}`);
    lines.push(`- confidence: ${m.confidence}`);
    lines.push(`- pinned: ${m.pinned}`);
    lines.push(`- expiresAt: ${m.expiresAt ?? "-"}`);
    lines.push(`- redactedAt: ${m.redactedAt ?? "-"}`);
    lines.push(`- createdAt: ${m.createdAt}`);
    if (m.hasContent && !m.redactedAt && m.content !== undefined) {
      lines.push("", "content:", "```", m.content, "```");
    }
    lines.push("");
  }
  return lines.join("\n");
}

function downloadManifest(scopeType: string, scopeId: string, items: MemoryView[]) {
  const md = buildManifest(scopeType, scopeId, items);
  const blob = new Blob([md], { type: "text/markdown" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `memories-${scopeType}-${scopeId}.md`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

export function Memories() {
  const { capability } = useCapability();
  const [scopeType, setScopeType] = useState<ScopeType>("operator");
  const [scopeId, setScopeId] = useState("");
  const [memories, setMemories] = useState<MemoryView[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [verbBusy, setVerbBusy] = useState<string | null>(null);

  const [key, setKey] = useState("");
  const [content, setContent] = useState("");
  const [confidence, setConfidence] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [creating, setCreating] = useState(false);

  const loadMemories = useCallback(async () => {
    const sid = scopeId.trim();
    if (!sid) {
      setMemories([]);
      return;
    }
    setLoading(true);
    const { data, error: apiErr } = await apiClient.GET("/v1/memories", {
      params: { query: { scopeType, scopeId: sid } },
    });
    if (apiErr || !data) {
      setError("Could not load memories.");
      setMemories([]);
    } else {
      setMemories(data);
      setError(null);
    }
    setLoading(false);
  }, [scopeType, scopeId]);

  // Default to the operator's own scope once the capability resolves (FE-22: "match
  // what identity the UI actually has"); a manual scope pick is never overwritten.
  useEffect(() => {
    if (capability?.subject && scopeType === "operator" && scopeId === "") {
      setScopeId(capability.subject);
    }
  }, [capability, scopeType, scopeId]);

  // One-shot fetch once a scope is resolvable — the house fetch-on-mount-plus-Refresh
  // pattern (these routes do NOT auto-poll; typing into scopeId does not itself refetch).
  const fetchedOnce = useRef(false);
  useEffect(() => {
    if (!fetchedOnce.current && scopeId.trim()) {
      fetchedOnce.current = true;
      void loadMemories();
    }
  }, [scopeId, loadMemories]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    const sid = scopeId.trim();
    if (!sid || !content.trim()) return;
    setCreating(true);
    setError(null);
    try {
      const body: MemoryCreate = { scopeType, scopeId: sid, content: content.trim() };
      if (key.trim()) body.key = key.trim();
      if (confidence.trim() !== "") body.confidence = Number(confidence);
      if (expiresAt) body.expiresAt = new Date(expiresAt).toISOString();
      const { data, error: apiErr } = await apiClient.POST("/v1/memories", { body });
      if (apiErr || !data) {
        setError(apiErr?.message ? `Could not create the memory: ${apiErr.message}` : "Could not create the memory.");
        return;
      }
      setKey("");
      setContent("");
      setConfidence("");
      setExpiresAt("");
      setMemories((m) => [...m, data]);
    } finally {
      setCreating(false);
    }
  }

  async function onRedact(id: string) {
    const confirmed = window.confirm(
      "Redact this memory? This erases its content from this memory store only — it does not and cannot rewrite the audit chain, session recordings, or previously exported copies.",
    );
    if (!confirmed) return;
    setVerbBusy(id);
    setError(null);
    try {
      const { data, error: apiErr } = await apiClient.POST("/v1/memories/{id}/redact", { params: { path: { id } } });
      if (apiErr) setError("Could not redact the memory. Refresh and try again.");
      else if (data) setMemories((m) => m.map((x) => (x.id === id ? data : x)));
    } finally {
      setVerbBusy(null);
    }
  }

  async function onTogglePin(id: string, pinned: boolean) {
    setVerbBusy(id);
    setError(null);
    try {
      const path = pinned ? "/v1/memories/{id}/unpin" : "/v1/memories/{id}/pin";
      const { data, error: apiErr } = await apiClient.POST(path, { params: { path: { id } } });
      if (apiErr) setError(`Could not ${pinned ? "unpin" : "pin"} the memory. Refresh and try again.`);
      else if (data) setMemories((m) => m.map((x) => (x.id === id ? data : x)));
    } finally {
      setVerbBusy(null);
    }
  }

  // R2/GOV-10 (MEM-6, T-091): the SAME confirm copy fires for either direction — it
  // states the sharing risk AND the unshare limit together, so the operator has both
  // facts before committing either way.
  async function onToggleShare(id: string, shared: boolean) {
    if (!window.confirm(SHARE_CONFIRM_COPY)) return;
    setVerbBusy(id);
    setError(null);
    try {
      const path = shared ? "/v1/memories/{id}/unshare" : "/v1/memories/{id}/share";
      const { data, error: apiErr } = await apiClient.POST(path, { params: { path: { id } } });
      if (apiErr) setError(`Could not ${shared ? "unshare" : "share"} the memory. Refresh and try again.`);
      else if (data) setMemories((m) => m.map((x) => (x.id === id ? data : x)));
    } finally {
      setVerbBusy(null);
    }
  }

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Memory</h1>
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => void loadMemories()} className="text-sm text-muted-foreground hover:underline">
            Refresh
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {/* Scope picker — drives both the list below and the authoring form's target. */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Scope</h2>
        <div className="flex gap-2">
          <select
            aria-label="scope type"
            value={scopeType}
            onChange={(e) => setScopeType(e.target.value as ScopeType)}
            className="w-40 rounded-md border border-input bg-background px-3 py-2 text-sm"
          >
            {SCOPE_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
          <input
            aria-label="scope id"
            value={scopeId}
            onChange={(e) => setScopeId(e.target.value)}
            placeholder="scope id"
            className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
          />
        </div>
      </section>

      {/* Authoring form (GOV-9 consent copy beside the submit). */}
      <section className="space-y-3 rounded-md border border-border p-4">
        <h2 className="text-sm font-medium">Author a memory</h2>
        <form aria-label="author memory" className="space-y-3" onSubmit={(e) => void onCreate(e)}>
          <input
            aria-label="key"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="key (optional)"
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
          <textarea
            aria-label="content"
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="the note to remember"
            rows={3}
            className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm font-mono"
          />
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <label className="text-xs text-muted-foreground">
              Confidence (0–1, optional)
              <input
                type="number"
                aria-label="confidence"
                value={confidence}
                min={0}
                max={1}
                step={0.01}
                onChange={(e) => setConfidence(e.target.value)}
                className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
              />
            </label>
            <label className="text-xs text-muted-foreground">
              Expires at (optional)
              <input
                type="datetime-local"
                aria-label="expires at"
                value={expiresAt}
                onChange={(e) => setExpiresAt(e.target.value)}
                className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
              />
            </label>
          </div>

          {/* GOV-9: the consent copy — what authoring stores, and what redaction can and
              cannot reach (R2), stated at the act. No dark-pattern "delete" language. */}
          <p className="text-xs text-muted-foreground">
            Authored content is stored <strong>sealed and operator-only</strong>. <strong>Redact</strong> erases
            content from this memory store only — it does not and cannot rewrite the audit chain, session
            recordings, or previously exported copies. Accrued run pointers (written automatically when governed
            work completes) are content-free.
          </p>

          <button
            type="submit"
            disabled={creating || !scopeId.trim() || !content.trim()}
            className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
          >
            {creating ? "Authoring…" : "Author memory"}
          </button>
        </form>
      </section>

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      {/* The scoped memories: accrued pointers + authored notes. */}
      <section className="rounded-md border border-border">
        <div className="flex items-center justify-between border-b border-border p-3">
          <h2 className="text-sm font-medium">
            Memories <span className="font-mono text-muted-foreground">({scopeType}:{scopeId || "—"})</span>
          </h2>
          <button
            type="button"
            onClick={() => downloadManifest(scopeType, scopeId, memories)}
            disabled={memories.length === 0}
            className="rounded-md border border-border px-2 py-1 text-xs font-medium disabled:opacity-60"
          >
            Export
          </button>
        </div>
        {loading ? (
          <p className="p-4 text-sm text-muted-foreground">Loading…</p>
        ) : memories.length === 0 ? (
          <p className="p-4 text-sm text-muted-foreground">No memories for this scope.</p>
        ) : (
          <ul className="divide-y divide-border">
            {memories.map((m) => (
              <MemoryRow
                key={m.id}
                m={m}
                onRedact={onRedact}
                onTogglePin={onTogglePin}
                onToggleShare={onToggleShare}
                busy={verbBusy === m.id}
              />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
