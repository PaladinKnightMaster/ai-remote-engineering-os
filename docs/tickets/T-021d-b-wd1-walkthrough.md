---
name: T-021d-b Ticket
about: Offline install (4b/4) — WD-1 operator walkthrough through Caddy HTTPS + chain assertion
title: "T-021d-b: WD-1 wedge demo on the installed stack (Caddy HTTPS) — closes OI-3 (v0.1.0)"
labels: ["ticket", "devops", "test"]
---

## Ticket ID and link

T-021d-b — second half of the §OI capstone (T-021d split a/b). The G1 capstone:
the WD-1 wedge demo driven through Caddy HTTPS against the **installed** stack
(T-021d-a brought it up under real systemd). Closes **OI-3 / WD-1** except the
operator timeline *view* (a real gap surfaced here → **T-024**). Binding specs:
ADR-0051 (install) / ADR-0050 (WS) / ADR-0045 (chain).

## Goal

Drive the operator walkthrough on the production wire and prove the audit chain:

> login → resource picker (`GET /v1/resources`) → create session → broker WS over
> **wss through Caddy** → run a command + read its output over the live PTY → end
> → logout; then assert the chain is **intact** (`audit-service verify`) and carries
> `auth.login.success` + `opened` + `input.line` + `closed:operator` + `auth.logout`.

## Priority

P0 — release-gating (the wedge demo is the single load-bearing v0.1.0 criterion).

## Files in scope

- Create: `tests/e2e/harness_test.go` (`//go:build e2e || wd1`) — shared helpers, factored out of e2e_test.go (unchanged signatures) so both drivers reuse them.
- Modify: `tests/e2e/e2e_test.go` — slimmed to `TestSSHCockpitEndToEnd` (helpers moved to the harness).
- Create: `tests/e2e/wd1_test.go` (`//go:build wd1`) — `TestWedgeDemoInstalledStack` through Caddy HTTPS.
- Modify: `tests/e2e/cmd/seed/main.go` — keys-only mode (emit host-pubkey + client-key, skip the DB) for the installed-stack seed.
- Modify: `infra/offline/wd1/run-wd1.sh` — full WD-1 flow: keygen → test sshd → systemd host → real install → seed the ssh resource (psql + `api set-secret`) → run the driver → host-internal chain assert.
- Create: `docs/tickets/T-021d-b-wd1-walkthrough.md` (this) + Modify: `docs/tickets/README.md` (+ register T-024).

7 files, ~330 LoC, within budget.

## Design (per the approved consult)

- **API/WS through Caddy HTTPS**, not a full browser: the SPA wiring is unit-tested
  (T-007c/T-018b) and T-021d-a proved Caddy serves it; the load-bearing claim is
  function + audit chain. The driver trusts the `tls internal` cert via
  `InsecureSkipVerify` (so a normal client works) — and because it's real HTTPS,
  Secure cookies work normally (no T-020b plaintext-cookie workaround).
- **Caddy serves both `localhost` and the host name** (`AIREOS_SITE_ADDRESS="localhost
  aireos-wd1-host"`) so the in-container checks and the by-name driver both match.
- **Resource seed via psql + `api resources set-secret`** inside the host (the
  installed postgres is localhost-only); reuses the keys-only seed for the sshd.
- **Chain asserted host-internally** (`audit-service verify` + a `psql` outcome
  read) because audit-service binds to localhost and is not Caddy-routed.

## Real bugs surfaced + fixed (en route)

- **Operator password mismatch:** run-wd1 provisioned a different password than the
  shared harness const → login 401. Aligned to `operatorPass`.
- **Env-file `source` breakage:** `AIREOS_SITE_ADDRESS` (a space-separated multi-host
  value) and the argon2id hash (`$`) don't survive bash `source` (systemd's
  EnvironmentFile parser handles both). The harness now `grep`-extracts only the
  vars a CLI needs instead of sourcing the whole file.

## Acceptance criteria (verified)

- [x] `task wd1` runs the real install (T-021d-a) then the WD-1 walkthrough through
      Caddy HTTPS; `TestWedgeDemoInstalledStack` PASSes.
- [x] login over HTTPS, resource visible in `GET /v1/resources`, session created,
      command output round-trips over wss, end + logout all succeed.
- [x] `audit-service verify` → **chain intact: 5 events**; all of
      `auth.login.success` / `opened` / `input.line` / `closed:operator` /
      `auth.logout` present in chain order.
- [x] `assertWSRejectsWithoutAuth` holds over wss (ADR-0050 §2 guard, through Caddy).
- [x] both tagged builds (`-tags e2e`, `-tags wd1`) vet clean; gofmt clean.

### Violating vs. passing fixtures

- **Sourcing the env file — violating:** `command not found` on the space-containing
  site address. **Passing:** grep-extract the needed vars.
- **No-cookie WS over Caddy — violating(if it succeeded):** auth disabled.
  **Passing:** 401, asserted by `assertWSRejectsWithoutAuth`.

## Out of scope (surfaced gap)

- **T-024 — operator audit-timeline VIEW.** WD-1 step 8's "operator visits the audit
  timeline" is not reachable in the cockpit: Caddy has no route to `audit-service`,
  the operator cookie's audience wouldn't pass `audit-service` (same class as the
  ADR-0050 broker fix), and there's no SPA timeline page. The events are produced +
  chained + CLI-verifiable; only the in-UI *view* is missing. Release-gating
  follow-up. (Full-browser WD-1 also deferred.)

## ADRs required

- None new — conforms to ADR-0051/0050/0045.

## Verification

```sh
# artifacts first (T-021a/T-021c), then:
task wd1
```

Confirmed 2026-05-31 (Windows/WSL2 Docker): walkthrough PASS through Caddy HTTPS;
`audit chain intact: 5 event(s) verified`; all five WD-1 outcomes present.
