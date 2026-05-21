---
name: T-006 session-broker skeleton (HTTP/2 + WebSocket transport)
about: Stand up services/session-broker with a capability-gated WebSocket channel and an HTTP/2 endpoint over the shared middleware chain
title: "T-006: session-broker skeleton (HTTP/2 + WebSocket)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-006 — see `docs/tickets/README.md`. The last P0 backend service skeleton.
Closes **ADR-0043 §Verification item 5** ("`services/session-broker` serves at
least one HTTP/2 endpoint and one WebSocket endpoint via stdlib `http.Server` + a
`net/http`-compatible WebSocket library, with the same audit middleware chain
applied") and settles the WebSocket-library choice ADR-0043 left to this ticket.
Lead lens: **Tech Lead** (transport); **Permission Model Reviewer** (the
agent-remote-session edge).

## Goal

Stand up `services/session-broker`: a capability-gated WebSocket channel
(skeleton echo transport) and an HTTP/2 readiness endpoint, served over stdlib
`http.Server` with the shared `RequireCapability` middleware.

## Priority

P0. Completes the P0 backend service skeletons (api, audit-service, now broker).

## Scope decisions (settled in consult / during implementation)

- **WebSocket library: `github.com/coder/websocket` v1.8.12** — the maintained
  successor to the `nhooyr.io/websocket` ADR-0043 named (the package was donated
  to Coder; import path changed). Context-native, `net/http`-compatible, and
  near-zero-dependency — best fit for the tight supply chain (ADR-0041). Gorilla
  (the "more-deployed" fallback ADR-0043 offered) was not needed.
- **HTTP/2 via `h2c`** (`golang.org/x/net/http2/h2c`): TLS gives HTTP/2 for free
  in production; for cleartext dev/CI, `h2c` makes the broker demonstrably speak
  HTTP/2 so the ADR-0043 verification is testable (`resp.ProtoMajor == 2`).
- **Skeleton scope.** The channel echoes (transport proof). Real session protocol
  (Guacamole / xterm framing, lifecycle, control) and session-lifecycle REST +
  `oapi-codegen` slice are **deferred to the session feature ticket** — WebSocket
  does not model cleanly in OpenAPI, so plain chi is used here (as audit-service
  did pre-T-014). `/healthz` is public; the channel is gated over the
  `agent-remote-session` / `human-agent` edges.
- **Verifier resolution** mirrors the other services: `AIREOS_PASETO_KEY`
  (audience `session-broker`) → real PASETO verifier; else `BROKER_DEV_INSECURE_AUTH=1`;
  else fail loud.

## Files in scope

- **Create:**
  - `services/session-broker/go.mod`
  - `services/session-broker/main.go` — `h2c`-wrapped stdlib server + verifier.
  - `services/session-broker/internal/server/server.go` — `/healthz` + the gated
    `/v1/sessions/{id}/channel` WebSocket echo.
  - `services/session-broker/internal/server/server_test.go` — WS echo (gated),
    WS-without-bearer 401, HTTP/2 healthz.
  - `docs/tickets/T-006-session-broker-skeleton.md` — this ticket.
- **Modify:**
  - `services/session-broker/README.md` — replace the placeholder.
  - `go.work` — `use ./services/session-broker`.
  - `.github/workflows/ci.yml` — add session-broker to the go job.
  - `Taskfile.yml` — add session-broker to `test`/`build`; fix `test` to use
    explicit per-module patterns (a bare `./packages/go/...` does not resolve
    from the workspace root) and emit all three service binaries from `build`.
  - `docs/tickets/README.md` — mark T-006 complete.

## Acceptance criteria

- [ ] **Passing fixture**: a capability-bearing client opens
      `/v1/sessions/{id}/channel` and the broker echoes a message
      (`TestWebSocketEchoGated`). **Violating fixture**: no bearer → the upgrade
      is rejected with 401, no WebSocket (`TestWebSocketRequiresCapability`).
- [ ] `/healthz` is served over HTTP/2 — `resp.ProtoMajor == 2`
      (`TestHTTP2Healthz`), satisfying ADR-0043 §Verification item 5.
- [ ] The channel is gated by the shared `RequireCapability` middleware over the
      agent-remote-session edge; no non-stdlib HTTP router other than chi/v5.
- [ ] `go build` / `go vet` clean across all modules incl. session-broker.
- [ ] OpenAPI / schemas — **N/A** (no REST paths; WebSocket transport skeleton).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — done.
- [ ] Verification command runs clean (see below).

## Out of scope

- Session-lifecycle REST (`/v1/sessions` create/list/terminate) + `oapi-codegen`
  slice + the Session schema wiring — the session feature ticket.
- The remote-session wire protocol (Guacamole `guacd`, xterm framing), recording,
  and control-channel semantics — feature tickets per deep-dive 09.
- TLS termination / cert provisioning — devops; h2c covers dev/CI.

## Diff budget

≤12 files / ≤500 LoC. Actual: 5 created + 5 modified = 10 files, ≈330 LoC.

## ADRs required

None new. Closes ADR-0043 §Verification item 5 and settles its deferred
WebSocket-library choice. Consumes ADR-0046 (capability verifier).

## Verification

Verified in a `golang:1.23` container:

```
go build ./services/session-broker/...     # clean
go vet   ./services/session-broker/...      # clean
go test  ./services/session-broker/...      # WS echo + 401 + HTTP/2 pass
# full sweep:
go build/vet/test across all packages/go/* + all services/*   # clean
```

All **pass**: `TestWebSocketEchoGated`, `TestWebSocketRequiresCapability` (401),
`TestHTTP2Healthz` (ProtoMajor 2), and the all-modules sweep.
