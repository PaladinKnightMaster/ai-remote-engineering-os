// useEventSource — thin React wrapper around the native EventSource (SSE) API
// (ADR-0047 §Decision item "Session-broker channel: native WebSocket +
// EventSource with thin React hooks").
//
// Scope at T-007c: skeleton with named-event subscription + status surface +
// reconnect-on-error (the browser does most of the reconnect work for SSE,
// but we surface the "closed → reopening" transition). Real-world wiring to
// services/session-broker / services/api SSE channels lands with the first
// approval-prompt ticket (ADR-0047 names <ApprovalPrompt> as the first SSE
// consumer).
//
// Auth: same `aireos_session` HttpOnly cookie path as the WebSocket hook —
// EventSource sends cookies automatically when `withCredentials` is true and
// the request is same-origin.

import { useEffect, useRef, useState } from "react";

export type SSEStatus = "idle" | "connecting" | "open" | "closed";

export interface UseEventSourceOptions {
  url: string;
  enabled?: boolean;
  /** Map of event-name → handler. The "message" handler receives unnamed events. */
  events?: Record<string, (e: MessageEvent) => void>;
  withCredentials?: boolean;
}

export interface UseEventSourceResult {
  status: SSEStatus;
}

export function useEventSource(opts: UseEventSourceOptions): UseEventSourceResult {
  const { url, enabled = true, events, withCredentials = true } = opts;
  const [status, setStatus] = useState<SSEStatus>("idle");
  const eventsRef = useRef(events);
  eventsRef.current = events;

  useEffect(() => {
    if (!enabled) return;
    setStatus("connecting");
    const es = new EventSource(url, { withCredentials });

    es.onopen = () => setStatus("open");
    es.onerror = () => setStatus("closed");
    // Bind each named event listener at mount. EventSource doesn't expose a
    // dynamic handler map, so we wire all named events here.
    // Bind by event name only — the handler is read off eventsRef.current at
    // dispatch time so callers can rotate handlers without re-binding.
    const names = Object.keys(eventsRef.current ?? {});
    const bound: Array<[string, (e: MessageEvent) => void]> = [];
    for (const name of names) {
      const wrapped = (e: MessageEvent) => eventsRef.current?.[name]?.(e);
      es.addEventListener(name, wrapped as EventListener);
      bound.push([name, wrapped]);
    }

    return () => {
      for (const [name, fn] of bound) {
        es.removeEventListener(name, fn as EventListener);
      }
      es.close();
    };
  }, [url, enabled, withCredentials]);

  return { status };
}
