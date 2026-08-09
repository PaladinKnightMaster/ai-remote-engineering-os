# v0.12.0 — verification runbook

The gate ladder for **v0.12.0 "Access & Onboarding"**, and the script for the one
gate a human runs.

`v0.12.0-release-criteria.md` §2 defines four gates. **GATE-1, GATE-2 and GATE-3
were executed at T-122a and are recorded here as evidence, not as instructions to
re-run.** A gate reads evidence that already exists; re-running a green gate to
produce a fresh copy of the same result is waste. **GATE-4 is the remaining one**,
and it is Step 4 below.

## Step 0 — sync the RC

```bash
git -C ai-remote-engineering-os log --oneline -1   # expect the T-122b closure commit
```

`main` and `dev` are the same commit. There is no remote; nothing is pushed.

## Step 1 — the automated gates (recorded at T-122a)

| Gate | Command | Result |
|---|---|---|
| **GATE-1** compose | `task e2e` | PASS — audit chain intact, **639 events**, WD-1…WD-11 |
| **GATE-1** installed | `task wd1` | PASS — audit chain intact, **238 events**, WD-1…WD-11 |
| **GATE-2** regression | both of the above | WD-1…WD-10 green on both stacks |
| **GATE-3** smoke | `task smoke` | **20/20** in real Chromium, incl. pairing, mobile approval, Button states |

Supporting checks, same run: bundle **116 MB / 42 MB** as
`aireos-offline-0.12.0.tar.gz` (OI-29 baseline held); OI-27's install diff is
exactly two env vars, their resolution helpers and the OI-22 assertion, with
`systemd/` and `Caddyfile` at **zero diff**; `task licenses`, `task lint`,
`task test` and `doc_check.py` all clean.

> **Before any `wd1` run, rebuild BOTH artifacts from HEAD** — the offline bundle
> (the standing T-033 lesson) **and** the staged postgres companion
> (`bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404`). The
> companion is built against the *floating* `ubuntu:24.04` tag, so it rots on its
> own with no code change: a stale one fails the install with
> `perl : Depends: perl-base (= …ubuntu0.2) but …ubuntu0.3 is to be installed`,
> an apt error with nothing to do with the code under test. Hit at the v0.12
> mid-ladder checkpoint.

## Step 2 — WD-11, the release's one new walkthrough demo

v0.12 mints exactly one new WD, because pairing is the only epic that adds
governed capability: a new session type, a new token tier, **the product's first
revocation primitive**, and a new consent-bearing act.

**Happy path** (`tests/e2e/wd11_test.go`, compose; `wd11_installed_test.go`, wd1):
desk requests pairing → `pairing.requested` → device redeems the fragment secret →
`pairing.redeemed` → **a held invocation is approved from the paired session** →
desk revokes → `pairing.revoked`. All three events chain; the approval carries
actor `operator:<id>#paired` (AUD-34).

**Negative leg** (`wd11_negatives_test.go`) — the reason WD-11 exists. A capability
claim is proven by what it *denies* (the shape SEC-21 set at WD-4):

- a **revoked** paired session → **401** (both stacks)
- an **expired** paired session → **401** (compose; see the exception below)
- **seven out-of-scope actions → 403**, live over HTTP with a genuinely redeemed
  cookie: launch a Run, issue a grant, revoke a grant, register a context source,
  remove a context source, mint an agent token, open a terminal session
- a **positive control** (`GET /v1/auth/me` → 200) proving the same live session's
  in-scope surface still works — without it, seven denials could equally mean the
  session was simply dead

> **Disclosed exception.** The **expired-session leg is proven on compose only.**
> On the installed stack it is a documented skip: `PairedTTL` is a hardcoded 8h
> constant with no env override, and the wd1 tester has no network path to the
> installed Postgres to seed `expires_at` into the past. Closing it would require a
> test-only TTL override or a debug force-expire route — **new product surface
> existing purely to satisfy a gate, one ticket before the E-06 freeze.** The
> liveness code is identical on both stacks and the revoked case is proven on both,
> so GATE-1 is met **with this exception named**, never as a bare "GATE-1 green".

## Step 3 — the fresh-install stack for GATE-4

GATE-4 runs on a **fresh install**, not a dev server. Every other gate this product
owns starts from a *provisioned* stack and `task smoke` runs against a seeded
backend — but a first-run experience is **by definition** about the un-seeded state
(ADR-0068 §D9). No new ceremony was minted for it: `wd1` already builds a stack
from scratch.

```bash
bash infra/offline/wd1/bringup-demo.sh
```

Same real `install.sh` under systemd-in-Docker as `run-wd1.sh`, but **without
teardown**, with Caddy published to **`https://localhost:8443`**. The script prints
the operator password on completion. The host image now bakes in `openssh-server`
(T-122a) so the loopback demo target has a real sshd — with **multiple host-key
types**, which is what T-123's fix requires the product to handle.

Browse `https://localhost:8443` in a **real host browser** (Chrome/Edge) and accept
the `tls internal` warning — Caddy serves a self-signed local CA.

> **⚠ Do not run `task smoke`, `task e2e`, or any other repo-bind-mounting
> container while this stack is up.** The hazard is symmetric and was learned the
> hard way at the v0.11 §human. Finish the container gates first — they are already
> done (Step 1) — then bring this up.

## Step 4 — GATE-4, the CEO §human walkthrough

Four behaviours, from `v0.12.0-release-criteria.md` §2. **Follow
`docs/devops/quickstart-first-governed-action.md` for the first-run sequence** —
that document was written to be followed verbatim by a human, and this walkthrough
is what verifies it. It is deliberately not duplicated here; duplication guarantees
drift.

### 4a. First run from an un-seeded stack

Log in. The **first-run checklist** appears (desk sessions only — a paired session
never sees it). Follow the quickstart: run
`setup-loopback-demo.sh generate --account <name>` on the host, create the demo
target in the cockpit from the values it prints, then launch a **one-step Run**
against it and watch it reach a completed governed action through the real
policy → connector → broker chain. The checklist items flip **on their own** —
they are derived from live state, not from a stored flag.

> **Known limitation, worth knowing before you click.** If you open a *terminal
> session* to the demo target, you will not be able to remove it afterwards:
> `sessions.resource_id` is a hard foreign key with no `ON DELETE`, so even a
> closed session blocks deletion and the remove action returns a 409. The demo path
> is a **Run**, not a session, so following the quickstart leaves the target
> removable. Recorded as a v1.x migration candidate.

### 4b. The stack reports its deployment profile

The cockpit shows the active profile (`lan-only` on this install). It rides
`GET /v1/auth/me` as an additive field — no new route, and **no audit event**: a
static installation property is not a governed action (ADR-0068 §D2).

### 4c. Pairing — the release's centrepiece

Pairing ships **off** (`AIREOS_PAIRING_ENABLED=0`, SEC-41), so **enable it
deliberately** — the enable path is documented in
`docs/devops/offline-install-v3.md`, and a re-run of the installer preserves your
choice. With it off the routes answer **404**, not 403: a disabled capability must
not advertise its own existence.

Then: render the QR, **redeem it on a real phone** (or a second browser profile),
**approve a held invocation from the paired session**, and **revoke that session
from the desk** — and watch the phone lose access on its very next request. The
liveness check runs per request against the row, not at mint time.

The QR encodes `https://<site>/pair#<id>.<secret>`. The secret rides the **URL
fragment**, which browsers never transmit — it reaches no access log and no
`Referer`. The SPA reads it in JS, posts it deliberately, and strips it from the
address bar immediately.

### 4d. Buttons have hover and active states

Across the routes. This closes a defect present in **every route since v0.1.0** —
before v0.12 no button with an actual button appearance had any hover or active
state, because no `Button` appearance existed in the design system at all.

> The consent-dialog half of this behaviour **does not apply**: E-05b was cut by
> recorded CEO decision (VER-10). The six consent gates keep working on
> `window.confirm`, and the smoke suite already asserts their copy through
> Playwright's dialog handler.

## Step 5 — sign-off and tag (T-122c)

On sign-off: the artifact trio flips from RC to **released**, and the release is
tagged **locally** as `v0.12.0`. No remote; nothing is pushed. Evidence from the
§human walkthrough is captured on disk under `tests/smoke/test-results/` and is
**not** git-archived, per the v0.4.0 precedent.
