# Canonical API

## API principles

- REST for durable resources.
- WebSocket for live sessions, terminals, events and agent runs.
- OpenAPI for external contract.
- Idempotency keys for create/start actions.
- Audit event emitted for each state-changing operation.
- All APIs require organization and policy context.

## Resource API

```http
GET    /v1/resources
POST   /v1/resources
GET    /v1/resources/{resourceId}
PATCH  /v1/resources/{resourceId}
DELETE /v1/resources/{resourceId}
```

## Session API

```http
POST   /v1/sessions
GET    /v1/sessions
GET    /v1/sessions/{sessionId}
POST   /v1/sessions/{sessionId}/join
POST   /v1/sessions/{sessionId}/leave
POST   /v1/sessions/{sessionId}/terminate
POST   /v1/sessions/{sessionId}/request-control
POST   /v1/sessions/{sessionId}/grant-control
POST   /v1/sessions/{sessionId}/revoke-control
```

## Ticket API

```http
GET    /v1/projects/{projectId}/tickets
POST   /v1/projects/{projectId}/tickets
GET    /v1/tickets/{ticketId}
PATCH  /v1/tickets/{ticketId}
POST   /v1/tickets/{ticketId}/transition
POST   /v1/tickets/{ticketId}/agent-runs
```

## Agent run API

```http
GET    /v1/agent-runs/{runId}
POST   /v1/agent-runs/{runId}/pause
POST   /v1/agent-runs/{runId}/resume
POST   /v1/agent-runs/{runId}/kill
GET    /v1/agent-runs/{runId}/artifacts
GET    /v1/agent-runs/{runId}/evidence
```

## Approval API

```http
GET    /v1/approvals
POST   /v1/approvals/{approvalId}/approve
POST   /v1/approvals/{approvalId}/deny
```

## Pairing API

```http
POST   /v1/pairing-sessions
GET    /v1/pairing-sessions/{pairingId}
POST   /v1/pairing-sessions/{pairingId}/confirm-phone
POST   /v1/pairing-sessions/{pairingId}/confirm-desktop
POST   /v1/pairing-sessions/{pairingId}/revoke
```

## WebSocket channels

```text
/ws/v1/sessions/{sessionId}/events
/ws/v1/sessions/{sessionId}/terminal/{terminalId}
/ws/v1/sessions/{sessionId}/control
/ws/v1/agent-runs/{runId}/events
/ws/v1/approvals
```
