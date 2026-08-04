// Pairing route — the desk half of QR device pairing (T-115a; ADR-0068 §D6,
// GOV-12/GOV-13/GOV-14, FE-39). An authenticated desktop operator creates a
// one-time pairing request, scans the QR into a phone (or copies the link),
// and manages the paired sessions that result. The device-side /pair redeem
// UI, the paired-session marker, and the mobile approval surface are T-115b.
//
// SEC-41 ("hidden by capability, not by CSS"): there is no client-exposed
// AIREOS_PAIRING_ENABLED flag — the ADR-0065/0066 no-oracle posture the four
// pairing routes already follow (404 whether disabled or merely unknown)
// extends to the frontend by construction: this route asks the real API
// (GET /v1/paired-sessions) and renders the "not available" state from that
// live 404, never from a hardcoded style toggle.
//
// GOV-12/§D6: the create response's `secret` is the ONLY place the plaintext
// ever appears. It lives in this component's state alone — never persisted,
// never logged, never re-fetched — and is gone the moment the operator
// navigates away (the route unmounts) or creates another request.

import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { QRCode } from "@aireos/ui";
import type { components } from "@aireos/shared-types/generated/openapi";
import { apiClient } from "../api";

type PairedSession = components["schemas"]["PairedSession"];

interface CreatedPairing {
  id: string;
  secret: string;
  expiresAt: string;
}

export function Pairing() {
  // null = not yet known; false = the live GET 404'd (disabled on this
  // deployment); true = enabled and reachable.
  const [available, setAvailable] = useState<boolean | null>(null);
  const [sessions, setSessions] = useState<PairedSession[]>([]);
  const [loadingSessions, setLoadingSessions] = useState(true);
  const [label, setLabel] = useState("");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedPairing | null>(null);
  const [revokingId, setRevokingId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadSessions = useCallback(async () => {
    setLoadingSessions(true);
    const { data, error: apiErr, response } = await apiClient.GET("/v1/paired-sessions", {});
    if (response.status === 404) {
      // No oracle (SEC-41): this is the ONE signal the frontend uses to know
      // pairing is off — the same 404 an unknown route would answer.
      setAvailable(false);
      setSessions([]);
      setError(null);
    } else if (apiErr || !data) {
      setAvailable(true);
      setError("Could not load paired sessions.");
    } else {
      setAvailable(true);
      setSessions(data.items ?? []);
      setError(null);
    }
    setLoadingSessions(false);
  }, []);

  useEffect(() => {
    void loadSessions();
  }, [loadSessions]);

  async function onCreate() {
    setCreating(true);
    setError(null);
    try {
      const { data, error: apiErr } = await apiClient.POST("/v1/pairing-requests", {
        body: { label: label.trim() || undefined },
      });
      if (apiErr || !data) {
        setError(apiErr?.message ? `Could not create a pairing request: ${apiErr.message}` : "Could not create a pairing request.");
        return;
      }
      // Replaces any prior secret still on screen — only one live at a time.
      setCreated({ id: data.id, secret: data.secret, expiresAt: data.expiresAt });
      setLabel("");
    } finally {
      setCreating(false);
    }
  }

  async function onRevoke(id: string) {
    setRevokingId(id);
    setError(null);
    try {
      const { error: apiErr } = await apiClient.POST("/v1/paired-sessions/{id}/revoke", { params: { path: { id } } });
      if (apiErr) setError("Could not revoke the session — it may already be revoked. Refresh and try again.");
      await loadSessions();
    } finally {
      setRevokingId(null);
    }
  }

  const pairingUrl = created
    ? `${window.location.origin}/pair#${created.id}.${created.secret}`
    : null;

  return (
    <div className="mx-auto max-w-3xl space-y-6 p-6">
      <header className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Device pairing</h1>
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => void loadSessions()} className="text-sm text-muted-foreground hover:underline">
            Refresh
          </button>
          <Link to="/" className="text-sm text-muted-foreground hover:underline">
            ← Dashboard
          </Link>
        </div>
      </header>

      {available === false ? (
        <section className="rounded-md border border-border bg-background p-4">
          <p className="text-sm text-muted-foreground">
            Device pairing is not enabled on this deployment.
          </p>
        </section>
      ) : (
        <>
          {/* Create a pairing request: the label is set HERE, at request time
              (T-113) — a redeeming device never chooses how its own session
              appears in this operator's revoke list, below. */}
          <section className="space-y-3 rounded-md border border-border p-4">
            <h2 className="text-sm font-medium">Pair a new device</h2>
            <form
              aria-label="create pairing request"
              className="flex items-end gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                void onCreate();
              }}
            >
              <label className="flex-1 text-xs text-muted-foreground">
                Label
                <input
                  aria-label="label"
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                  placeholder="e.g. my phone"
                  className="mt-1 w-full rounded-md border border-input bg-background px-2 py-1 text-sm"
                />
              </label>
              <button
                type="submit"
                disabled={creating || !label.trim()}
                className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground disabled:opacity-60"
              >
                {creating ? "Creating…" : "Create pairing code"}
              </button>
            </form>

            {created && pairingUrl && (
              <div className="space-y-3 rounded-md border border-border bg-muted p-4">
                <div className="flex justify-center">
                  <QRCode value={pairingUrl} size={220} label="Pairing link — scan with the device to pair" />
                </div>
                <p className="text-xs text-muted-foreground">
                  Can&apos;t scan? Enter this link on the device instead:
                </p>
                <code className="block select-all break-all rounded-md bg-background px-2 py-1 text-xs">
                  {pairingUrl}
                </code>
                <p className="text-xs text-muted-foreground">
                  Single-use — expires at <span className="font-mono">{created.expiresAt}</span>. This code will
                  not be shown again; if it lapses, create a new one.
                </p>
                <button
                  type="button"
                  onClick={() => setCreated(null)}
                  className="rounded-md border border-border px-2 py-1 text-xs"
                >
                  Done
                </button>
              </div>
            )}
          </section>

          {error && (
            <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          {/* Paired sessions (GOV-14): live and revoked alike, revocable. */}
          <section className="rounded-md border border-border">
            <h2 className="p-3 text-sm font-medium">Paired sessions</h2>
            {loadingSessions ? (
              <p className="p-4 text-sm text-muted-foreground">Loading…</p>
            ) : sessions.length === 0 ? (
              <p className="p-4 text-sm text-muted-foreground">No paired sessions.</p>
            ) : (
              <ul className="divide-y divide-border">
                {sessions.map((s) => (
                  <li key={s.id} className="space-y-1 p-3 text-sm">
                    <div className="flex items-center justify-between">
                      <span className="min-w-0 font-medium">{s.label || "(unlabeled)"}</span>
                      <div className="flex items-center gap-3">
                        <span className="text-muted-foreground">{s.revokedAt ? "revoked" : "live"}</span>
                        {!s.revokedAt && (
                          <button
                            onClick={() => void onRevoke(s.id)}
                            disabled={revokingId === s.id}
                            className="rounded-md bg-destructive px-2 py-1 text-xs font-medium text-destructive-foreground disabled:opacity-60"
                          >
                            {revokingId === s.id ? "Revoking…" : "Revoke"}
                          </button>
                        )}
                      </div>
                    </div>
                    <p className="text-xs text-muted-foreground">
                      created <span className="font-mono">{s.createdAt}</span> · expires{" "}
                      <span className="font-mono">{s.expiresAt}</span>
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
    </div>
  );
}
