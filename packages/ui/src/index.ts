// @aireos/ui — public barrel.
//
// T-007c lands the capability provider/gate + the WS/SSE hooks (ADR-0047
// §Verification §4). The remaining components named there
// (<ApprovalPrompt>, <RemoteSessionView>, <TerminalView>) land with the
// feature tickets that consume them:
//
//   - <RemoteSessionView>    — first RDP/VNC session ticket
//                              (Guacamole on a <canvas>); still deferred.
//
// <TerminalView> (xterm.js) landed in T-018b; <ApprovalPrompt> (ADR-0054
// human-in-the-loop) in T-031c — both exported below.
//
// Deferring per "search-before-create" in design-system-governance §3 —
// vendoring components without a concrete consumer would speculate on
// UX that the relevant UI specs (docs/ui-ux/) haven't pinned down yet.

export { CapabilityProvider, useCapability } from "./capability/CapabilityProvider";
export type { Capability, CapabilityContextValue, CapabilityProviderProps } from "./capability/CapabilityProvider";
export { CapabilityGate } from "./capability/CapabilityGate";
export type { CapabilityGateProps } from "./capability/CapabilityGate";

export { useWebSocketChannel } from "./hooks/useWebSocketChannel";
export type {
  WSStatus,
  UseWebSocketChannelOptions,
  UseWebSocketChannelResult,
} from "./hooks/useWebSocketChannel";

export { useEventSource } from "./hooks/useEventSource";
export type {
  SSEStatus,
  UseEventSourceOptions,
  UseEventSourceResult,
} from "./hooks/useEventSource";

export { TerminalView } from "./components/TerminalView";
export type { TerminalViewProps } from "./components/TerminalView";

export { ApprovalPrompt } from "./components/ApprovalPrompt";
export type { ApprovalPromptProps } from "./components/ApprovalPrompt";
