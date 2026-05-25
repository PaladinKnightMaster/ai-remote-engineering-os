// @aireos/shared-types/client — typed openapi-fetch wrapper (ADR-0047).
//
// What this file owns at T-007a:
//
//  - The single openapi-fetch construction site for the SPA. ADR-0047 §Negative
//    item ("CSRF double-submit is implementation discipline") plus the
//    capability-edge UI contract require every mutation request to attach an
//    X-CSRF-Token header echoed from the non-HttpOnly `aireos_csrf` cookie.
//    Centralizing it here means feature code never touches cookies / CSRF
//    directly — it just calls the typed client. T-012 will lint-enforce
//    "no direct fetch() outside this wrapper."
//
//  - The 401 chokepoint hook (ADR-0047 §Decision item "401 handling: single
//    chokepoint"). The wrapper is environment-agnostic; the SPA's <App> wires
//    `onUnauthorized` to clear the <CapabilityProvider> cache and navigate to
//    `/login` (T-007c).
//
// What is intentionally NOT here at T-007a:
//
//  - No token storage. The PASETO session cookie is HttpOnly per ADR-0047 §5;
//    JS cannot read it. `credentials: "same-origin"` lets the browser attach
//    it automatically on the same-origin deploy posture from ADR-0047 §10.
//
//  - No refresh logic. ADR-0047 §Decision item "Refresh: server-side cookie
//    rotation" — services/api re-issues the cookie in the second half of TTL
//    (lands in T-007b). The client is unaware.
//
// The `aireos_csrf` cookie name + `X-CSRF-Token` header name are settled in
// the T-007b consult sign-off and are the contract this file binds to.

import createClient, { type Middleware } from "openapi-fetch";
import type { paths } from "./generated/openapi";

const CSRF_COOKIE = "aireos_csrf";
const CSRF_HEADER = "X-CSRF-Token";
const MUTATION_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export interface CreateApiClientOptions {
  /** Base URL for the API. Defaults to "/" (same-origin per ADR-0047 §10). */
  baseUrl?: string;
  /**
   * Invoked exactly once per 401 response. The SPA's <App> wires this to
   * clear the <CapabilityProvider> cache and navigate to /login (T-007c).
   */
  onUnauthorized?: () => void;
  /**
   * Cookie reader override. Production code uses the default (document.cookie);
   * tests inject a stub so the wrapper can be exercised in node without a DOM.
   */
  readCookie?: (name: string) => string | undefined;
}

const defaultCookieReader = (name: string): string | undefined => {
  if (typeof document === "undefined") return undefined;
  // RFC 6265 cookies are name=value pairs separated by "; ". Quote the name to
  // avoid regex injection from caller-controlled cookie names.
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const match = document.cookie.match(new RegExp("(?:^|; )" + escaped + "=([^;]*)"));
  return match ? decodeURIComponent(match[1]!) : undefined;
};

export function createApiClient(options: CreateApiClientOptions = {}) {
  const baseUrl = options.baseUrl ?? "/";
  const readCookie = options.readCookie ?? defaultCookieReader;
  const onUnauthorized = options.onUnauthorized;

  const client = createClient<paths>({
    baseUrl,
    credentials: "same-origin",
  });

  const csrfAndUnauthorized: Middleware = {
    async onRequest({ request }) {
      if (MUTATION_METHODS.has(request.method.toUpperCase())) {
        const csrf = readCookie(CSRF_COOKIE);
        if (csrf !== undefined && csrf !== "") {
          request.headers.set(CSRF_HEADER, csrf);
        }
      }
      return request;
    },
    async onResponse({ response }) {
      if (response.status === 401) onUnauthorized?.();
      return response;
    },
  };

  client.use(csrfAndUnauthorized);
  return client;
}

// Exported for the T-012 lint rule + tests that assert the cookie/header
// contract has not silently drifted. Not part of the public surface.
export const __INTERNAL__ = {
  CSRF_COOKIE,
  CSRF_HEADER,
  MUTATION_METHODS,
};
