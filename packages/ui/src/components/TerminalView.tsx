// <TerminalView> — xterm.js terminal bridged to a services/session-broker
// WebSocket channel (ADR-0047 "xterm.js mounted in <TerminalView>"; ADR-0048
// data plane). Reserved in the @aireos/ui barrel for "the first terminal UI
// ticket" — this is it (T-018b).
//
// Wire protocol (must match services/session-broker, T-017b):
//   - inbound binary frames  = raw PTY bytes        → term.write(Uint8Array)
//   - outbound binary frames = operator keystrokes  → term.onData → send(bytes)
//   - outbound text frames   = control JSON         → resize {type,cols,rows}
//
// The broker authenticates the upgrade from the HttpOnly aireos_session cookie
// (CookieToAuthHeader, T-018a); this component never touches tokens.

import { useCallback, useEffect, useRef } from "react";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { useWebSocketChannel, type WSStatus } from "../hooks/useWebSocketChannel";

export interface TerminalViewProps {
  /** Absolute WS URL of the broker channel, e.g. wss://host/v1/sessions/<id>/channel. */
  url: string;
  /** Don't connect while false (e.g. before the session is created). */
  enabled?: boolean;
  /** Surface connection status to the parent (toolbar, banners). */
  onStatusChange?: (status: WSStatus) => void;
  className?: string;
}

const encoder = new TextEncoder();

export function TerminalView({ url, enabled = true, onStatusChange, className }: TerminalViewProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  // send is captured in a ref so xterm's onData / resize handlers (bound once on
  // mount) always reach the current socket without re-binding.
  const sendRef = useRef<(d: string | ArrayBufferView) => void>(() => {});

  // Mount the terminal once; dispose on unmount.
  useEffect(() => {
    const term = new Terminal({
      cursorBlink: true,
      convertEol: false,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
      fontSize: 13,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    if (containerRef.current) {
      term.open(containerRef.current);
      fit.fit();
    }
    const onData = term.onData((d) => sendRef.current(encoder.encode(d)));
    termRef.current = term;
    fitRef.current = fit;
    return () => {
      onData.dispose();
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, []);

  // Inbound frames: binary = PTY bytes, string = (tolerated) text. binaryType is
  // "arraybuffer", so a non-string payload is an ArrayBuffer of raw PTY bytes.
  const onMessage = useCallback((e: MessageEvent) => {
    const term = termRef.current;
    if (!term) return;
    if (typeof e.data === "string") {
      term.write(e.data);
    } else {
      term.write(new Uint8Array(e.data as ArrayBuffer));
    }
  }, []);

  const { status, send } = useWebSocketChannel({
    url,
    enabled,
    binaryType: "arraybuffer",
    // A broker WS drop ends the session server-side at v0.1.0 (T-020a/ADR-0049);
    // auto-reconnect would only re-hit a closed session. Deferred to v0.2.0.
    reconnect: false,
    onMessage,
  });
  sendRef.current = send;

  useEffect(() => {
    onStatusChange?.(status);
  }, [status, onStatusChange]);

  // Fit + tell the target the new window size (text control frame).
  const sendResize = useCallback(() => {
    const term = termRef.current;
    const fit = fitRef.current;
    if (!term || !fit) return;
    fit.fit();
    sendRef.current(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
  }, []);

  // Send the initial size once connected, and on every viewport resize.
  useEffect(() => {
    if (status === "open") sendResize();
  }, [status, sendResize]);

  useEffect(() => {
    window.addEventListener("resize", sendResize);
    return () => window.removeEventListener("resize", sendResize);
  }, [sendResize]);

  return <div ref={containerRef} role="group" aria-label="remote terminal" className={className} />;
}
