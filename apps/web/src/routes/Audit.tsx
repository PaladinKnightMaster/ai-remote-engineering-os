// Audit route — the operator audit-timeline view (T-024-b; extended by T-107 for
// ADR-0067's read semantics). Reads the chained, append-only evidence log through
// services/api's read-proxy (GET /v1/audit/events → audit-service, T-024-a/T-106).
//
// Read-only by construction: the chain is append-only at the authority (ADR-0045)
// and the proxy exposes only GET. The surface now opens newest-first on the
// governed-actions@1 preset (AUD-24, FE-31) and composes server-side actor/outcome/
// edge filters with the reverse-cursor keyset (AUD-25, FE-33); "Load more" still
// appends, but only within the CURRENT direction+filter set — a direction, preset,
// or filter change is a NEW request that RESETS the list (see `load` below). The
// actor/outcome text filters commit on blur or Enter, not per keystroke — an
// `onChange` fires per character, and this is the audit surface whose whole
// point this release is being fast at production volume. A `requestSeq` guard
// discards any response that isn't from the LATEST request, because even a
// committed-only cadence can still race (a slow response for an earlier filter
// resolving after a fast one for a later filter) — without it the EVD-8 banner
// could describe a filter that did not produce the rows on screen.
//
// No client-side filtering. This rule dates to v0.1.0: the harm it named was
// precise — fetch 50 events, filter *those 50* in the browser, see 3 rows, and
// believe those 3 are all the matches in the whole chain, when they are an
// arbitrary window. Fetching a bigger page cannot close that harm; it is intrinsic
// to filtering AFTER the page is drawn. The rule stands unamended (AUD-26). What
// changes is its SCOPE: T-107 answers the underlying need with filtering applied
// SERVER-SIDE, before the LIMIT, over the whole chain (ADR-0067 §D4) — a complete
// answer to a well-posed question, not a partial one dressed up as complete. Any
// view that is not the plain, contiguous seq-ordered window is disclosed as a
// query result per the EVD-8 invariant (ADR-0067 §D1): the banner below is driven
// ONLY by what the RESPONSE says was applied (page.preset/actor/outcome/edge/
// chainHead) — never by local request state, so it can only ever report what the
// server actually did — and "Show all events" always returns the canonical,
// unfiltered, verifiable timeline in one interaction.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type AuditEvent = components["schemas"]["AuditEvent"];
type AuditPageMeta = components["schemas"]["AuditPageMeta"];
type Direction = components["parameters"]["DirectionParam"];
type Edge = components["parameters"]["EdgeParam"];

const PAGE_LIMIT = 50;
const PRESET = "governed-actions@1" as const;
const EDGES: Edge[] = [
  "human-agent",
  "agent-agent",
  "agent-tool",
  "agent-workspace",
  "agent-repo",
  "agent-memory",
  "agent-remote-session",
  "service-service",
];

export function Audit() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [pageMeta, setPageMeta] = useState<AuditPageMeta>({});
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // AUD-24/FE-31: opens newest-first, on the preset.
  const [direction, setDirection] = useState<Direction>("desc");
  const [presetOn, setPresetOn] = useState(true);
  // actor/outcome are COMMITTED filters — the values `load` closes over and
  // requests are issued against. They are set on blur/Enter only (see
  // actorDraft/outcomeDraft below), never on every keystroke: an `onChange` on a
  // text input fires per keystroke, and wiring that directly to a request would
  // fire one filtered-chain-read + one MAX(seq) per character typed. The select
  // and checkbox controls have no such storm (one discrete event each), so they
  // stay wired directly.
  const [actorFilter, setActorFilter] = useState("");
  const [outcomeFilter, setOutcomeFilter] = useState("");
  const [edgeFilter, setEdgeFilter] = useState<Edge | "">("");
  const [actorDraft, setActorDraft] = useState("");
  const [outcomeDraft, setOutcomeDraft] = useState("");

  // requestSeq guards against a stale response landing after a newer one — even
  // with the blur/Enter commit above, two committed requests can still race (a
  // slow response for filter #1 resolving after a fast response for filter #2).
  // Without this, `pageMeta` could be set from request #1 while the rows the
  // operator is looking at came from #2, and the EVD-8 disclosure banner would
  // then describe a filter that did not produce what's on screen — the banner
  // asserting something the server did NOT do for the view currently rendered.
  // Only the LATEST request's result is ever applied to events/pageMeta/
  // nextCursor/error; they move together, as one.
  const requestSeq = useRef(0);

  // load fetches one page under the CURRENT direction/filters/preset. reset=true
  // starts a fresh read (any control change, or Refresh); cursor is only ever
  // passed for "Load more" within that SAME direction+filter set — appending a
  // page fetched under a different request onto the existing list would
  // misrepresent the chain (FE-33).
  const load = useCallback(
    async (cursor?: string, reset = false) => {
      const seq = ++requestSeq.current;
      setLoading(true);
      const { data, error: apiErr } = await apiClient.GET("/v1/audit/events", {
        params: {
          query: {
            limit: PAGE_LIMIT,
            cursor,
            direction,
            preset: presetOn ? PRESET : undefined,
            actor: actorFilter.trim() || undefined,
            outcome: outcomeFilter.trim() || undefined,
            edge: edgeFilter || undefined,
          },
        },
      });
      if (seq !== requestSeq.current) return; // EVD-8: a superseded request must never overwrite the current view
      if (apiErr || !data) {
        setError("Could not load the audit timeline.");
      } else {
        setError(null);
        setEvents((prev) => (reset ? data.items ?? [] : [...prev, ...(data.items ?? [])]));
        setNextCursor(data.page?.nextCursor || undefined);
        setPageMeta(data.page ?? {});
      }
      setLoading(false);
    },
    [direction, presetOn, actorFilter, outcomeFilter, edgeFilter],
  );

  // AUD-26/FE-33: a change to direction, preset, or any filter is a NEW request —
  // `load` is recreated (it closes over those values), so this effect fires once
  // per change and always resets. This is correctness, not polish: appending a
  // `desc` page onto an `asc`-accumulated list, or a filtered page onto an
  // unfiltered one, would render a view that misrepresents the chain.
  useEffect(() => {
    void load(undefined, true);
  }, [load]);

  // FE-32/EVD-8: filtered iff the RESPONSE says something was applied. Never
  // derived from direction/presetOn/actorFilter/etc — a banner driven by request
  // state could assert something the server ignored or altered.
  const filtered = Boolean(pageMeta.preset || pageMeta.actor || pageMeta.outcome || pageMeta.edge);

  // Commits both text-filter drafts to the values `load` requests against — on
  // blur of either field, or Enter (the wrapping form's onSubmit). A no-op
  // commit (draft already equals the committed value) does not re-trigger a
  // fetch: React bails a setState of an equal primitive, so `load` isn't
  // recreated and the effect below doesn't fire.
  function commitTextFilters() {
    setActorFilter(actorDraft.trim());
    setOutcomeFilter(outcomeDraft.trim());
  }

  function showAll() {
    setPresetOn(false);
    setActorFilter("");
    setOutcomeFilter("");
    setEdgeFilter("");
    setActorDraft("");
    setOutcomeDraft("");
  }

  return (
    <div className="mx-auto max-w-5xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Audit timeline</h1>
        <div className="flex items-center gap-4">
          <button
            onClick={() => void load(undefined, true)}
            disabled={loading}
            className="rounded-md border border-border px-3 py-1.5 text-sm disabled:opacity-60"
          >
            {loading ? "Loading…" : "Refresh"}
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      <section aria-label="filters" className="flex flex-wrap items-end gap-3 rounded-md border border-border p-3">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Direction
          <select
            aria-label="direction"
            value={direction}
            onChange={(e) => setDirection(e.target.value as Direction)}
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm text-foreground"
          >
            <option value="desc">Newest first</option>
            <option value="asc">Oldest first</option>
          </select>
        </label>
        {/* Text filters commit on blur or Enter, not per keystroke (FE-33): an
            onChange on a text input fires per keystroke, and wiring that
            directly to `load` would fire a filtered chain read + a MAX(seq)
            per character typed. The <button type="submit"> is what makes Enter
            reliable with TWO text fields — a browser's implicit-submission
            fallback (Enter submits with no explicit button) only kicks in for a
            form with exactly one text field, which this form is not. */}
        <form
          aria-label="text filters"
          className="contents"
          onSubmit={(e) => {
            e.preventDefault();
            commitTextFilters();
          }}
        >
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Actor
            <input
              aria-label="actor"
              value={actorDraft}
              onChange={(e) => setActorDraft(e.target.value)}
              onBlur={commitTextFilters}
              placeholder="e.g. operator:admin"
              className="rounded-md border border-input bg-background px-3 py-1.5 text-sm text-foreground"
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-muted-foreground">
            Outcome
            <input
              aria-label="outcome"
              value={outcomeDraft}
              onChange={(e) => setOutcomeDraft(e.target.value)}
              onBlur={commitTextFilters}
              placeholder="e.g. session.open"
              className="rounded-md border border-input bg-background px-3 py-1.5 text-sm text-foreground"
            />
          </label>
          <button
            type="submit"
            className="rounded-md border border-border px-3 py-1.5 text-sm self-end"
          >
            Apply
          </button>
        </form>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Edge
          <select
            aria-label="edge"
            value={edgeFilter}
            onChange={(e) => setEdgeFilter(e.target.value as Edge | "")}
            className="rounded-md border border-input bg-background px-3 py-1.5 text-sm text-foreground"
          >
            <option value="">All edges</option>
            {EDGES.map((edge) => (
              <option key={edge} value={edge}>
                {edge}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-2 pb-1.5 text-sm">
          <input
            type="checkbox"
            aria-label="preset"
            checked={presetOn}
            onChange={(e) => setPresetOn(e.target.checked)}
          />
          Governed actions only
        </label>
      </section>

      {filtered && (
        <div role="region" aria-label="filtered view" className="space-y-2 rounded-md border border-border bg-muted/40 px-3 py-2 text-sm">
          <p>
            {pageMeta.preset && (
              <>
                Showing the <strong>{pageMeta.preset}</strong> preset (governed actions only).{" "}
              </>
            )}
            {pageMeta.actor && (
              <>
                Filtered to actor <strong>{pageMeta.actor}</strong>.{" "}
              </>
            )}
            {pageMeta.outcome && (
              <>
                Filtered to outcome <strong>{pageMeta.outcome}</strong>.{" "}
              </>
            )}
            {pageMeta.edge && (
              <>
                Filtered to edge <strong>{pageMeta.edge}</strong>.{" "}
              </>
            )}
            The full chain has {pageMeta.chainHead ?? "an unknown number of"} positions.
          </p>
          <button onClick={showAll} className="rounded-md border border-border px-3 py-1.5 text-sm">
            Show all events
          </button>
        </div>
      )}

      {error && (
        <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}

      <section className="overflow-hidden rounded-md border border-border">
        {events.length === 0 && !loading ? (
          <p className="p-4 text-sm text-muted-foreground">
            {/* FE-32/EVD-8: derived from `filtered` (the RESPONSE), not local
                request state — this message describes what the server did, the
                same way the banner above it does. "No audit events yet." is the
                v0.1.0 copy and is only honest when nothing was filtered; once a
                filter narrows the chain, an empty result means no matches, not
                an empty chain, and this branch must say so instead. */}
            {filtered ? "No events match this filter." : "No audit events yet."}
          </p>
        ) : (
          <table className="w-full table-fixed text-left text-sm">
            <thead className="border-b border-border bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr>
                <th className="w-44 px-3 py-2 font-medium">Time</th>
                <th className="w-40 px-3 py-2 font-medium">Actor</th>
                <th className="w-28 px-3 py-2 font-medium">Edge</th>
                <th className="px-3 py-2 font-medium">Target</th>
                <th className="w-44 px-3 py-2 font-medium">Outcome</th>
                <th className="px-3 py-2 font-medium">Evidence</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {events.map((e) => (
                <tr key={e.id}>
                  <td className="whitespace-nowrap px-3 py-2 font-mono text-xs text-muted-foreground">
                    {e.timestamp}
                  </td>
                  <td className="px-3 py-2 font-mono">{e.actor}</td>
                  <td className="px-3 py-2 text-muted-foreground">{e.edge}</td>
                  <td className="max-w-xs truncate px-3 py-2 font-mono" title={e.target}>
                    {e.target}
                  </td>
                  <td className="px-3 py-2">{e.outcome ?? "—"}</td>
                  <td
                    className="max-w-[16rem] truncate px-3 py-2 font-mono text-xs text-muted-foreground"
                    title={e.evidencePacketId}
                  >
                    {e.evidencePacketId ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {nextCursor && (
        <div className="flex justify-center">
          <button
            onClick={() => void load(nextCursor)}
            disabled={loading}
            className="rounded-md border border-border px-3 py-2 text-sm disabled:opacity-60"
          >
            {loading ? "Loading…" : "Load more"}
          </button>
        </div>
      )}
    </div>
  );
}
