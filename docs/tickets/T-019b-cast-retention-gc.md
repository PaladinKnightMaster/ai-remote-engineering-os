---
name: T-019b Ticket
about: SSH end-to-end vertical slice (d) — cast persistence layout + retention/gc
title: "T-019b: cast-file retention sweep (casts gc) + audited deletion"
labels: ["ticket"]
---

## Ticket ID and link

T-019b — second half of the SSH end-to-end vertical slice (d) per ADR-0048
§Hand-off. Depends on T-017b (cast writer + `sessions.cast_path/cast_sha256`).
**Closes the SSH vertical slice (a–d).** Sibling: T-019a (per-input-line audit).

## Goal

Give cast files a documented persistence layout and a retention/cleanup
mechanism (ADR-0048 §6; the Negative note's "rotation policy" + slice (d)
"cleanup policy"): an operator/cron `casts gc` sweep that deletes the cast files
of long-closed sessions and **audits each deletion** so forensic evidence is
never removed silently.

## Priority

P0 — v0.1.0 wedge (bounded disk growth; ADR-0048 §6). No new ADR.

## Files in scope

- Modify: `services/session-broker/internal/store/store.go`
  (`ListPrunableCasts`, `PruneCast`, `castGCActor`)
- Modify: `services/session-broker/internal/store/store_integration_test.go`
- Modify: `services/session-broker/main.go` (`casts gc` subcommand)
- Create: `docs/tickets/T-019b-cast-retention-gc.md` (this file)
- Modify: `docs/tickets/README.md`

5 files. Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions

- **Layout: flat `/var/lib/aireos/sessions/<id>.cast`** (ADR-0048 §6, already
  written by T-017b). Not sharded — one operator at v0.1.0 (~80 MB/8h-day, ADR
  Negative). The path is recorded in `sessions.cast_path` (NULL once pruned).
- **`casts gc [--older-than-days N]` subcommand** (default 90), mirroring the
  `migrate` / `outbox relay` operator-subcommand pattern (ADR-0044/0045). It only
  touches `closed`/`terminated` sessions whose `ended_at < cutoff` and whose
  `cast_path` is still set — never an `active`/`pending` session.
- **Audited deletion (`cast.pruned`).** For each pruned file `PruneCast` clears
  `cast_path` **and**, in the same transaction, enqueues a `cast.pruned` event
  (actor `system:cast-gc`, edge `service-service`, target `session:<id>`,
  `evidencePacketId = sha256:<original cast hash>`). Deleting forensic evidence
  is itself an audited, hash-anchored event — the running broker's relay delivers
  it. `cast_sha256` stays on the row as the durable record of what was deleted.
- **Idempotent + crash-safe order.** gc removes the file first (ignoring
  "not exist"), then `PruneCast`; a re-run skips already-pruned rows
  (`cast_path IS NULL`).

## Acceptance criteria

- [x] `ListPrunableCasts(cutoff)` returns closed/terminated sessions ended before
      the cutoff with a cast still present; never active/pending ones.
- [x] `PruneCast` clears `cast_path`, retains `cast_sha256`, and emits a
      `cast.pruned` event anchoring the original SHA (integration test).
- [x] A pruned session is not listed prunable again (idempotent).
- [x] `casts gc [--older-than-days N]` deletes files + prunes rows + reports a
      count; bad `N` (<0) errors.
- [x] `go vet`/`gofmt`/`golangci-lint` (T-011) clean.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Silent deletion — violating:** removing a cast with no audit record leaves a
  gap in the evidence trail. **Passing:** every deletion emits a hash-anchored
  `cast.pruned` event.
- **Pruning a live session — violating:** deleting an `active` session's cast
  mid-stream. **Passing:** `ListPrunableCasts` filters to `closed`/`terminated`
  with `ended_at < cutoff`.
- **Lost provenance — violating:** dropping `cast_sha256` on prune erases what
  was deleted. **Passing:** only `cast_path` is cleared; the SHA record remains.

## Out of scope

- A cron scheduler / systemd timer to invoke `casts gc` → operations runbook
  (release-prep ticket; ADR-0048 Negative defers the runbook).
- Cast-file UI replay → v0.2.0 evidence-replay ticket (ADR-0048 reserve).
- Off-host/object-storage archival of casts → future.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 5 files, within budget.

## ADRs required

- None new. Implements ADR-0048 §6 cast retention / slice (d) cleanup policy.

## Verification

```sh
# build + vet + gofmt + lint (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 bash -c '
  test -z "$(gofmt -l services/session-broker)" &&
  (cd services/session-broker && go build ./... && go vet ./...) &&
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./services/session-broker/...'

# store integration (MVP Postgres on docker-compose_default)
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e BROKER_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 go test -tags integration ./services/session-broker/internal/store/...
```

Expected: gofmt/build/vet clean; integration `TestCastRetentionPruneFlow` passes
(prunable listing, cast_path cleared, `cast.pruned` audited with the original
SHA, idempotent re-run); lint clean. (Confirmed 2026-05-30 on Docker /
golang:1.23.)
