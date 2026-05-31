---
name: T-019a Ticket
about: SSH end-to-end vertical slice (d) — per-input-line audit conformance
title: "T-019a: terminal.input.line per-input-line audit (ADR-0048 §6 conformance)"
labels: ["ticket"]
---

## Ticket ID and link

T-019a — first half of the SSH end-to-end vertical slice (d) per ADR-0048
§Hand-off. No B-NNN counterpart. Depends on T-017a (outbox) + T-017b (SSH pipe).
Sibling: **T-019b** (cast persistence layout + retention/gc).

## Goal

Bring the broker's audit emission into conformance with **ADR-0048 §6**, which
mandates a per-Enter `terminal.input.line` chain event (the operator-attributable
atom). T-017b shipped lifecycle-only emission (`session.open`/`session.close`); a
consult re-litigated §6 without recording a deviation ADR — this ticket closes
that gap by emitting per-line events.

## Priority

P0 — v0.1.0 wedge; audit-pillar conformance with an Accepted ADR. No new ADR
(this aligns the implementation *to* ADR-0048 §6).

## Files in scope

- Modify: `services/session-broker/internal/sshpty/sshpty.go` (`lineBuffer` +
  `Pipe` `onInputLine` callback)
- Modify: `services/session-broker/internal/sshpty/sshpty_test.go` (line-buffer
  scenarios)
- Modify: `services/session-broker/internal/store/store.go` (`RecordInputLine`)
- Modify: `services/session-broker/internal/store/store_integration_test.go`
- Modify: `services/session-broker/internal/server/server.go` (wire the
  per-line closure; `SessionStore.RecordInputLine`)
- Modify: `services/session-broker/internal/server/server_test.go` (fake)
- Create: `docs/tickets/T-019a-per-input-line-audit.md` (this file)
- Modify: `docs/tickets/README.md`

8 files. Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (consult, 2026-05-30)

- **Conform, don't deviate (approved).** ADR-0048 §6 is explicit (per-line is
  "the correct atom," the volume worry is "misframed," and the slice "must NOT
  normalize backspaces out of the chain"). Rather than write a deviation ADR, the
  broker now emits `terminal.input.line` per Enter.
- **Hash in the chain, bytes in the cast (approved).** The audit `Ingest`
  contract is deliberately content-free (`{timestamp, actor, edge, target,
  outcome?, evidencePacketId?, idempotencyKey}`). The event carries
  `evidencePacketId = sha256:<line-bytes>`; the raw bytes stay in the
  access-controlled cast (already recorded as `i` events). This gives real-time
  per-line tamper-evidence (the §6 integrity goal), needs **no** audit-service
  schema change, and — decisively — does **not** freeze typed secrets (a password
  at a prompt) into the immutable chain. Content is recoverable by hashing the
  cast's line back.
- **Raw bytes, not normalized (§6).** `lineBuffer` splits on `\r` and emits the
  accumulated bytes verbatim — `helo\bllo` stays `helo\bllo` (intent on the
  wire). An empty line (Enter on an empty buffer) emits nothing. Lines reassemble
  across WS frames.
- **Off the keystroke path.** `onInputLine` fires once per Enter (≈30-100/session-
  hour, §6), so the in-line outbox write (a local Postgres tx) never touches the
  per-character echo latency.

## Acceptance criteria

- [x] `lineBuffer` yields raw, Enter-delimited lines; preserves backspace; drops
      empty lines; reassembles across frames (unit tests mirror the spike's
      scenarios).
- [x] The broker emits one `terminal.input.line` per Enter via the outbox, with
      `evidencePacketId = sha256:<line>` and `edge = agent-remote-session`.
- [x] The chain payload carries **only** the hash — never the raw line bytes
      (integration test asserts no `line` content in the payload).
- [x] `go vet`/`gofmt`/`golangci-lint` (T-011) clean.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Normalized backspace — violating:** stripping `\b` before emit loses the
  operator's wire intent. **Passing:** raw `helo\bllo` line bytes hashed.
- **Secrets in chain — violating:** putting raw line bytes in the chain freezes
  typed passwords into the immutable log. **Passing:** only `sha256:<line>` in
  the chain; bytes in the access-controlled cast.
- **Per-keystroke flood — violating:** emitting per byte blows chain volume.
  **Passing:** one event per Enter.

## Out of scope

- Cast-file persistence layout, retention, `casts gc` → **T-019b**.
- Extending the audit-service ingest schema to carry content (explicitly
  rejected — privacy).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 8 files, within budget.

## ADRs required

- None new. Conforms the implementation to ADR-0048 §6.

## Verification

```sh
# build + vet + gofmt + unit tests + lint (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 bash -c '
  test -z "$(gofmt -l services/session-broker)" &&
  (cd services/session-broker && go build ./... && go vet ./...) &&
  go test ./services/session-broker/internal/sshpty/... ./services/session-broker/internal/server/... &&
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./services/session-broker/...'

# store integration (MVP Postgres on docker-compose_default)
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e BROKER_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 go test -tags integration ./services/session-broker/internal/store/...
```

Expected: gofmt/build/vet clean; line-buffer + handler tests pass; integration
asserts `terminal.input.line` carries the SHA only (no raw bytes); lint clean.
(Confirmed 2026-05-30 on Docker / golang:1.23.)
