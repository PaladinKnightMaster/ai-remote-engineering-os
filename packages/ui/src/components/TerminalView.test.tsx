// TerminalView wiring tests. xterm.js needs a real canvas/DOM it can't get in
// jsdom, and useWebSocketChannel needs a WebSocket global jsdom lacks — so both
// are mocked and the test asserts the *protocol wiring* (the contract with
// services/session-broker, T-017b), not xterm rendering.

import { cleanup, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => ({
  state: {
    onDataCb: null as ((d: string) => void) | null,
    writes: [] as unknown[],
    status: "connecting" as string,
    send: vi.fn(),
    hookOpts: null as Record<string, unknown> | null,
  },
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 100;
    rows = 30;
    loadAddon() {}
    open() {}
    onData(cb: (d: string) => void) {
      h.state.onDataCb = cb;
      return { dispose() {} };
    }
    write(d: unknown) {
      h.state.writes.push(d);
    }
    dispose() {}
  },
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));

vi.mock("../hooks/useWebSocketChannel", () => ({
  useWebSocketChannel: (opts: Record<string, unknown>) => {
    h.state.hookOpts = opts;
    return { status: h.state.status, send: h.state.send };
  },
}));

import { TerminalView } from "./TerminalView";

describe("TerminalView", () => {
  beforeEach(() => {
    h.state.onDataCb = null;
    h.state.writes = [];
    h.state.status = "connecting";
    h.state.hookOpts = null;
    h.state.send.mockClear();
  });
  afterEach(cleanup);

  it("connects with arraybuffer frames, no auto-reconnect, at the given url", () => {
    render(<TerminalView url="ws://host/v1/sessions/s1/channel" />);
    expect(h.state.hookOpts?.url).toBe("ws://host/v1/sessions/s1/channel");
    expect(h.state.hookOpts?.binaryType).toBe("arraybuffer");
    // A broker drop ends the session at v0.1.0 — terminal must not auto-reconnect.
    expect(h.state.hookOpts?.reconnect).toBe(false);
  });

  it("writes inbound PTY bytes (ArrayBuffer) to the terminal", () => {
    render(<TerminalView url="ws://host/c" />);
    const onMessage = h.state.hookOpts?.onMessage as (e: MessageEvent) => void;
    const bytes = new TextEncoder().encode("hi");
    onMessage({ data: bytes.buffer } as MessageEvent);
    const written = h.state.writes.at(-1);
    // Realm-safe: jsdom's test realm and the module realm have distinct
    // Uint8Array constructors, so toBeInstanceOf is unreliable here. isView +
    // byte contents prove it's the right binary payload.
    expect(ArrayBuffer.isView(written as ArrayBufferView)).toBe(true);
    expect(Array.from(written as Uint8Array)).toEqual([104, 105]); // "hi"
  });

  it("sends operator keystrokes as binary frames", () => {
    render(<TerminalView url="ws://host/c" />);
    h.state.onDataCb?.("x");
    const arg = h.state.send.mock.calls.at(-1)?.[0];
    expect(typeof arg).not.toBe("string"); // binary frame, not text
    expect(ArrayBuffer.isView(arg as ArrayBufferView)).toBe(true);
    expect(Array.from(arg as Uint8Array)).toEqual([120]); // 'x'
  });

  it("sends a resize control frame (text JSON) once the socket opens", () => {
    h.state.status = "open";
    render(<TerminalView url="ws://host/c" />);
    const resizeCall = h.state.send.mock.calls
      .map((c) => c[0])
      .find((a) => typeof a === "string" && a.includes('"resize"'));
    expect(resizeCall).toBeTruthy();
    const msg = JSON.parse(resizeCall as string);
    expect(msg).toMatchObject({ type: "resize", cols: 100, rows: 30 });
  });
});
