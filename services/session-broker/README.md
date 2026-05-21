# session-broker

Brokers live remote sessions (P0). stdlib `http.Server` with HTTP/2 (TLS in
prod; cleartext dev/CI via `h2c`), WebSocket via `github.com/coder/websocket`
(the maintained successor to the `nhooyr.io/websocket` named in ADR-0043), and
the shared capability-edge middleware (ADR-0043). Module:
`aireos.local/services/session-broker`.

## Surface (T-006 skeleton)

- `GET /healthz` — readiness (public; served over HTTP/2).
- `GET /v1/sessions/{id}/channel` — WebSocket channel, gated by
  `RequireCapability` over the `agent-remote-session` / `human-agent` edges.
  Skeleton transport: echoes messages. Real session protocol (Guacamole / xterm
  framing, lifecycle, control) lands with the session feature ticket.

Session-lifecycle REST paths + `oapi-codegen` slice are deferred — WebSocket does
not model cleanly in OpenAPI, and the skeleton's purpose is the transport.

## Run

```
BROKER_DEV_INSECURE_AUTH=1 go run ./services/session-broker      # :8082
# or with a real key:
AIREOS_PASETO_KEY=<hex> go run ./services/session-broker
```
