// @aireos/shared-types/client tests — ADR-0047 §Decision items
// "CSRF defense: double-submit token" and "401 handling: single chokepoint."
//
// Black-box: the tests assert what an outgoing `fetch` Request looks like
// and when `onUnauthorized` fires. They do NOT import internal constants —
// the contract under test is the wire-level cookie/header behavior, not
// any specific implementation symbol. T-012's lint rule can layer on top
// of this without coupling tests to implementation.
//
// The single source of truth for the cookie name / header name / mutation
// methods set is client.ts. T-007b's Go-side must use the same names; the
// integration test in T-007c exercises the round trip end-to-end.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createApiClient } from "./client";

// openapi-fetch builds absolute URLs from baseUrl + path. Under node (vitest's
// default environment) there's no browser origin to resolve relative URLs
// against, so tests inject an absolute base. In the SPA (browser) the default
// baseUrl: "/" works because the page origin is the implicit base.
const TEST_BASE_URL = "http://api.test";

type FetchMock = ReturnType<typeof vi.fn>;

const okResponse = (status = 200) =>
  new Response(JSON.stringify({}), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const lastRequest = (mock: FetchMock): Request => {
  const calls = mock.mock.calls;
  expect(calls.length).toBeGreaterThan(0);
  const last = calls[calls.length - 1]!;
  // openapi-fetch passes a Request as the first arg.
  return last[0] as Request;
};

describe("createApiClient — CSRF double-submit (ADR-0047)", () => {
  let fetchMock: FetchMock;

  beforeEach(() => {
    fetchMock = vi.fn(async () => okResponse(200));
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("attaches X-CSRF-Token on POST when the csrf cookie is present", async () => {
    const client = createApiClient({ baseUrl: TEST_BASE_URL, readCookie: () => "csrf-abc-123" });
    await client.POST("/v1/auth/login", {
      body: { username: "operator", password: "pw" },
    });
    expect(lastRequest(fetchMock).headers.get("X-CSRF-Token")).toBe("csrf-abc-123");
  });

  it("omits X-CSRF-Token when the csrf cookie is absent", async () => {
    const client = createApiClient({ baseUrl: TEST_BASE_URL, readCookie: () => undefined });
    await client.POST("/v1/auth/login", {
      body: { username: "operator", password: "pw" },
    });
    expect(lastRequest(fetchMock).headers.get("X-CSRF-Token")).toBeNull();
  });

  it("never attaches X-CSRF-Token on safe (GET) requests, even with a cookie", async () => {
    const client = createApiClient({ baseUrl: TEST_BASE_URL, readCookie: () => "csrf-abc-123" });
    await client.GET("/v1/auth/me");
    expect(lastRequest(fetchMock).headers.get("X-CSRF-Token")).toBeNull();
  });
});

describe("createApiClient — 401 chokepoint (ADR-0047)", () => {
  let fetchMock: FetchMock;

  beforeEach(() => {
    fetchMock = vi.fn(async () => okResponse(200));
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("fires onUnauthorized exactly once on a 401 response", async () => {
    fetchMock.mockResolvedValueOnce(okResponse(401));
    const onUnauthorized = vi.fn();
    const client = createApiClient({ baseUrl: TEST_BASE_URL, onUnauthorized, readCookie: () => undefined });
    await client.GET("/v1/auth/me");
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  it("does not fire onUnauthorized on non-401 responses (200, 403, 500)", async () => {
    const onUnauthorized = vi.fn();
    const client = createApiClient({ baseUrl: TEST_BASE_URL, onUnauthorized, readCookie: () => undefined });

    fetchMock.mockResolvedValueOnce(okResponse(200));
    await client.GET("/v1/auth/me");
    fetchMock.mockResolvedValueOnce(okResponse(403));
    await client.GET("/v1/auth/me");
    fetchMock.mockResolvedValueOnce(okResponse(500));
    await client.GET("/v1/auth/me");

    expect(onUnauthorized).not.toHaveBeenCalled();
  });
});
