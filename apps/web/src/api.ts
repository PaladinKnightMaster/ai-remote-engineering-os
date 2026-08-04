// Single module-level API client for the SPA — ADR-0047.
//
// One `createApiClient` instance per app boot. Wired with the 401
// chokepoint: any 401 response (after the bearer reaches services/api's
// gate and is rejected) clears the session-local Capability state and
// forces a fresh login. `window.location.assign` over SPA navigation by
// design — page reload also re-bootstraps the CapabilityProvider, which
// is the cleanest way to drop any stale in-memory state.

import { createApiClient } from "@aireos/shared-types";
import type { Capability } from "@aireos/ui";

// Use the page origin as the absolute baseUrl. In dev that's
// http://localhost:5173 (Vite proxies /v1/* to services/api per
// vite.config.ts). In prod that's whatever Caddy/Traefik fronts both
// the SPA and services/api on (same-origin per ADR-0047 §10).
//
// Absolute base is required because openapi-fetch resolves paths via
// new Request(url), which in node-undici (used by jsdom + SSR) rejects
// bare-path URLs. Real browsers tolerate "/path" but jsdom doesn't —
// using window.location.origin keeps test and runtime aligned.
const BASE_URL =
  typeof window !== "undefined" && window.location?.origin
    ? window.location.origin
    : "http://localhost";

// Routes that are intentionally reachable with no session at all. A fresh
// mount of ANY of them triggers <CapabilityProvider>'s own bootstrap
// GET /v1/auth/me (App.tsx wraps the whole router in one provider instance,
// not per-route) — which 401s by design there, since there is no cookie yet.
// The 401 chokepoint below must not hard-redirect away from one of these
// (T-115b: it did, for /pair — the hard redirect aborted the in-flight
// redeem POST before it could complete, mid-flight, every time).
const PUBLIC_PATHS = ["/login", "/pair"];

export const apiClient = createApiClient({
  baseUrl: BASE_URL,
  onUnauthorized: () => {
    // Avoid an infinite reload loop / an aborted in-flight request on a
    // route that is public BY DESIGN (a 401 there is expected, not a signal
    // to bounce the user — e.g. /login itself, or /pair mid-redeem).
    if (!PUBLIC_PATHS.includes(window.location.pathname)) {
      window.location.assign("/login");
    }
  },
});

/**
 * Adapter for <CapabilityProvider fetchCapability={...}>.
 * Maps the typed Identity from /v1/auth/me into the packages/ui
 * `Capability` shape (subject + edge + deploymentProfile + scope). Returns
 * null on any error so the provider treats the user as unauthenticated.
 */
export async function fetchCurrentCapability(): Promise<Capability | null> {
  const { data, error } = await apiClient.GET("/v1/auth/me");
  if (error || !data) return null;
  return {
    subject: data.subject,
    edge: data.edge,
    deploymentProfile: data.deploymentProfile,
    scope: data.scope,
  };
}
