// @aireos/ui — public barrel.
//
// T-007a stands up the package boundary only. The component + hook surface
// listed in ADR-0047 §Verification §4 lands with T-007c:
//
//   - <CapabilityProvider>, <CapabilityGate>          — capability-edge UI
//   - <ApprovalPrompt>                                — egress / approval UX
//   - <RemoteSessionView>, <TerminalView>             — Guacamole / xterm wrappers
//   - useWebSocketChannel, useEventSource             — session-broker hooks
//
// Until then the package is intentionally empty so the workspace shape exists
// without speculative components. Adding it as an explicit empty export keeps
// TypeScript's `isolatedModules` happy.

export {};
