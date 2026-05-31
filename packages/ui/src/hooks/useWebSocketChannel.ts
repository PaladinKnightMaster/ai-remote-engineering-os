// useWebSocketChannel — thin React wrapper around the native WebSocket API
// for talking to services/session-broker (ADR-0047 §Decision item
// "Session-broker channel: native WebSocket + EventSource with thin React
// hooks").
//
// Scope at T-007c: skeleton with auto-reconnect + exponential backoff +
// status surface. Streaming-grade semantics (multiplexed channels,
// backpressure, binary framing) land with the first session-broker UI
// ticket — that's when this hook gets its real workout.
//
// Auth piggybacks on the same `aireos_session` HttpOnly cookie (the WS
// upgrade is an HTTP request; the browser sends the cookie automatically).
// The hook itself doesn't touch tokens.

import { useEffect, useRef, useState } from "react";

export type WSStatus = "idle" | "connecting" | "open" | "closing" | "closed";

export interface UseWebSocketChannelOptions {
  url: string;
  /** Don't open the socket while false. Toggling true opens; toggling false closes. */
  enabled?: boolean;
  /** Per-message callback. Receives the raw MessageEvent. */
  onMessage?: (e: MessageEvent) => void;
  /** Initial reconnect delay (ms). Doubles up to maxBackoffMs on consecutive failures. */
  initialBackoffMs?: number;
  maxBackoffMs?: number;
  /**
   * Optional subprotocols passed to the WebSocket constructor.
   *
   * **Caution: pass a stable reference.** If a consumer passes an inline
   * array literal (`protocols={["v1"]}`) each render creates a new array
   * identity, which trips the effect's deps and causes a reconnect on
   * every parent re-render. Use `useMemo` or a module-level constant.
   */
  protocols?: string | string[];
  /**
   * Frame decoding for binary messages. Terminal streams set "arraybuffer"
   * so PTY bytes arrive synchronously and in order in `onMessage` (the default
   * "blob" forces an async `.arrayBuffer()` read that can reorder frames).
   */
  binaryType?: BinaryType;
  /**
   * Auto-reconnect with backoff on close (default true). `<TerminalView>` sets
   * this false: at v0.1.0 a broker WS drop *ends* the session server-side
   * (T-020a / ADR-0049), so reconnecting would only hit a closed session —
   * reconnect-with-fresh-shell is deferred to v0.2.0. SSE / non-terminal
   * consumers keep the default.
   */
  reconnect?: boolean;
}

export interface UseWebSocketChannelResult {
  status: WSStatus;
  /** Send a string or binary frame. No-op when the socket isn't open. */
  send: (data: string | ArrayBufferLike | Blob | ArrayBufferView) => void;
}

export function useWebSocketChannel(
  opts: UseWebSocketChannelOptions,
): UseWebSocketChannelResult {
  const {
    url,
    enabled = true,
    onMessage,
    initialBackoffMs = 500,
    maxBackoffMs = 15_000,
    protocols,
    binaryType,
    reconnect = true,
  } = opts;

  const [status, setStatus] = useState<WSStatus>("idle");
  const wsRef = useRef<WebSocket | null>(null);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  useEffect(() => {
    if (!enabled) {
      return;
    }
    let cancelled = false;
    let backoff = initialBackoffMs;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const open = () => {
      if (cancelled) return;
      setStatus("connecting");
      const ws = new WebSocket(url, protocols);
      if (binaryType) ws.binaryType = binaryType;
      wsRef.current = ws;
      ws.onopen = () => {
        if (cancelled) return;
        backoff = initialBackoffMs;
        setStatus("open");
      };
      ws.onmessage = (e) => {
        onMessageRef.current?.(e);
      };
      ws.onclose = () => {
        if (cancelled) return;
        setStatus("closed");
        if (!reconnect) return; // terminal: a drop ends the session (T-020a)
        // Exponential backoff up to the cap.
        reconnectTimer = setTimeout(open, backoff);
        backoff = Math.min(backoff * 2, maxBackoffMs);
      };
      ws.onerror = () => {
        // onclose follows; reconnect is driven from there.
      };
    };

    open();

    return () => {
      cancelled = true;
      if (reconnectTimer) clearTimeout(reconnectTimer);
      if (wsRef.current) {
        setStatus("closing");
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [url, enabled, initialBackoffMs, maxBackoffMs, protocols, binaryType, reconnect]);

  const send: UseWebSocketChannelResult["send"] = (data) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(data);
  };

  return { status, send };
}
