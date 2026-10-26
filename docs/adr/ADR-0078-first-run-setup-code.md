# ADR-0078: The first operator is created in the browser, gated by a one-time setup code that only root on the server can print

## Status

**Proposed** — 2026-10-26 (v0.17.0 "Install & First-Run Experience", ticket T-169). Commander seat.
**Decider: CEO.**

**Refines ADR-0046** (operator authentication at P0) — *where* the single bootstrap operator's
credentials are stored and *how* they are first set. ADR-0046's single-account model, argon2id hashing,
"no self-service signup", "no password reset UI" and its Zitadel adoption triggers are **unchanged**.
**Follows the one-time-secret pattern of ADR-0068 D6** (pairing). Supersedes nothing.

---

## Context

**The goal.** v0.17.0's ratified shape is *one command on the server, then everything else in the
browser* (`docs/releases/v0.17.0/v0.17.0-release-criteria.md` §1, FRX-1/2/3, SEC-64/65/67). Today the
operator's username and password are typed at the installer's terminal prompts.

**How the operator exists today.**
- `install.sh` prompts for a username and a password, hashes the password with `authctl hash` (argon2id),
  and writes both into `/etc/aireos/aireos.env` as `AIREOS_BOOTSTRAP_OPERATOR_USER` and
  `AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH` [read: `infra/offline/install.sh:398-405`, `:441-442`]. The
  unattended path supplies the same values by environment variable [read: `infra/offline/install.sh:21-22`].
- `services/api` loads the operator **only from that environment, once, at start**
  [read: `services/api/internal/account/account.go:31-40`]. If it is absent, login is disabled with a
  log line, and api still starts [read: `services/api/main.go:304-308`].
- There is **no operator table** [read: `services/api/internal/account/account.go:1-4` — "Credentials
  come from the environment"].

**ADR-0046 constrains the answer.** It decides "a single bootstrap operator account … provisioned by
`install.sh`. No self-service signup. No password reset UI" [read: `docs/adr/ADR-0046.md:55`]. It names
*self-service signup* as a trigger for adopting Zitadel [read: `docs/adr/ADR-0046.md:196`]. A gate
rejects any code path named like signup [read: `tools/forbidden-features-check/main.go:47`]. **A browser
form that any visitor can use to create the operator would be self-service signup.** This ADR must
therefore keep provisioning **anchored to whoever has root on the server**, and only move the *typing*
into the browser.

**The precedent.** ADR-0068 D6 already ships a one-time secret in this product. A 32-byte secret is
stored only as its hash, is single-use, is consumed in the same transaction that grants access, has
capped failed attempts, and is audited by operator-initiated events only
[read: `docs/adr/ADR-0068-paired-operator-sessions.md`, §D6].

**The exposure this creates.** Between `setup.sh` finishing and the operator completing setup, the
cockpit is reachable on the LAN with **no operator configured**. Whoever completes setup first owns the
installation. The only acceptable gate on that window is a secret that **never left the server**.

---

## Decision

**When `setup.sh` runs without operator credentials, api starts in *setup mode*. In setup mode it
creates the first and only operator through the browser, and only for a client that presents a
one-time setup code which `setup.sh` printed on the server's terminal.**

1. **The code.**
   - `setup.sh` (root) obtains it from a new `api setup-code issue` subcommand, which writes **only
     its hash** to the database. The code is shown **once**, on that terminal.
   - Format: **20 characters of Crockford base32 in four groups of five** — about 100 bits, case-
     insensitive, no ambiguous characters. Easy to type, impossible to guess within the lockout.
   - **Valid for 60 minutes.** **Single-use**: consumed in the same transaction that creates the
     operator.
   - **Five wrong attempts invalidate it.** After that a new one must be issued on the server.
   - `sudo ./setup.sh --new-setup-code` issues a fresh code and invalidates the previous one. It works
     only while no operator exists.
2. **Setup mode is total.** While no operator exists, every API route except the setup status and setup
   completion routes answers **`503 setup required`**, and the SPA routes to its setup page. **No
   governed action, resource, run or audit read is possible before an operator exists.**
3. **Completion.**
   - The browser posts the code, a username and a password **in the request body**, never the URL.
   - api verifies the code's hash, expiry and attempt count, hashes the password with argon2id
     (minimum length **12**), writes the operator, and consumes the code — **all in one
     transaction**.
   - It then leaves setup mode **in-process**, with no restart, and sets the normal session cookie.
4. **Where the operator lives.**
   - A new api-owned table holds **at most one** operator row, enforced by a database constraint, not
     by code.
   - **An operator in the environment always wins.** If `AIREOS_BOOTSTRAP_OPERATOR_*` is set, as it is
     on the unattended path (INS-6) and on every existing install (INS-8), api uses it and setup mode
     is **never entered**.
   - There is still exactly one operator; ADR-0046's model holds.
5. **After setup, the setup routes are gone.** They answer exactly as an unknown route does, including to
   a client that saved the old page (SEC-65). There is no "add another operator" path; that remains
   ADR-0046's Zitadel trigger.
6. **The code never leaves the server except in the operator's head.**
   - It never appears in the audit chain, an HTTP access log, api's own logs, or the shell history
     `setup.sh` leaves behind (SEC-67).
   - Audit events are **`setup.code.issued`**, **`setup.code.locked`** (lockout) and
     **`setup.completed`** (actor `operator:<username>`, client address). None carries the code; no
     expiry event, per ADR-0068 D6's reasoning.
7. **The certificate can be checked before the code is typed.**
   - `setup.sh` prints the SHA-256 fingerprint of the cockpit's local certificate authority next to
     the code.
   - That lets an operator who clicks through the browser's self-signed warning confirm they are
     talking to *this* server, and not to something on the LAN in between, **before** sending the
     code and a password.

---

## Alternatives considered

1. **Keep creating the operator at the terminal (status quo).** Secure, and it **remains available**
   through the environment variables. As the *default*, it is exactly what the release exists to remove:
   a password typed at a prompt with no line editing and no way to see it. Rejected as the default.
2. **The first browser visitor becomes the operator.** This is self-service signup, forbidden by
   ADR-0046. On a LAN it is also a race any device can win. Rejected outright.
3. **The installer prints an initial password; the first login must change it.** Nearly equivalent in
   security, and used by several self-hosted products. Rejected because a valid login credential
   would exist, and could be used, before the operator ever chose one. It also needs a
   forced-change-on-login path, which is a password-reset-adjacent feature ADR-0046 forbids. The setup
   code is never a login credential.
4. **api writes the operator into `/etc/aireos/aireos.env`.** That has a web-facing process rewriting
   its own configuration file, and it needs a restart to take effect. Rejected; the database is where
   api already keeps its state.
5. **A QR code or link fragment, as in pairing (ADR-0068 D6).** Pairing moves a secret between *two
   devices the operator holds*. At setup there is one terminal and one browser, possibly on different
   machines, so a typed code is simpler and loses nothing.

---

## Consequences

**Gained:**
- No password is typed at a terminal prompt.
- The operator chooses their username and password in a normal form, with a strength rule the terminal
  never had.
- **Provisioning is still anchored to root on the server.** Nobody without terminal access to the host
  can become the operator.

**Costs, accepted deliberately:**
- ⚠ **An unauthenticated window exists on the LAN** from `setup.sh` until setup completes, up to 60
  minutes per code. It is gated by a ~100-bit code with a 5-attempt lockout. The window is **inside**
  this product's threat model only because the code never crosses the network until the operator types
  it, and decision 7 lets them check the certificate first.
- New surface: one api table, one `api` CLI subcommand, two setup routes in the OpenAPI fragment. The
  surface freeze is at v1.0.0 final, so adding routes now is in-policy. The SPA also gets a setup page.
- **The code paths must not be named "signup"**: `forbidden-features-check` would correctly reject them.
  Names use **`setup`**.
- **A lost operator password is still unrecoverable from the browser.** There is no reset UI
  (ADR-0046, unchanged). Recovery stays a root action on the server: re-run the installer with
  `AIREOS_BOOTSTRAP_OPERATOR_*`, which overrides per decision 4. The release notes must say so.
- **Two storage locations for one concept** (environment and table), with a stated precedence. Accepted
  so that no existing install and no unattended run changes behaviour.

---

## How we would know this was wrong

- **HUM-1 testers cannot find or type the code.** Then the code format, or where `setup.sh` prints it,
  is wrong. That is not a reason to fall back to alternative 2.
- **`setup.code.locked` appears on real installs that nobody attacked.** Then five attempts is too few
  for honest typing; raise the limit, never drop the lockout.
- **Operators need a second account.** Then that is ADR-0046's Zitadel trigger firing. It is never a
  reason to keep setup mode open.
- **A setup route answers anything but "unknown route" after setup** — SEC-65 fails. Then decision 5 is
  not implemented, and the release does not ship.
