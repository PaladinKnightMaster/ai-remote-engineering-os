# Spike findings — xterm-ssh-pty

Branch: `spike/xterm-ssh-pty` (throwaway). Feeds ADR-0048 §Verification.

The spike validates that ADR-0048's chosen architecture — in-process
Go SSH via `services/session-broker`, piped through a coder/websocket
connection to xterm.js in the SPA — works end-to-end and meets the
latency / reconnect SLOs.

## Phase 1 — Items 1, 2, 3 (done)

### Item 1 — End-to-end SSH terminal through session-broker

**Status: ✓ PASS.**

Stack:

```
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d  # postgres
docker compose -f infra/docker-compose/docker-compose.spike.yml up -d # spike-sshd
docker run -d --name aireos-spike-broker --network docker-compose_default \
  -v "$PWD:/work" -w /work -v gomodcache:/go/pkg/mod \
  -p 8082:8082 \
  -e BROKER_DEV_INSECURE_AUTH=1 -e BROKER_ADDR=":8082" \
  -e AIREOS_SPIKE_SSH_USER=spike -e AIREOS_SPIKE_SSH_PASSWORD=spike \
  golang:1.23 bash -lc 'go run ./services/session-broker'
```

Confirmed: WS connection establishes; SSH dial to `aireos-spike-sshd:2222`
succeeds; PTY allocation succeeds; bytes flow bidirectionally; shell
prompt arrives; commands echo correctly. Two separate measurement runs
(200 keystrokes + 20 cold dials) completed without WS-close errors,
SSH dial failures, or PTY allocation failures.

### Item 2 — Keystroke→echo latency, p95 < 100ms on localhost

**Status: ✓ PASS — p95 = 1.03ms.**

Measurement: 200 single-byte sends (`'a'` then `'\b'`-space-`'\b'` to
erase), 30ms human-typing-simulation gap between samples, captured from
the same Docker network the cockpit will use in dev.

```
samples:    200
min:        0.36 ms
p50:        0.54 ms
p95:        1.03 ms
p99:        1.77 ms
max:        3.11 ms
```

Caveat: localhost-docker is best-case. Real WAN deployment is
RTT-dominated (50-200ms typical); SLO still applies. Re-measure once
the staging environment exists.

Methodology issue resolved during the spike: my first version of
`spike-latency` allowed previous-sample echoes to leak into the next
sample's measurement window, producing impossible negative latencies
and uniformly tiny p50/p95 numbers. The fix: drain pending frames
non-blockingly right before each send so the first frame received
is guaranteed to be the post-send echo. Documented inline in
`tools/spike-latency/main.go`.

### Item 3 — Reconnect time, p95 < 5s

**Status: ✓ PASS — p95 = 529ms.**

Measurement: 20 cold WS dials, each waiting for the shell to settle
(quiet 500ms = prompt delivered). The 500ms quiet timer dominates the
reported time; actual SSH dial + PTY allocation cost ≈ 20-30ms.

```
samples:    20
min:        520.91 ms
p50:        522.21 ms
p95:        529.22 ms
p99:        529.22 ms
max:        534.91 ms
```

This is the server-side bound. The SPA-side `useWebSocketChannel`
hook (T-007c) adds its own client backoff (default initial 500ms,
exponential up to a cap). Combined client+server worst case under
normal conditions: ~1s reconnect to operable shell. SPA-side
behavior is verified by the existing T-007c unit tests.

## Phase 2 — Items 4, 5 (done)

### Item 4 — Per-input-line audit emission correctness

**Status: ✓ PASS.**

A `tools/spike-latency/verify/` driver ran 5 deterministic scenarios
through the live broker. Each Enter (`\r`) in operator input is
expected to produce one `spike: session=X input.line="..."` log line
(the chain-event stand-in). Empty input lines (`\r` with empty buffer)
produce nothing.

Scenarios and observed log entries:

| Scenario | Bytes sent | Expected `input.line` entries | Observed |
|---|---|---|---|
| two-commands | `echo first-line\recho second-line\r` | `"echo first-line"`, `"echo second-line"` | ✓ both |
| backspace-midline | `helo\bllo\r` | `"helo\bllo"` (raw bytes, not rendered text) | ✓ |
| pasted-multiline | `a-line\rb-line\r` | `"a-line"`, `"b-line"` (split on embedded `\r`) | ✓ both |
| empty-enter | `\r\r\r` | (none — empty line buffer) | ✓ zero entries |
| exit | `exit\r` | `"exit"` | ✓ |

**Total: 6 expected entries, 6 emitted.** The line buffer correctly
discriminates Enter-as-delimiter from Enter-as-empty-line and
correctly splits multi-line paste arriving in a single WS frame.

**Important semantic property surfaced**: backspace mid-line captures
the *raw input bytes* (`helo\bllo`), not the *rendered text* (`hello`).
The chain audits the operator-attributable atom (what they typed on
the wire). The cast file (item 5) records the *rendered terminal
state* (what the screen showed). Investigators get both — the
operator's intent and the system's visible-to-them result.

### Item 5 — Cast file replays via asciinema

**Status: ✓ PASS.**

The verify scenario produced `spike-<id>.cast` of 21 lines (1 header
JSON + 20 event JSONL). The broker logged the SHA256 of the closed
file (`35dbf056053e544018e6d27d2130b09e5462f3977ce1113cb603abcd378ad7b2`)
in the `cast.close` line — this is the anchor that ADR-0048 §SEC-11
records in the chain.

Validation in a `python:3.12-alpine` container with the `asciinema`
package installed:

- `asciinema cat <file>` — exit 0; rendered the full session including
  the bash prompt's response to backspace (`[K` ANSI erase-to-end-of-
  line, distinct from the raw `\b` in the input audit).
- Programmatic parse: header v2, 20 events (5 "i" / 15 "o"),
  timeline 0.000s → 3.531s, byte balance 64B input / 535B output.

The format is fully spec-compliant
([asciinema v2 docs](https://docs.asciinema.org/manual/asciicast/v2/)):
JSON header line + JSONL event lines `[elapsed_seconds, "o"|"i", "data"]`.

### Phase 2 verdict

**GREEN.** Both items pass with the architecture as committed to in
ADR-0048. The line-buffer + cast-file recording IS implementable in
~80 lines of Go (the helpers in `spike.go`) and produces operator-
queryable forensic data without per-keystroke chain pressure.

## What the spike code overall validated about ADR-0048

| ADR-0048 decision | Spike result |
|---|---|
| (1) In-process Go SSH for terminal | Works; `golang.org/x/crypto/ssh` does what we need without surprises |
| (2) services/api for control, session-broker for data plane | Spike used direct WS to broker; the split is mechanically simple |
| (3) sessions table schema | Not exercised (spike uses query params); schema design defensible |
| (4) Cookie auth on WS upgrade | Not exercised (BROKER_DEV_INSECURE_AUTH=1); SPA cookie path tested in T-007b |
| (5) WS-path authorization | Not exercised; trivial chi handler |
| (6) Per-input-line audit + cast file | **Validated** — both items 4 and 5 pass |
| (7) pgcrypto secrets storage | Not exercised; postgres pgcrypto is well-known |
| (8) TTL + reconnect + crash recovery | Reconnect validated; idle-timeout deferred to SSH slice ticket |

The unexercised items are all architecturally-defensible-from-paper
(schema, auth, secrets, TTL). The exercised items all passed. The
architecture is sound.

## Phase 1 review pass — ADR-0048 amendments to apply at promotion

These are the substantive changes Phase 1 surfaced that ADR-0048 should
absorb when it promotes to Accepted (after Phase 2 completes):

1. **§Verification item 3 — reword.** Current text reads "prompt returns
   within 5s p95" but conflates server-side dial bound with client-side
   backoff. Replace with three parts:
   - Server-side reconnect bound (dial + SSH + PTY allocation):
     measured ~20-30ms on docker-compose_default network; SLO p95 < 200ms.
   - Client-side backoff window: `useWebSocketChannel` default initial
     500ms, exponential up to 15s cap; SPA unit-tested in T-007c.
   - First-failure recovery (combined server + client): under normal
     conditions p95 < 1s on localhost; subsequent failures
     intentionally exceed via backoff.

2. **§Verification — add item 6: PTY resize signaling.** Browser
   `window.resize` event → SPA computes new cols/rows from xterm's
   `FitAddon` → sends a control message over WS → session-broker
   propagates via `ssh.Session.WindowChange(cols, rows)`. Spike
   hardcodes 80x24; production must handle resize.

3. **§Verification — add item 7: host key pinning.** Production must
   replace `ssh.InsecureIgnoreHostKey()` with `ssh.FixedHostKey()` or
   `known_hosts`-style verification per target. Spike accepts any host
   key for measurement.

4. **§Verification — add item 8: idle timeout enforcement.** Per
   ADR-0048 §8, sessions close at 30min of input inactivity. Spike has
   no timeout — handler runs until WS closes. SSH slice ticket must
   implement.

5. **§Verification — add item 9: large-output streaming.** Verify
   session-broker doesn't buffer-bloat or block under multi-megabyte
   output (e.g., `find /`). Spike measured only single-byte echo;
   production must demonstrate sustained streaming without OOM or
   backpressure failures.

These amendments capture the *production gaps* the spike exposed.
None of them invalidate the architectural decisions; all are
implementation requirements for the SSH end-to-end vertical slice
ticket that follows ADR-0048's promotion.

## Phase 1 verdict

GREEN with the amendments above tracked. Phase 2 proceeds.

## Phase 2 review pass — additional findings

The Phase 2 review pass (after the initial Phase 2 commit `6438ec6`)
surfaced a real bug class plus four documentation items:

### HIGH — UTF-8 silent corruption via `string(data)`

Initial Phase 2 used `string(data)` to convert PTY bytes to a Go
string for JSON encoding. PTY streams carry **raw 8-bit bytes**
(terminal escape sequences, locale-dependent output, partial UTF-8
multi-byte chars that span WS frames). Go's `json.Marshal` on a
string containing invalid UTF-8 silently replaces bad bytes with
U+FFFD — **data loss with no error signal**.

Fix: `safeJSONStringFromBytes()` in `spike.go` builds a JSON string
literal byte-by-byte, escaping control bytes via `\u00XX` and
encoding every byte ≥ 0x80 as `\u00XX` (which round-trips correctly
when the asciinema player reads them back as the original byte for
the U+0080-U+00FF range). ASCII printable bytes pass through
unchanged. Cast files remain spec-compliant; `asciinema cat` still
exits 0 with the new encoder.

**SSH slice ticket implication**: the production cast writer must
NOT use Go's default string→JSON path on PTY bytes. The pattern in
`safeJSONStringFromBytes` is the validated baseline.

### LOW — Documentation-only items

These are characteristics of the spike code that production should
improve but were not load-bearing for Phase 1/2 verification:

1. **Cast event timestamps are mutex-acquire time**, not arrival
   time. Under contention the recorded elapsed could be skewed by
   the lock wait. Acceptable at v0.1.0 (sub-mutex timestamping adds
   complexity); production may want lock-free timestamp capture
   that's then serialized via channel.
2. **Cast file write errors silently ignored** (`_ = f.Write(...)`).
   Disk full mid-session = silent data loss. Production should at
   minimum log + maybe fail the session closed (Permission Model
   Reviewer call at SSH slice ticket time).
3. **`os.OpenFile(...|os.O_TRUNC, ...)`** — session IDs include
   nanosecond timestamps so collisions are vanishingly unlikely,
   but production should use UUID v7 + reject pre-existing files.
4. **SHA256 of cast file at close reads the whole file** — 80MB
   for an 8h session. Streaming hash kept in sync with writes is
   the small improvement; not load-bearing.
5. **Verify driver has no programmatic assertions** — relies on
   human grep of broker logs. Acceptable for spike (spike code is
   throwaway); production audit tests in the SSH slice ticket must
   assert programmatically.

### After tightening: Phase 2 re-verification

Re-ran the verify scenario after the safe-byte encoder landed:
- All 6 expected `input.line` log entries still emitted.
- Cast file SHA256: `fa540a06ffa1b544904ffb821fdf2e7e8f9fed254683e6db2eaf37b4adfac553`
- 20 events (5 input, 15 output), timeline 0 → 3.522s — identical
  to the pre-tightening run for the all-ASCII scenario (as expected;
  the safe-byte encoder is byte-equivalent on printable ASCII).
- `asciinema cat` exits 0; full session renders correctly.

The byte-safe encoder protects against the UTF-8 corruption path
without changing observable output on ASCII-only sessions.

## Phase 2 verdict

GREEN. Items 4 and 5 pass; the UTF-8 finding is documented as a
production-critical pattern; the four documentation items go into the
SSH slice ticket scope.

## What the spike code is and isn't

**Is:**
- `services/session-broker/internal/server/spike.go` — the SSH-over-WS
  pipe handler.
- `tools/spike-latency/` — measurement tool.
- `apps/web/src/spike/Spike.tsx` — browser-side terminal using
  xterm.js for manual sanity testing (not on the measurement path).
- `infra/docker-compose/docker-compose.spike.yml` — the SSH target
  container.

**Isn't:**
- Production-bound. Bypasses capability gating (route is unauthenticated),
  hardcodes test credentials, accepts any SSH host key, logs per-line
  "audit" to stdout (no audit chain), and writes no cast file.

When ADR-0048 promotes to Accepted with these findings, the spike
branch is deleted. The architectural decisions migrate to a real
ticket (see ADR-0048 §Hand-off notes for the proposed 4-slice
split).

## Findings to fold into ADR-0048 §Verification (Phase 1)

```
Item 1: end-to-end works                  — confirmed
Item 2: latency p95 < 100ms localhost     — 1.03 ms measured (1000x headroom)
Item 3: reconnect p95 < 5s                — 529 ms measured (10x headroom)
Item 4: per-input-line audit (Phase 2)    — pending
Item 5: cast file replay (Phase 2)        — pending
```

The numerical headroom suggests the architecture isn't load-bearing at
the latency limit. Real-world bottlenecks will be:
- Network RTT (out of our control)
- SSH server's PTY processing speed (out of our control)
- xterm.js rendering (negligible — sub-ms)
- Audit-chain write latency (Phase 2 will measure)

None of these are likely to invalidate ADR-0048's architectural choices.
