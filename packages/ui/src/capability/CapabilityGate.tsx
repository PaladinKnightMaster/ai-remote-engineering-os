// CapabilityGate — conditionally render children based on the operator's
// capability edge (ADR-0047 §Decision item "Capability-edge UI enforcement").
//
// Server-side `RequireCapability` (ADR-0046) is the actual security boundary;
// this gate is UX only. Hiding a button does not protect anything — the
// server enforces. The gate exists so users don't see buttons that would
// just 403 when clicked.

import { type ReactNode } from "react";
import { useCapability } from "./CapabilityProvider";

export interface CapabilityGateProps {
  /** The capability edge required (from docs/security/agent-permission-model.md). */
  edge: string;
  /**
   * Optional subject allow-list. If present, the gate also requires the
   * current capability's subject to be in this set. Empty / undefined = any
   * subject with the matching edge is allowed.
   */
  subjects?: string[];
  children: ReactNode;
  /** Rendered when the gate denies. Defaults to nothing (collapse). */
  fallback?: ReactNode;
}

export function CapabilityGate({
  edge,
  subjects,
  children,
  fallback = null,
}: CapabilityGateProps) {
  const { capability } = useCapability();
  if (!capability) return <>{fallback}</>;
  if (capability.edge !== edge) return <>{fallback}</>;
  if (subjects && subjects.length > 0 && !subjects.includes(capability.subject)) {
    return <>{fallback}</>;
  }
  return <>{children}</>;
}
