# Quickstart — from an installed stack to a completed governed action

This picks up where [`offline-install-v3.md`](offline-install-v3.md) ends — a running,
installed stack — and takes you to a completed, audited governed action, by hand. Every
command and every cockpit click below is checked against the shipped code.

**If any step does not match what you see, that is a defect in this document or in the
product, not in your environment** — stop and report it rather than improvising past it.
No automated test walks this document, so a person following it is the only check it gets.

---

## Prerequisite

A stack installed per [`docs/devops/offline-install-v3.md`](offline-install-v3.md),
including a chosen `AIREOS_DEPLOYMENT_PROFILE`. This quickstart uses `lan-only`
throughout — it is the only profile proven end-to-end by the project's installed-stack test (see
[Honest limits](#honest-limits-read-this-before-you-file-a-bug) below). You should be
able to reach `https://<AIREOS_SITE_ADDRESS>` in a browser and have the bootstrap
operator username/password from that install.

---

## 1. Log in

Open `https://<AIREOS_SITE_ADDRESS>` (the address you installed with; `localhost` for
a `lan-only` install with no `AIREOS_SITE_ADDRESS` set) and sign in with the bootstrap
operator credentials from the install step.

**What you should see:** the Dashboard, with a header reading "Signed in as
`<your operator username>` on edge `human-agent`, deployment profile `lan-only`"
(the profile name is `GET /v1/auth/me`'s additive field, OI-24) — and, above
"Operator actions", a **"Get started"** panel. That panel is the first-run checklist
(`apps/web/src/components/OnboardingChecklist.tsx`); it renders only for a desk
operator session, never for a paired phone session (`ADR-0068` §D5) — if you are
walking this quickstart from a paired session you will not see it, which is expected,
not a bug.

The checklist shows two unchecked items: **"Register a resource"** and **"Complete a
governed action"**.

---

## 2. Generate the loopback demo target (on the host)

The demo target is the installed host SSHing to itself — an air-gapped
single-host install cannot conjure a second machine, so the "remote" resource in this
quickstart is the same box the stack runs on.

**Where the script is.** `setup-loopback-demo.sh` ships at the top level of the bundle,
beside `install.sh`. If you unpacked the bundle at, say,
`/opt/aireos-offline-<version>/` on the host, the script is already there:

```sh
/opt/aireos-offline-<version>/setup-loopback-demo.sh generate --account aireos-demo
```

The commands below assume you copied or symlinked it to `/root/setup-loopback-demo.sh`
for brevity — adjust the path to wherever you unpacked the bundle.

**First, create a dedicated local account for the demo** (any name; the script only
requires an account that already exists with a usable home directory — this is a
standard OS step, not something the product's own scripts assert):

```sh
useradd -m -s /bin/bash aireos-demo
```

**Then, as root, on the host**, run the setup script's `generate` mode:

```sh
sudo /root/setup-loopback-demo.sh generate --account aireos-demo
```

(`--port` also exists, defaulting to `22` — you do not need it unless your sshd
listens elsewhere.)

**What you should see:** the script confirms sshd is running, generates a fresh
ed25519 keypair, installs a *restricted* `authorized_keys` entry for `aireos-demo`
(`restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty` — no PTY
is ever needed, because the governed exec path this demo exercises
(`services/mcp-gateway/internal/brokerexec/`) never allocates one), and prints exactly
four values to paste into the cockpit:

```
[loopback-demo] done — paste these into the cockpit's 'Create loopback demo target' action:
  host:         127.0.0.1
  port:         22
  username:     aireos-demo
  ssh host key: ssh-ed25519 AAAA... aireos-demo-host
```

Copy the `username` and the full `ssh host key` line (including the `ssh-ed25519 `
prefix) — you will paste both into the cockpit in the next step. The private key never
leaves the host.

Your sshd may offer several host-key types (the OpenSSH default); that is fine — no
restriction is needed.

---

## 3. Create the demo target in the cockpit

Back in the browser, on the Dashboard's "Get started" panel, click **"Create loopback
demo target"** (this button only appears while no resource exists yet — it is
`CreateLoopbackDemoTarget` inside `OnboardingChecklist.tsx`). A small form opens with
`host` and `port` already fixed to `127.0.0.1` / `22`. Paste:

- **username** — the account you created (`aireos-demo`)
- **ssh host key** — the full `ssh-ed25519 AAAA...` line the script printed

Click **Create**.

**What you should see:** the form closes, and the checklist's first item flips to
**"✓ Register a resource"** within a moment (the panel re-reads live state — it never
caches). This created the resource through the ordinary `POST /v1/resources` path
(`type: ssh, name: loopback-demo`) — nothing about this step is a special demo-only
code path.

---

## 4. Bind the credential (on the host)

Still on the host, as root, find the new resource's id — either from the cockpit
(hover/inspect the resource, or note it from the next step's Run compose dropdown) or
by reading it back from the script's own state:

```sh
sudo /root/setup-loopback-demo.sh bind --resource-id <the-resource-id>
```

This loads the private key generated in step 2 into connector-service's custody via
the existing `connector-service credentials set-secret` CLI — there is no second
credential path; the connector remains the sole decrypt authority (`ADR-0053`).

**What you should see:** `[loopback-demo] bound — the demo resource is ready to run a
governed action against`.

---

## 5. Launch a one-step Run

In the cockpit, open **Runs** (from the Dashboard, or `/runs`). Under "Compose a
run":

1. Select **loopback-demo (ssh)** from the resource dropdown.
2. Type a harmless command, e.g. `whoami`, into the command field.
3. Click **Add step**, then **Review & launch**.
4. On the review screen, confirm the single step reads back exactly the command you
   typed, then click **Confirm launch**.

This resource was created **without** `requires_approval`, so the step runs
unattended through the real chain — policy → connector → broker — with no approval to
park on (a flagged demo target would stall the quickstart on a second operator action).

**What you should see:** the new Run appears in the list below, its status moving
from `pending`/`running` to **`completed`** within a few seconds. Click the Run's id
to expand its step detail and confirm the command's output.

Back on the Dashboard, the checklist's second item now reads **"✓ Complete a governed
action"**, and the panel reports "All set — dismiss whenever you're ready."

---

## 6. See the events in the audit surface

Open **Audit** (`/audit`). It opens newest-first on the `governed-actions@1` preset by
default — no filter changes needed. You should see, near the top (newest first):
a `run.completed` event and a `tool.result` event (`services/mcp-gateway/internal/
store/store.go`'s `Finish` — it targets `resource:<the-demo-resource-id>`, not the
Run), both from the step you just ran, and a `run.launched` event (targeting
`run:<the-run-id>`) further down the page, from when you clicked "Confirm launch".
Every one of these carries your operator identity as actor (`operator:<your
username>`) — the chain is append-only and verifiable end to end.

---

## 7. Clean up

**In the cockpit**, back on the Dashboard's checklist, click **"Remove the demo
target"**. This calls the same governed `DELETE /v1/resources/{id}` route any resource
removal uses. It should succeed immediately — the demo path used a Run, not a
terminal session, so nothing blocks the delete (see the FK note below).

**On the host, as root**, undo the SSH and credential side:

```sh
sudo /root/setup-loopback-demo.sh --undo
```

This removes the `authorized_keys` entry (matched by its own marker comment, so it
never touches anything you or another tool added by hand) and, if a resource was
bound, unsets the stored connector credential. It is idempotent — safe to run again,
or run out of order, if something above did not go as expected.

---

## Optional: enabling pairing

Device pairing (a phone approving from a QR-paired session) ships **off by default**
(`AIREOS_PAIRING_ENABLED=0`). Enabling it, and what re-running the installer preserves
about that choice, is documented in
[`docs/devops/offline-install-v3.md`](offline-install-v3.md#install-target-host-offline-as-root)
under "Pairing / QR device pairing" — this quickstart does not restate it. In short: a
re-run of `install.sh` with the variable unset keeps whatever you last set it to; it
never silently reverts to disabled once you have turned it on.

---

## Honest limits — read this before you file a bug

**Only `lan-only` is proven end-to-end here.** This quickstart, and the installed-stack
test behind it, run on the `lan-only` deployment profile. `vpn-first` and `dmz-first`
are enforced at install time (the installer refuses either one without a routable,
non-loopback `AIREOS_SITE_ADDRESS`) and are documented in
[`docs/deep-dive/03-network-topologies.md`](../deep-dive/03-network-topologies.md#verification-depth-stated-honestly-oi-25),
but **no VPN or DMZ gateway has been stood up against this product** — do not read a
clean walkthrough on `lan-only` as evidence for the other two.

**A resource that has ever had a terminal session opened against it cannot be
deleted.** `sessions.resource_id` is a hard foreign key with no `ON DELETE` clause
(`services/api/migrations/0002`), so even a long-closed session still blocks the
physical row delete — the API surfaces this as a `409 Conflict` ("resource is in
use"), never a raw 500, but it will not go away short of a schema migration. The
walkthrough above stays clean specifically because it drives the demo resource through
a **Run**, never a session — if you additionally open a terminal (`/sessions`) against
`loopback-demo` to poke around, step 7's removal will start failing with 409, and the
fix is not obvious from the error alone. One sentence here beats a support ticket.
