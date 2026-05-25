// @aireos/shared-types — public barrel (ADR-0047, T-007a).
//
// Consumers (apps/web in T-007c) import the typed client factory from "./client"
// or directly from the package root. The generated OpenAPI types are re-exported
// for callers that need them at the type level (e.g. capability-edge UI typing
// against the Identity schema).

export { createApiClient } from "./client";
export type { CreateApiClientOptions } from "./client";
export type { paths, components, operations } from "./generated/openapi";
