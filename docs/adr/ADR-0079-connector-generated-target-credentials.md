# ADR-0079: Target SSH keys are generated inside connector-service; the host's SSH trust is touched only by the operator-run installer

## Status

**Accepted** — 2026-10-26 (CEO). Proposed 2026-10-26 (v0.17.0 "Install & First-Run Experience", ticket T-170). Commander seat.
**Decider: CEO.**

**Extends ADR-0053** (connector-service is the sole custody and decrypt authority for target
credentials) with a **generation** path and a **bind** path — custody stays single. **Conforms to the
v0.12.0 split of trust** recorded with ADR-0068 D8's loopback demo: no service ever writes the host's SSH
trust. Supersedes nothing.

---

## Context

**The goal.** v0.17.0 requires that a governed action against the loopback demo be reachable **with no
server command after `setup.sh`** (FRX-5), and that a target key's private half never appear in any API
response, log or audit event (FRX-4, SEC-66) [read: `docs/releases/v0.17.0/v0.17.0-release-criteria.md`
§4–§5].

**How a target gets a credential today.**
- connector-service accepts credentials **only from a CLI on the host**: `connector-service credentials
  set-secret <resource-id> --key-file <path>` [read: `services/connector-service/main.go:20`, `:244`].
- Its only HTTP route is the broker's **lease**, gated to `service-service` tokens
  [read: `services/connector-service/internal/server/server.go:39-42`].
- Credentials are keyed by `resource_id`, a soft reference to api's `resources` table
  [read: `services/connector-service/migrations/0001_credentials.up.sql:10`, `:23`].

**The loopback demo today needs two server commands around one browser step**
[read: `infra/offline/setup-loopback-demo.sh:6-35`]:
- `generate` runs `ssh-keygen` and **writes the private key to disk** under
  `/var/lib/aireos/loopback-demo/` [read: `infra/offline/setup-loopback-demo.sh:76-79`]. It installs the
  public half into the demo account's `authorized_keys` with
  `restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty`
  [read: `infra/offline/setup-loopback-demo.sh:93`], and prints the host key for the operator to paste.
- The operator creates the resource in the cockpit, through the governed `POST /v1/resources`.
- `bind` hands the private key to the connector.

**The split of trust this must keep.** The CEO ratified, for v0.12.0, that *"giving a service the ability
to write authorized_keys on its own host is a new privileged capability"*. The rule that came with it:
**the script — run by the operator, never by a service — does everything that touches the filesystem or
SSH trust; the cockpit does everything that is governed; this script never creates the resource row.**
[read: `infra/offline/setup-loopback-demo.sh:6-14`]. **A "one-click demo" that let a service write
`authorized_keys` would reverse that decision**; this ADR must not.

**ADR-0053 constrains who may see the private key**: after v0.2.0 *"no service other than
connector-service … can read a target secret"*, and the broker receives plaintext only to dial
[read: `docs/adr/ADR-0053-connector-service.md`, Decision 1 and 3].

---

## Decision

**connector-service generates target SSH key pairs itself and keeps the private half. The operator-run
`setup.sh` prepares the loopback demo's host-side SSH trust once, at install time, around a
connector-generated key. The cockpit then binds that key to a resource created through the normal
governed path.**

1. **Generation inside the connector.**
   - A new connector route, **`POST /v1/credentials/{resource_id}/generate`**, is gated like the lease
     to `service-service` tokens, so api calls it on behalf of an authenticated operator.
   - It creates an **ed25519** key pair, stores the private half encrypted exactly as `set-secret`
     does, and stores the **public half in plaintext** beside it.
   - It returns **only** the public key and its fingerprint.
   - If a credential already exists it **refuses**, unless the request says `rotate`. A rotation
     replaces the key and is audited.
2. **The cockpit shows only public material.**
   - api exposes the operator-facing action and a read of the public half, plus the exact restricted
     `authorized_keys` line, using the same option set as today, for the operator to install **on
     the target machine themselves**.
   - **No API response, log line or audit event contains the private half** (SEC-66, proven by
     searching for its bytes).
   - Audit: **`credential.generated`** and **`credential.rotated`**, each with the resource and
     public-key fingerprint.
3. **The loopback demo is prepared at install time, by the operator, opt-in.**
   - `setup.sh` asks **once** whether to set up the demo target; the unattended path uses an
     environment variable. If yes, it runs as root, as the operator's command, and does three things:
     - creates the local demo account;
     - asks the connector, **through its local CLI**, for a **pending** key labelled `loopback-demo`,
       and receives only the public half;
     - installs that public half into the account's `authorized_keys` with the same restrictions as
       today, and records the account name and this host's SSH host key as **non-secret metadata** on
       the pending record.
   - **The private key never exists on disk outside the connector's encrypted store.** That is an
     improvement on today's `/var/lib/aireos/loopback-demo/id_ed25519`.
4. **The cockpit binds; it never touches the host.**
   - "Create loopback demo target" needs **no paste**. api reads the pending record's non-secret
     metadata from the connector, creates the resource through the ordinary governed
     `POST /v1/resources` (so `resource.created` is chained, as ONB-4 requires), then asks the
     connector to **bind** the pending key to the new resource id.
   - A bind is a move inside the connector's own table. Audit: **`credential.bound`**.
   - No service writes `authorized_keys`, creates an account, or touches the filesystem: **the v0.12.0
     split of trust holds exactly.**
5. **Importing an operator's own key stays a server-side CLI action** (`set-secret`), and the browser
   **never** accepts a private key. Uploading one would put plaintext through api, which ADR-0053
   forbids.
6. **The script path stays** for operators who prefer it, or who declined the demo at setup (FRX-6). Its
   `generate` gains the same connector-side key generation, so it too stops writing a private key to
   disk.
7. **Undo stays an operator action on the server.** `setup.sh --remove-demo` removes the
   `authorized_keys` entry, the pending or bound credential, and, if the operator asks, the account.
   "Remove the demo target" in the cockpit keeps removing the resource, as today.

---

## Alternatives considered

1. **A service writes `authorized_keys`** (a privileged helper, a sudo rule, or setuid). It is the only
   way to get a demo with *no* install-time step. It reverses the CEO's v0.12.0 split of trust and adds
   a root-capable path reachable from a web session. Rejected.
2. **The browser accepts a pasted or uploaded private key.** It puts plaintext through api,
   contradicting ADR-0053's single custody. Rejected; the CLI import remains.
3. **api generates the key and forwards it to the connector.** api would hold plaintext. Rejected for
   the same reason.
4. **Keep `generate`/`bind` as the only demo path.** It keeps the two server commands this release
   exists to remove. Kept as a fallback (decision 6), not as the default.
5. **An SSH certificate authority in the connector** (short-lived certificates instead of key pairs).
   Stronger in the long run, but it needs every target to trust the CA, a deployment change far beyond
   this release. Deferred; the generate path does not preclude it.

---

## Consequences

**Gained:**
- An operator never handles a private key at all. Previously they handled it on disk (demo) or in a
  file they passed to the CLI.
- The demo needs nothing on the server after `setup.sh`.
- Every key's origin is audited.

**Costs, accepted deliberately:**
- ⚠ **The connector gains its first write route reachable via api.** A compromised api can now ask the
  connector to **generate or rotate** a key for any resource. It still cannot read any private key,
  but a rotation can **break access** to a target until the new public half is installed. Rotation
  therefore requires the explicit `rotate` flag and is audited. Exfiltration is not added; disruption
  is, and it is stated.
- **"Pending" credentials are a new state** in the connector, keyed by label rather than by resource. A
  stale pending demo key, from a declined or abandoned demo, must be removable (decision 7), and must
  not be leasable: only bound credentials are.
- **The install-time step creates a local account**, opt-in, on the operator's command. The release notes
  and the install guide must say exactly what it creates and how to remove it.
- **SEC-66 must be proven** by searching every API response, log and audit event produced during
  generate, bind, lease and rotate for the private key's bytes, with a planted control.

---

## How we would know this was wrong

- **Testers decline the demo at setup and then cannot reach a governed action without the terminal.**
  Then the prompt's wording or its default is wrong, not the trust split.
- **A rotation locks an operator out of a production target.** Then rotation needs a confirmation that
  names the consequence; it never needs a way to read the old private key.
- **Anyone proposes "just let the service install the key."** That is alternative 1. It needs its own
  ADR reversing the v0.12.0 decision, never a ticket-level change.
