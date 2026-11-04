// OnboardingChecklist — the E-04 first-run checklist (T-118a; ADR-0068 §D8,
// release criteria ONB-1/ONB-2). First new file under apps/web/src/components/
// (previously only routes/ existed) — this is the first apps/web UI element
// that is not itself a route; packages/ui/src/components/ already establishes
// what that directory name means, so a future non-route apps/web element has
// a home instead of getting bolted onto a route file.
//
// Desk-only by construction: Dashboard.tsx renders this ONLY inside a
// <CapabilityGate deniedScopes={["approvals@1"]}> (the same gate T-115b built
// for Runs/Grants/Workstreams/Memories) — a paired phone session cannot create
// resources, launch Runs, or take almost anything else this panel points at,
// so it must never see the panel (ADR-0068 §D5).
//
// T-176d (ADR-0079 D4, FRX-5): the default way to get a demo target is ONE button with NO fields — "Create loopback demo
// target" calls POST /v1/demo-target, which reads the key and account that setup.sh prepared and binds them server-side. The
// older flow (run a script on the server, paste its username and host key here, run a bind command) survives ONLY as the
// "script path" fallback behind a plain button, because operators who declined the demo at setup.sh have no other way to
// create the resource for setup-loopback-demo.sh (FRX-6 keeps that script supported). It is not on the default path.
//
// ONB-1: completion is DERIVED from live state on every mount — never cached,
// never a stored flag/column. Two conditions, not the three ADR-0068 §D8
// names:
//
//   1. Does a resource exist?             GET /v1/resources
//   2. Has a governed action completed?   GET /v1/audit/events?outcome=tool.result
//
// The third condition ADR-0068 §D8 names — "a connector credential" — is
// deliberately NOT a separate check, and no credential-presence endpoint is
// added for it. The API surface closed at T-115b (ADR-0068 §D11 — the last
// growth before the E-06 freeze), and one isn't needed anyway: a completed
// governed action SUBSUMES the credential. You cannot complete a
// `remote.exec` invocation — policy → approval → connector → broker — without
// a credential the connector actually decrypted and used successfully. A
// credential ROW existing would prove LESS than that (only that a secret is
// stored, not that it works). So condition 2 is strictly stronger than a
// credential-presence check would be — do not "restore" a third read here.
//
// `outcome=tool.result` is audit-service's outcome filter, which is EXACT
// match only (no wildcard/prefix — services/audit-service/internal/store/
// store.go binds it as `outcome = $n`), so it can't match "any *.completed"
// outcome across the product. "tool.result" is the one outcome every
// successfully completed governed invocation carries, fired ad-hoc or as a
// Run step (services/mcp-gateway/internal/store/store.go Finish/
// finalizeFrom) — the narrowest true signal for "a governed action has
// completed" reachable with the existing filter, no new preset or param
// (ADR-0068 §D11 forbids both).
//
// ONB-2: dismissal is a per-browser localStorage flag ONLY. The server
// records nothing about onboarding — no route, no column, no audit event.
// This is a deliberate asymmetry: the checklist's COMPLETION (above) is
// derived and can never be dismissed away; the PANEL'S VISIBILITY is pure
// presentation state. A dismissed panel on an un-seeded stack is still an
// un-seeded stack — dismissal hides the reminder, not the fact.

import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Button } from "@aireos/ui";
import { apiClient } from "../api";

// Namespaced so a future apps/web local-storage key doesn't collide with it;
// no existing convention to match (Login.tsx explicitly avoids localStorage
// for auth state — this is unrelated presentation state, not a credential).
const DISMISSED_KEY = "aireos.onboarding.dismissed";

function readDismissed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(DISMISSED_KEY) === "1";
  } catch {
    // Storage can throw (private-browsing quota, disabled storage). Treat as
    // "not dismissed" — the worst case is the panel keeps showing, not that
    // it hides itself unexpectedly.
    return false;
  }
}

interface DerivedState {
  resourceExists: boolean;
  actionCompleted: boolean;
  /** The loopback demo resource's own id, found by name among the same
   * /v1/resources read `resourceExists` already derives from — null when no
   * such resource exists (T-118c: the Remove action's target, never a general
   * resource picker). */
  demoResourceId: string | null;
  loading: boolean;
  /** Re-runs both reads without a remount — used after the T-118b create
   * action so item 1 flips the instant the operator's POST succeeds, instead
   * of waiting for the next mount (ONB-1 stays mount-derived; this is just an
   * on-demand extra derivation, not a cache). T-118c's remove action reuses
   * it so item 1 flips back the same way. */
  refresh: () => void;
}

// Recomputed fresh on every mount (and on-demand via refresh()) — see ONB-1
// note above. Both reads fire together; a fetch error on either leaves that
// condition unmet (fails safe toward "still onboarding") rather than
// surfacing a route-level error state, matching how Grants/Runs/Sessions
// already treat their own /v1/resources picker fetch (ignore the error,
// default to empty).
function useDerivedOnboardingState(): DerivedState {
  const [resourceExists, setResourceExists] = useState(false);
  const [actionCompleted, setActionCompleted] = useState(false);
  const [demoResourceId, setDemoResourceId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [generation, setGeneration] = useState(0);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      setLoading(true);
      const [resources, actions] = await Promise.all([
        apiClient.GET("/v1/resources", {}),
        apiClient.GET("/v1/audit/events", {
          params: { query: { limit: 1, outcome: "tool.result" } },
        }),
      ]);
      if (cancelled) return;
      const items = resources.data?.items ?? [];
      setResourceExists(items.length > 0);
      setDemoResourceId(items.find((item) => item.name === DEMO_RESOURCE_NAME)?.id ?? null);
      setActionCompleted((actions.data?.items?.length ?? 0) > 0);
      setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [generation]);

  const refresh = useCallback(() => setGeneration((g) => g + 1), []);

  return { resourceExists, actionCompleted, demoResourceId, loading, refresh };
}

// Is the demo resource still WITHOUT a stored key? The one-click button binds server-side, so the bind command only matters
// after the script path's manual form. GET /v1/resources/{id}/credential answers 404 when there is nothing to show (no key —
// or an imported one, which also has no public half; the API does not tell them apart), 200 once a generated key is bound.
function useDemoUnbound(resourceId: string | null): boolean {
  const [unbound, setUnbound] = useState(false);
  useEffect(() => {
    if (!resourceId) {
      setUnbound(false);
      return;
    }
    let cancelled = false;
    void (async () => {
      const { response } = await apiClient.GET("/v1/resources/{id}/credential", { params: { path: { id: resourceId } } });
      if (!cancelled) setUnbound(response?.status === 404);
    })();
    return () => {
      cancelled = true;
    };
  }, [resourceId]);
  return unbound;
}

function ChecklistItem({ done, children }: { done: boolean; children: ReactNode }) {
  return (
    <li className="flex items-center gap-2 text-sm">
      <span aria-hidden="true" className={done ? "text-foreground" : "text-muted-foreground"}>
        {done ? "✓" : "○"}
      </span>
      <span className={done ? "text-foreground" : "text-muted-foreground"}>{children}</span>
      <span className="sr-only">{done ? "done" : "not done"}</span>
    </li>
  );
}

// The loopback demo target's fixed coordinates (ADR-0068 §D8: "the host SSHing
// to itself"). Only the account is the operator's own choice — whatever they
// passed to `infra/offline/setup-loopback-demo.sh generate --account <name>`.
const LOOPBACK_HOST = "127.0.0.1";
const LOOPBACK_PORT = "22";

// The single source of truth for the demo resource's `name` field — used both
// to CREATE it (ManualDemoTargetForm's POST body) and to FIND it again
// (useDerivedOnboardingState's lookup, T-118c's Remove action target). Before
// T-118c review caught it, these were two independent string literals: diverge
// them and the create still succeeds but the Remove action never appears,
// leaving an undeletable demo resource with no affordance — a silent break on
// the exact path that demonstrates ONB-4, which neither existing test would
// catch (each asserts against its own literal). infra/offline/
// setup-loopback-demo.sh's own "loopback-demo" strings (its state directory,
// its SSH-key marker) are a DIFFERENT value for a different purpose — the
// script never sets or reads this API field — so there is nothing on that
// side for this constant to stay in sync with.
const DEMO_RESOURCE_NAME = "loopback-demo";

// The script-path commands (the fallback only; see the T-176d note at the top). Creating the unix account first is a
// quickstart step, not repeated here.
const GENERATE_COMMAND = "sudo ./setup-loopback-demo.sh generate --account aireos-demo";
const bindCommand = (resourceId: string) => `sudo ./setup-loopback-demo.sh bind --resource-id ${resourceId}`;

// What the operator is told when POST /v1/demo-target answers 409 (the demo was not prepared at install time). The server
// sends its own message too; this one is the cockpit's plain sentence naming the two ways forward.
const DEMO_NOT_PREPARED =
  "The demo target was not prepared when this server was installed. Re-run sudo ./setup.sh and answer yes to the demo, or use the script path (setup-loopback-demo.sh) below.";

// CreateDemoTargetButton — the no-paste demo target (T-176d; ADR-0079 D4, FRX-5). Statuses are POST /v1/demo-target's own:
// 201 created, 409 not prepared, 500 (the message names a leftover resource), 502/503 (connector unavailable or unconfigured).
// Non-409 failures show the SERVER's message verbatim after a fixed prefix; nothing is invented on its behalf.
function CreateDemoTargetButton({ onCreated }: { onCreated: () => void }) {
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onCreate() {
    setCreating(true);
    setError(null);
    try {
      const { error: apiErr, response } = await apiClient.POST("/v1/demo-target", {});
      if (apiErr) {
        if (response?.status === 409) setError(DEMO_NOT_PREPARED);
        else setError(apiErr.message ? `Could not create the demo target: ${apiErr.message}` : `Could not create the demo target (status ${response?.status ?? "unknown"}).`);
        return;
      }
      onCreated();
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="mt-2 space-y-1">
      <Button variant="primary" size="xs" type="button" onClick={() => void onCreate()} disabled={creating}>
        {creating ? "Creating…" : "Create loopback demo target"}
      </Button>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

// ManualDemoTargetForm (was CreateLoopbackDemoTarget; now the script-path fallback) — the first UI call site for POST /v1/resources
// (T-118b; ADR-0068 §D8/§D11, ONB-3/ONB-4). Deliberately a narrow demo-target
// creator, not a general resource-management form: it collects exactly the
// two values the setup script prints that the cockpit cannot already know
// (the account the operator chose, and the host's own SSH host public key —
// see the note below) and hardcodes everything else. A general "add any
// resource" form is a real product gap (v1.x candidate), out of scope here.
//
// ssh_host_key note: the release criteria's field list for this action names
// "host, port, username", but services/session-broker/internal/store/store.go
// LoadTargetCoords treats the resource's ssh_host_key label as MANDATORY
// (ErrTargetIncomplete without it) — host-key pinning via ssh.FixedHostKey
// applies to both the interactive and the non-interactive (remote.exec) SSH
// path. Omitting this field would create a resource that can never connect,
// defeating ONB-3, so it is collected as a fourth field here alongside
// host/port/username; setup-loopback-demo.sh's `generate` prints it for
// exactly this reason. See the ticket report for this called out explicitly.
function ManualDemoTargetForm({ onCreated, onCancel }: { onCreated: () => void; onCancel: () => void }) {
  const [username, setUsername] = useState("");
  const [hostKey, setHostKey] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    const user = username.trim();
    const key = hostKey.trim();
    if (!user || !key) return;
    setSubmitting(true);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/resources", {
        body: {
          type: "ssh",
          name: DEMO_RESOURCE_NAME,
          labels: {
            ssh_host: LOOPBACK_HOST,
            ssh_port: LOOPBACK_PORT,
            ssh_user: user,
            ssh_host_key: key,
            // requires_approval is deliberately OMITTED (unflagged): the
            // approval moment is already demonstrated by WD-2/WD-11, and
            // ONB-3 needs first-run to reach a COMPLETED governed action
            // without stalling on a second operator step. Absent (or any
            // value other than the literal string "true") reads as ungated —
            // services/mcp-gateway/internal/catalog/catalog.go's
            // RequiresApproval + services/api/internal/server/server.go's
            // `res.Labels["requires_approval"] != "true"` both key off that
            // exact string.
          },
        },
      });
      if (apiErr) {
        setError(apiErr.message ? `Could not create the demo target: ${apiErr.message}` : "Could not create the demo target.");
        return;
      }
      setUsername("");
      setHostKey("");
      onCreated(); // ONB-1 flips on its own from the next derived-state read — not special-cased here.
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form aria-label="create loopback demo target" className="mt-2 space-y-2 rounded-md border border-border p-3" onSubmit={(e) => void onSubmit(e)}>
      <p className="text-xs text-muted-foreground">Paste the username and ssh host key that the command above printed.</p>
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span>
          host <span className="font-mono text-foreground">{LOOPBACK_HOST}</span>
        </span>
        <span>
          port <span className="font-mono text-foreground">{LOOPBACK_PORT}</span>
        </span>
      </div>
      <label className="block text-xs">
        <span className="text-muted-foreground">username</span>
        <input
          aria-label="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          placeholder="the --account value you chose"
          className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm font-mono"
        />
      </label>
      <label className="block text-xs">
        <span className="text-muted-foreground">ssh host key</span>
        <input
          aria-label="ssh host key"
          value={hostKey}
          onChange={(e) => setHostKey(e.target.value)}
          placeholder="the ssh host key: line the script printed"
          className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm font-mono"
        />
      </label>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <div className="flex gap-2">
        <Button variant="primary" size="xs" type="submit" disabled={submitting || !username.trim() || !hostKey.trim()}>
          {submitting ? "Creating…" : "Create"}
        </Button>
        <Button variant="default" size="xs" type="button" onClick={onCancel} disabled={submitting}>
          Cancel
        </Button>
      </div>
    </form>
  );
}

// RemoveLoopbackDemoTarget — T-118c's UI half of ONB-4 ("removable through
// the existing surface"): a DELETE against the same governed
// /v1/resources/{id} route the create actions used, gated
// identically (operator-only, per gateByDeclaredSecurity). Narrow by
// construction — resourceId always comes from the derived-state lookup
// above (never a general resource picker/manager), matching
// the create actions' own scope discipline. Surfaces the store's 409
// in-use refusal (a non-terminal session/Run/Workstream still references it)
// honestly rather than pretending the click worked; ONB-1 flips item 1 back
// on its own from the next derived-state read on success — not special-cased
// here.
function RemoveLoopbackDemoTarget({ resourceId, onRemoved }: { resourceId: string; onRemoved: () => void }) {
  const [removing, setRemoving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onRemove() {
    setRemoving(true);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.DELETE("/v1/resources/{id}", {
        params: { path: { id: resourceId } },
      });
      if (apiErr) {
        setError(apiErr.message ? `Could not remove the demo target: ${apiErr.message}` : "Could not remove the demo target.");
        return;
      }
      onRemoved();
    } finally {
      setRemoving(false);
    }
  }

  return (
    <div className="mt-2 space-y-1">
      <Button variant="destructive" size="xs" type="button" onClick={() => void onRemove()} disabled={removing}>
        {removing ? "Removing…" : "Remove the demo target"}
      </Button>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}

export function OnboardingChecklist() {
  const [dismissed, setDismissed] = useState(readDismissed);
  const { resourceExists, actionCompleted, demoResourceId, loading, refresh } = useDerivedOnboardingState();
  const [manual, setManual] = useState(false);
  const demoUnbound = useDemoUnbound(demoResourceId);

  if (dismissed) return null;

  const allDone = resourceExists && actionCompleted;

  function dismiss() {
    try {
      window.localStorage.setItem(DISMISSED_KEY, "1");
    } catch {
      // Storage can throw; the in-memory dismiss below still hides the panel
      // for the rest of this session even if it can't persist.
    }
    setDismissed(true);
  }

  return (
    <section aria-label="first-run checklist" className="rounded-md border border-border bg-background p-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-medium">Get started</h2>
        <Button variant="ghost" size="xs" onClick={dismiss}>
          Dismiss
        </Button>
      </div>
      <ul className="mt-2 space-y-1">
        <ChecklistItem done={resourceExists}>Register a resource</ChecklistItem>
        <ChecklistItem done={actionCompleted}>Complete a governed action</ChecklistItem>
      </ul>
      {!loading && !resourceExists && <CreateDemoTargetButton onCreated={refresh} />}
      {!loading && !resourceExists && !manual && (
        <p className="mt-2 text-xs text-muted-foreground">
          Prefer the terminal?{" "}
          <button type="button" className="underline" onClick={() => setManual(true)}>
            Use the script path instead
          </button>
        </p>
      )}
      {!loading && !resourceExists && manual && (
        <div role="group" aria-label="script path" className="mt-2">
          <p className="mb-2 text-xs text-muted-foreground">
            The script path asks for a username and an SSH host key that only the server can print. On the server, from the
            directory the bundle was unpacked into, run <code className="select-all font-mono text-foreground">{GENERATE_COMMAND}</code>{" "}
            — it prints both.
          </p>
          <ManualDemoTargetForm onCreated={refresh} onCancel={() => setManual(false)} />
        </div>
      )}
      {!loading && demoResourceId && <RemoveLoopbackDemoTarget resourceId={demoResourceId} onRemoved={refresh} />}
      {!loading && demoResourceId && !actionCompleted && demoUnbound && (
        <div role="group" aria-label="bind the demo credential" className="mt-2 space-y-1 text-xs text-muted-foreground">
          <p>
            Demo target resource id: <code className="select-all font-mono text-foreground">{demoResourceId}</code>
          </p>
          <p>It has no key yet. On the server, bind the one the script prepared:</p>
          <pre className="select-all overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-foreground">
            {bindCommand(demoResourceId)}
          </pre>
        </div>
      )}
      {!loading && resourceExists && !actionCompleted && (
        <p className="mt-2 text-xs text-muted-foreground">
          Next: launch a one-step command against it from{" "}
          <Link to="/runs" className="underline">
            Runs
          </Link>
          .
        </p>
      )}
      {loading && <p className="mt-2 text-xs text-muted-foreground">Checking current status…</p>}
      {!loading && allDone && (
        <p className="mt-2 text-xs text-muted-foreground">All set — dismiss whenever you&apos;re ready.</p>
      )}
    </section>
  );
}
