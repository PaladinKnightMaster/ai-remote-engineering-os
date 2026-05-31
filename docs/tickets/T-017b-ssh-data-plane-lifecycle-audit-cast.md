---
name: T-017b Ticket
about: SSH end-to-end vertical slice (b) — SSH data plane + lifecycle audit + cast writer
title: "T-017b: session-broker SSH-over-WS data plane + lifecycle audit + cast writer"
labels: ["ticket"]
---

## Ticket ID and link

T-017b — second half of the SSH end-to-end vertical slice (b) per ADR-0048
§Hand-off. No B-NNN counterpart. Depends on **T-017a** (the outbox + relay it
emits through). Productionizes the spike (`docs/spike/xterm-ssh-pty-findings.md`).

## Goal

Turn the broker's echo skeleton into the real data plane: dial the SSH target
with a pinned host key + the decrypted credential, allocate a PTY, pipe it to the
WebSocket while recording a byte-safe asciinema cast, enforce an idle timeout and
PTY resize, and emit `session.open`/`session.close` evidence through the T-017a
outbox — with the cast SHA256 anchored in `session.close`.

## Priority

P0 — v0.1.0 wedge (ADR-0048 data plane + §SEC-11 cast anchoring). ADR Accepted;
no new ADR required.

## Files in scope

- Create: `services/session-broker/internal/cast/cast.go` (byte-safe v2 writer)
- Create: `services/session-broker/internal/cast/cast_test.go`
- Modify: `services/session-broker/internal/store/store.go` (Session/Target,
  LoadSession, LoadTarget+decrypt, Activate+emit, Close+emit)
- Create: `services/session-broker/internal/store/store_integration_test.go`
- Create: `services/session-broker/internal/sshpty/sshpty.go` (dial+pin+PTY+pipe)
- Create: `services/session-broker/internal/sshpty/sshpty_test.go` (host-key pin)
- Modify: `services/session-broker/internal/server/server.go` (channel handler)
- Modify: `services/session-broker/internal/server/server_test.go` (gate tests)
- Modify: `services/session-broker/main.go` (wire store + secret key + cast dir + guard)
- Modify: `services/session-broker/go.mod` (`golang.org/x/crypto` direct)  *(mechanical)*
- Create: `docs/tickets/T-017b-ssh-data-plane-lifecycle-audit-cast.md` (this file)
- Modify: `docs/tickets/README.md` (T-017b entry)

12 files; 1 mechanical. Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (war-room consult, 2026-05-30)

- **Audit model: lifecycle-chain + cast-anchored (approved).** Only
  `session.open` and `session.close` enter the chain (via the outbox); per-line
  detail lives in the cast file, made tamper-evident by the cast SHA256 the
  `session.close` event anchors (ADR-0048 §SEC-11, `evidencePacketId =
  sha256:<hex>`). Avoids the per-keystroke chain-volume blowup ADR-0045 warns of.
- **Host-key pinning from `resources.labels['ssh_host_key']` (approved).** Pinned
  per target via `ssh.FixedHostKey`, replacing the spike's
  `InsecureIgnoreHostKey`. Fail-closed: `LoadTarget` errors if the label or the
  decrypted key is absent. No schema change (label-based, not a new column).
- **Byte-safe cast encoder.** PTY bytes never go through `string(...)`/`json.Marshal`
  (the spike's HIGH finding: silent UTF-8 corruption to U+FFFD). `cast.go` builds
  the JSON string literal byte-by-byte, escaping control + high bytes as `\u00XX`.
- **In-tx evidence.** `Activate`/`Close` enqueue the lifecycle event inside the
  same transaction as the status transition (ADR-0045, no dual-write). This is
  the producer-write half that completes ADR-0045 §Verification item 2/3.
- **Secret decrypt mirrored, not imported.** `services/api/internal/secret` is
  api's `internal/`; the broker re-implements the one `pgp_sym_decrypt` query +
  the `{private_key,passphrase}` Material shape. Documented debt — extract
  `packages/go/secret` when a third consumer appears.
- **Recording is fail-closed at open.** If the cast file cannot be created the
  channel is refused (an audit product should not broker an unrecorded session);
  `O_EXCL` also rejects a duplicate channel for the same session at v0.1.0.
- **Idle timeout 30m (ADR-0048 §8) → `closed_reason = idle`.** Reasons map to the
  `sessions_status`/`closed_reason` domain: `idle | operator | broker_crash`.
- **WS framing:** binary frames = raw PTY bytes; text frames = JSON control
  (`{"type":"resize","cols":C,"rows":R}` → `ssh.Session.WindowChange`). The
  matching SPA client is T-018.

## Acceptance criteria

- [x] Cast writer is byte-safe (control + high bytes → `\u00XX`, output is pure
      ASCII, no U+FFFD), produces replayable v2 JSONL, and Close's streaming
      SHA256 equals `sha256(file)`. `O_EXCL` refuses an existing file.
- [x] `LoadTarget` decrypts the credential (`pgp_sym_decrypt`) and pins
      `ssh_host_key`; a wrong key surfaces as an error, never silent garbage;
      missing labels → `ErrTargetIncomplete`.
- [x] `Activate`/`Close` transition session state and emit exactly
      `session.open` then `session.close` (cast SHA anchored) in-tx; verified
      against real Postgres.
- [x] `ssh.FixedHostKey` accepts the correct pinned host key and rejects a
      different one (in-process SSH server test).
- [x] Channel handler enforces operator-scope (foreign operator → 404), session
      state (not open → 409), secret config (→ 503), target availability (→ 424),
      and capability (no bearer → 401), all before the WS upgrade.
- [x] `golangci-lint` (T-011) clean; `go vet`/`gofmt` clean.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **UTF-8 corruption — violating:** `json.Marshal(string(ptyBytes))` turns a byte
  ≥0x80 into U+FFFD (lost). **Passing:** byte-by-byte `\u00XX` encoder; output is
  pure ASCII and round-trips.
- **Host-key trust — violating:** `ssh.InsecureIgnoreHostKey()` accepts any
  server (MITM). **Passing:** `ssh.FixedHostKey(pinned)` rejects a mismatch (test
  asserts both directions).
- **Cross-operator data plane — violating:** brokering a session whose
  `operator_id` ≠ the caller's capability subject. **Passing:** mismatch → 404,
  existence not disclosed (ADR-0048 §5).
- **Dual-write — violating:** transition status, then separately POST the event
  (lost on crash). **Passing:** status update + outbox enqueue in one tx.

## Out of scope

- Cast-file persistence layout, retention/cleanup policy, audit-chain linkage
  beyond the close-event SHA anchor → **T-019**.
- `apps/web` `<TerminalView>` + the resize/keystroke client protocol consumer →
  **T-018**.
- Extracting `packages/go/secret` to de-dup the decrypt (debt noted above).
- Full browser↔PTY end-to-end automated test (spike-proven; the production pipe
  reuses that validated shape). T-017b verifies pinning, lifecycle/audit, cast
  byte-safety, and the handler gates directly.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 12 files (1 mechanical), within budget.

## ADRs required

- None new. Implements ADR-0048 data plane + §SEC-11; completes ADR-0045
  §Verification item 2/3 (producer writes evidence in its state-change tx).

## Verification

```sh
# build + vet + gofmt + unit tests (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 bash -c '
  (cd services/session-broker && go mod tidy) && go work sync &&
  (cd services/session-broker && go build ./... && go vet ./...) &&
  test -z "$(gofmt -l services/session-broker)" &&
  go test ./services/session-broker/internal/cast/... ./services/session-broker/internal/sshpty/... ./services/session-broker/internal/server/...'

# store lifecycle integration (api + broker migrations applied; MVP Postgres)
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e API_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  -e BROKER_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  -e BROKER_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 bash -c '
    go run ./services/api migrate up && go run ./services/session-broker migrate up &&
    go test -tags integration ./services/session-broker/internal/store/...'

# lint (T-011 config)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./services/session-broker/...
```

Expected: build/vet/gofmt clean; cast byte-safety + replay tests pass; sshpty
host-key pin accept/reject passes; server gate tests pass (401/404/409/503/424);
store integration emits session.open + session.close (cast SHA anchored); lint
clean.
