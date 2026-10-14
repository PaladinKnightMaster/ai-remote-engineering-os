# Quickstart — your first governed action

You have an installed, running cockpit ([install guide](offline-install-v3.md), steps 1–4).
This takes you to a completed, audited governed action: you will point the cockpit at the
server itself as a demo target, run one harmless command through it, and find the evidence.

**If a step does not match what you see, that is a defect in this document or in the product,
not in your environment** — stop and report it rather than improvising past it.

**Where to run things.** Commands below run **on the server**, in the directory where you
unpacked the application bundle in the install guide's step 3 — `aireos-offline-<version>`.
Open a shell on the server and go there first (`<version>` is the version number in the
bundle's file name):

```sh
cd aireos-offline-<version>
```

---

## 1. Log in

Open `https://<address>` — the address you gave the installer — and sign in with the operator
username and password you chose in the install guide's step 3. Expect the certificate warning
described there.

**What you should see:** the Dashboard, a header reading "Signed in as `<your operator
username>` on edge `human-agent`, deployment profile `lan-only`", and a **"Get started"**
panel with two unchecked items: **"Register a resource"** and **"Complete a governed
action"**. (A phone signed in through device pairing does not get this panel — use a normal
browser login.)

---

## 2. Generate the demo target (on the server)

The demo target is the server logging in to itself over SSH — a single server cannot conjure
a second machine, so the "remote" machine in this quickstart is the one the cockpit runs on.
This needs an SSH server listening on port 22 of this machine (Ubuntu Server normally has one).

First create a dedicated local account for it, then have the setup script generate a key and
a restricted login for that account:

```sh
sudo useradd -m -s /bin/bash aireos-demo
sudo ./setup-loopback-demo.sh generate --account aireos-demo
```

(`--port` also exists, defaulting to `22`; you need it only if your SSH server listens
elsewhere. If `useradd` says the account already exists, carry on.)

**What you should see:** a few progress lines, ending with the four values you will paste
into the cockpit:

```
[loopback-demo] done — paste these into the cockpit's 'Create loopback demo target' action:
  host:         127.0.0.1
  port:         22
  username:     aireos-demo
  ssh host key: ssh-ed25519 AAAA…
```

The `ssh host key` is one long line starting with `ssh-ed25519` (or another key type your SSH
server offers — any is fine). Copy the username and that whole line. The new login is
restricted to running commands — no port or agent forwarding, no terminal — and its private key
stays on the server.

---

## 3. Create the demo target in the cockpit

Back in the browser, the Dashboard's "Get started" panel shows the same `generate` command and
a **"Create loopback demo target"** button. Click it. A small form opens that shows `host` and `port` as
fixed values (`127.0.0.1` and `22`). Paste:

- **username** — `aireos-demo`
- **ssh host key** — the whole `ssh-…` line the script printed

Click **Create**.

**What you should see:** the first item flips to **"✓ Register a resource"**, and the panel now
shows the new resource's id ("Demo target resource id: …") and the exact `bind` command to run
on the server. That is the next step.

---

## 4. Bind the credential (on the server)

Copy the command from the panel (click it to select the whole line) and run it in the same
directory as before. It has this shape — use the one the panel shows, which carries your real
resource id:

```sh
sudo ./setup-loopback-demo.sh bind --resource-id <id-from-the-panel>
```

This hands the private key generated in step 2 to the connector, the one component allowed to
hold credentials; nothing else can read it back.

**What you should see:** the output ends with `[loopback-demo] bound — the demo resource is
ready to run a governed action against`. If it instead says the value "is not a resource id",
you ran the template above without replacing the placeholder.

---

## 5. Launch a one-step Run

In the cockpit, open **Runs** (the link in the panel, or `/runs`). Under "Compose a run":

1. Select **loopback-demo (ssh)** from the resource dropdown.
2. Type a harmless command, e.g. `whoami`, into the command field.
3. Click **Add step**, then **Review & launch**.
4. On the review screen, confirm the single step reads back exactly the command you typed,
   then click **Confirm launch**.

This resource was created **without** requiring approval, so the step runs unattended through
the real chain — policy, then connector, then the remote login — with nothing waiting for a
second operator.

**What you should see:** the new Run appears in the list below, its status moving from
`pending` or `running` to **`completed`** within a few seconds. Click the Run's id to expand
it: the step reads `1. loopback-demo` with its status, and a note says **"Command output is not
stored — the evidence of what ran is in the Audit timeline."** That is deliberate: the cockpit
records that the command ran, under whose identity and what the policy decided, but never what it
printed, so you will not see `whoami`'s output on any screen. (A launched Run's expanded step
shows the resource, not the command text; the command is on the review screen you just
confirmed and in the audit trail.)

Back on the Dashboard, the second item now reads **"✓ Complete a governed action"**, and the
panel says "All set — dismiss whenever you're ready."

---

## 6. See the events in the audit surface

Open **Audit** (`/audit`). It opens newest-first on its default view — no filter changes
needed. Among the events from your Run you should find **`run.launched`**, **`tool.result`** and
**`run.completed`**; a Run typically records others beside them, such as `tool.invoked`,
`policy.decision` (allow), `credential.leased`, `remote.exec` and `memory.written`.
`run.launched` records your clicking "Confirm launch"; `tool.result` and `run.completed` record
the step finishing. Every one shows your operator identity as the actor (`operator:` followed
by the username you chose), and together they are the permanent, hash-chained record of the
action.

---

## 7. Clean up

**In the cockpit**, on the Dashboard panel, click **"Remove the demo target"**. It should
succeed immediately (see the last limit below if it does not).

**On the server**, from the same directory, undo the SSH and credential side:

```sh
sudo ./setup-loopback-demo.sh --undo
```

This removes the login the script added to `aireos-demo` (matched by its own marker comment, so
nothing you added by hand is touched) and the stored credential, if one was bound. It is
safe to run again, or out of order. The `aireos-demo` account itself stays; remove it with
`sudo userdel -r aireos-demo` if you do not want it.

---

## Optional: enabling pairing

Device pairing (a phone approving from a QR-paired session) is **off by default**. How to
turn it on, and what a re-run of the installer keeps, is in the install guide's
[Pairing a phone](offline-install-v3.md#pairing-a-phone-optional).

---

## Honest limits — read this before you file a bug

**Only the `lan-only` deployment profile has been proven end to end.** This quickstart runs on
it. `vpn-first` and `dmz-first` are enforced at install time (the installer refuses either
without a routable, non-loopback site address) and are described in
[network topologies](../deep-dive/03-network-topologies.md#verification-depth--stated-honestly-oi-25),
but no VPN or DMZ gateway has been stood up against this product — do not read a clean
walkthrough on `lan-only` as evidence for the other two.

**An operator cannot see the output of a command.** Command output is not stored anywhere, by
design for now; the audit trail proves a command ran and under which decision, not what it
printed.

**A resource that has ever had a terminal session opened against it cannot be deleted.** The
database keeps a hard link from each session to its resource, so even a long-closed session
blocks the delete; the cockpit reports it as `409 Conflict` ("resource is in use") and it will
not go away short of a schema change. This walkthrough stays clean because it uses a **Run**,
never a terminal session. If you also open a terminal (`/sessions`) against `loopback-demo`,
step 7's removal will start failing with 409.
