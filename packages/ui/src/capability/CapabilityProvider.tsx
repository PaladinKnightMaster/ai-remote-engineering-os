// CapabilityProvider — the capability-edge UI state (ADR-0047 §Decision).
//
// The ADR's contract: GET /v1/auth/me returns the operator's current
// CapabilityToken.edge + subject; this provider holds them in React context
// and exposes reload / clear hooks for the SPA's auth-transition points
// (login success, logout, 401 chokepoint).
//
// **Server-side `RequireCapability` (ADR-0046) remains the security
// boundary.** This provider is UX only — a user who edits the React context
// in DevTools to claim capabilities they don't have will see additional
// children render and immediately receive 403s when they activate them.
//
// The provider is decoupled from the API client: it takes a
// `fetchCapability` callback so packages/ui never imports
// @aireos/shared-types (and so tests can stub the fetch trivially).

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

/**
 * Minimal capability shape. Mirrors the Identity schema from the OpenAPI
 * fragment by structure (subject + edge); intentionally NOT imported from
 * @aireos/shared-types so the package boundary stays clean. Consumers map
 * their API response to this shape at the call site.
 */
export interface Capability {
  subject: string;
  edge: string;
  /**
   * The active AIREOS_DEPLOYMENT_PROFILE (lan-only / vpn-first / dmz-first),
   * or "unknown" when the stack was not installed through install.sh.
   * Additive per ADR-0068 §D2 (OI-24) — optional so older callers/tests that
   * don't set it keep compiling.
   */
  deploymentProfile?: string;
  /**
   * The current session's named, versioned scope (ADR-0068 §D5) — e.g.
   * "approvals@1" for a paired device session. A scope narrows WITHIN this
   * `edge`, never a separate edge. Absent for an ordinary desktop session
   * (mirrors Identity.scope, T-115b) — consumers must not invent "" here.
   */
  scope?: string;
}

export interface CapabilityContextValue {
  capability: Capability | null;
  /** True until the first reload settles. */
  loading: boolean;
  /** Re-fetch from `fetchCapability`. Called after login, after sliding refresh, on retry. */
  reload: () => Promise<void>;
  /** Clear the in-memory capability without a fetch. Called by the 401 chokepoint. */
  clear: () => void;
}

const CapabilityContext = createContext<CapabilityContextValue | null>(null);

export interface CapabilityProviderProps {
  fetchCapability: () => Promise<Capability | null>;
  children: ReactNode;
  /**
   * Optional initial value. Useful for tests (skip the network round-trip)
   * and for SSR/preload scenarios. When provided, the initial `loading` is
   * false and no mount-fetch fires.
   */
  initialValue?: Capability | null;
}

export function CapabilityProvider({
  fetchCapability,
  children,
  initialValue,
}: CapabilityProviderProps) {
  const [capability, setCapability] = useState<Capability | null>(initialValue ?? null);
  const [loading, setLoading] = useState(initialValue === undefined);
  // The fetch callback may change identity across renders; we capture the
  // latest in a ref so `reload` is stable (consumers can put it in deps).
  const fetchRef = useRef(fetchCapability);
  fetchRef.current = fetchCapability;

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      setCapability(await fetchRef.current());
    } catch {
      setCapability(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const clear = useCallback(() => {
    setCapability(null);
  }, []);

  useEffect(() => {
    if (initialValue === undefined) {
      void reload();
    }
    // Intentionally mount-only; reload is stable, initialValue is a prop
    // that should not change across the provider's lifetime.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <CapabilityContext.Provider value={{ capability, loading, reload, clear }}>
      {children}
    </CapabilityContext.Provider>
  );
}

export function useCapability(): CapabilityContextValue {
  const ctx = useContext(CapabilityContext);
  if (!ctx) {
    throw new Error("useCapability must be used inside <CapabilityProvider>");
  }
  return ctx;
}
