// Pair route — the DEVICE half of QR device pairing (T-115b; ADR-0068 §D6,
// GOV-13, FE-38). Deliberately UNAUTHENTICATED and outside RequireAuth
// (App.tsx): the device landing here has no session yet — it holds only the
// one-time secret carried in the QR's URL fragment (rendered by the desk's
// /pairing route, T-115a).
//
// GOV-13 ("the secret never reaches the server in a URL"): the fragment is
// read from `window.location.hash` — which browsers never transmit to the
// server, never log, and never put in a Referer header — and posted
// deliberately, from JS, to the redeem endpoint. The secret is NEVER placed
// in a query string or a path segment.
//
// Hygiene beyond GOV-13's letter: the fragment is stripped via
// history.replaceState the instant it is read, so the one-time secret does
// not linger in the address bar, browser history, or a screenshot the
// operator takes while pairing.
//
// Uniform failure (§0 of the ticket): the redeem endpoint collapses wrong
// secret / expired / consumed / unknown id into one 404 (no oracle, mirroring
// SEC-34's per-request liveness posture) — this route renders exactly one
// error message and never tries to distinguish the cause from the response.
// The secret itself is never rendered, logged, or included in that message.

import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useCapability } from "@aireos/ui";
import { apiClient } from "../api";

type Phase = "redeeming" | "success" | "error";

/** Parses "<id>.<secret>" out of a raw fragment (no leading '#'). Neither half
 * may be empty — a malformed fragment is refused the same as a wrong secret
 * (no oracle). */
function parseFragment(raw: string): { id: string; secret: string } | null {
  const dot = raw.indexOf(".");
  if (dot <= 0 || dot === raw.length - 1) return null;
  return { id: raw.slice(0, dot), secret: raw.slice(dot + 1) };
}

export function Pair() {
  const [phase, setPhase] = useState<Phase>("redeeming");
  const navigate = useNavigate();
  const { reload } = useCapability();
  // StrictMode/dev double-invokes effects; the fragment is gone after the
  // first read (replaceState), so a guard keeps a second invocation from
  // spuriously reading an empty hash and reporting a false error.
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    const raw = window.location.hash.replace(/^#/, "");
    if (window.location.hash) {
      history.replaceState(null, "", window.location.pathname + window.location.search);
    }
    const parsed = parseFragment(raw);
    if (!parsed) {
      setPhase("error");
      return;
    }
    void (async () => {
      const { error } = await apiClient.POST("/v1/pairing-requests/{id}/redeem", {
        params: { path: { id: parsed.id } },
        body: { secret: parsed.secret },
      });
      if (error) {
        setPhase("error");
        return;
      }
      // The Set-Cookie from redeem is already applied; hydrate the
      // capability context off it before routing to the approval surface, so
      // the phone's shell (Dashboard/CapabilityGate) already knows its scope
      // the moment it renders (§1/§3 of the ticket).
      await reload();
      setPhase("success");
      navigate("/invocations", { replace: true });
    })();
  }, [navigate, reload]);

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div
        role="status"
        aria-live="polite"
        className="w-full max-w-sm space-y-4 rounded-md border border-border bg-background p-6 text-center shadow-sm"
      >
        <h1 className="text-xl font-semibold">Pairing this device</h1>
        {phase === "redeeming" && <p className="text-sm text-muted-foreground">Connecting…</p>}
        {phase === "success" && <p className="text-sm text-muted-foreground">Paired — redirecting…</p>}
        {phase === "error" && (
          <div role="alert" className="rounded-md bg-destructive/10 px-3 py-2 text-sm text-destructive">
            This pairing link is invalid or has expired. Ask the desk operator for a new QR code.
          </div>
        )}
      </div>
    </div>
  );
}
