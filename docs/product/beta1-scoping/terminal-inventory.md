# v1.0.0 Beta 1 — scoping input: terminal-dependence inventory

**Implementer pass, 2026-11-16, repo at `dev` / `96be956` (v0.17.0 tagged).** A scoping **input**, not a scope. It
decides nothing and recommends nothing beyond the ranking the CEO asked for. Companion input:
[`ecosystem-map.md`](ecosystem-map.md).

**The question.** For every journey a real user takes, which steps still need a terminal, and which are
browser-only? The CEO ranks the gaps; the Commander writes the strategy.

## How to read this

- **Column 3 is one of three values.** `Browser` = the user can do it on a screen of the cockpit.
  `Terminal` = it needs a shell (on the server, on the target, or on the user's own computer; the
  command column says which). `Not possible` = no way exists in any form, or none is documented and
  none is in the code.
- **Evidence (Rule 11).** `[read: path:line]` means I opened that file at that line. `[ran: …]` means I
  executed it and saw the output. No `[inherited:]` or `[assumed]` marker is used. Where I could not find
  where a step happens, the row says `NOT FOUND` and names what I searched.
- **Shipped behaviour, not the guides' description of it.** Every row cites code (scripts, service
  subcommands, cockpit routes and components, the OpenAPI fragment) first, and a guide second.
- **Nothing was run on a live install.** This pass read the code and ran searches over the repo. No
  command shown in a table was executed against a server. Where a row says what a script "does", that is
  from reading it. Absence claims ("no X exists") come from the sweeps in the next section.
- **`†` marks a deliberate terminal requirement** (a security property, not a UX gap). Each is explained
  in section (c) and must not be removed without an ADR.
- **Roles.** *Operator* = the human using the cockpit. *Integrator* = someone connecting an AI agent or
  harness to the MCP gateway. *Auditor* = someone outside the install checking the ledger.
- **Step IDs** (`J7.3`) are stable so the summary sections can point at them.

### The absence sweeps (Rule 12 — negative control)

Each "NOT FOUND" below rests on a search. Each sweep ran in the same shell as a **planted control**: a
file in the session scratchpad containing the searched words, which the same `grep` had to find first.
It found the plant each time (`control: 1`), so a zero count means the search could see the word.

| Claim | Searched (scope) | Result |
|---|---|---|
| No uninstall or teardown | `uninstall\|--purge\|teardown` over `infra/offline/*.sh`, `infra/offline/postgres/*.sh`, `infra/offline/systemd`, `docs/devops`, `README.md`, `SECURITY.md` | `[ran: grep -rniE …]` 0 hits |
| No backup or restore feature or guide | `pg_dump\|pg_restore\|backup` over the same plus `install-postgres.sh` | `[ran: …]` 1 hit: `docs/devops/offline-postgres-install.md:58` ("You own PostgreSQL patching and backups", bring-your-own-database case only) |
| No rollback or downgrade | `rollback\|downgrad` over `docs/devops`, `README.md`, `SECURITY.md`, the v0.17.0 release notes, `setup.sh`, `install.sh` | `[ran: …]` 0 hits |
| Cockpit cannot sign out | `auth/logout\|Sign out\|Log out` over `apps/web/src`, `packages/ui/src` | `[ran: …]` 0 hits (the `POST /v1/auth/logout` route exists: `docs/api-contracts/openapi.v3.fragment.yaml:409`) |
| Cockpit cannot mint an agent token | `/v1/agent-tokens` over `apps/web/src` | `[ran: …]` 0 hits |
| No key-rotation helper | `issue-agent\|rotate-key` over `packages/go/auth/cmd`, `services/*/main.go`, `infra/offline/*.sh` | `[ran: …]` 0 hits |
| Host-key capture undocumented | `ssh-keyscan\|ssh_host_key` over `docs/devops`, `README.md`, `apps/web/src/routes` | `[ran: …]` 0 hits (the label appears only in code comments in `apps/web/src/components/OnboardingChecklist.tsx` and `services/session-broker/internal/store/store.go`, and in the demo script) |
| No health or status screen | `health` (case-insensitive) over `apps/web/src` `*.tsx`, tests excluded | `[ran: grep -rn -i health …]` 0 hits |
| No notification channel | `smtp\|webhook\|notify\|sendmail\|push notification` over `services/*/internal` Go files | `[ran: …]` only `services/api/internal/store/store.go:38`, which is the Postgres outbox channel name, not a user notification |
| No jump-host support | `proxyjump\|bastion\|jump host` over `services/session-broker/internal`, `services/connector-service/internal` | `[ran: …]` 0 hits |
| No integrator guide | `mcpServers\|MCP client\|claude_desktop_config\|configure.*harness` over `README.md`, `SECURITY.md`, `docs/` (excluding releases, spike, ADRs, references), `services/*/README.md`; and `ls services/mcp-gateway` | `[ran: …]` hits only in architecture, requirements and planning documents and this folder; none in `docs/devops` or `docs/security`; `services/mcp-gateway` has no README |

Facts established by listing rather than sweep: the cockpit has exactly these routes — `/login`, `/pair`,
`/`, `/sessions`, `/sessions/:id`, `/invocations`, `/runs`, `/grants`, `/workstreams`, `/policy-rules`,
`/memories`, `/audit`, `/pairing`, plus the first-run setup gate [read: `apps/web/src/App.tsx:41-134`].
The REST contract has exactly the operations in `docs/api-contracts/openapi.v3.fragment.yaml`
[ran: `grep -n "^  /v1" …` for the path list]: there is no `PUT`/`PATCH` on resources, no password-change
route, no update-check route, and no logout caller in the cockpit.

---

## Journey 1 — Download and verify

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J1.1 Find the release and read the key fingerprint | Operator | Browser | The GitHub Releases page; the 64-character fingerprint is in the release notes. The guide wants it read "through a different channel than the downloads" | [read: `docs/devops/offline-install-v3.md:37-38`], [read: `README.md:126-132`] |
| J1.2 Download the five files into one folder | Operator | Browser | Application bundle + `.sig`, PostgreSQL companion for the right OS + `.sig`, `aireos-release.pub` | [read: `docs/devops/offline-install-v3.md:15-24`] |
| J1.3 Work out the server's OS to pick the companion | Operator | Terminal | `cat /etc/os-release` on the server (only if you do not already know it) | [read: `docs/devops/offline-install-v3.md:26-27`] |
| J1.4 Copy the five files to the server | Operator | Terminal | `scp <five files> <user>@<server-ip>:` on **your own computer** (PowerShell on Windows). The guide says "any SFTP client works too", the only non-terminal route, named in one clause and never shown | [read: `docs/devops/offline-install-v3.md:30-34`] |
| J1.5 Check the key fingerprint and every signature | Operator | Terminal | The one-line `openssl` command on the server; the only pass mark is `ALL VERIFIED` naming both archives. No browser-side or GUI verifier exists | [read: `docs/devops/offline-install-v3.md:84`, `:94-97`]; `†` D6 |
| J1.6 Verify on a Windows or macOS machine before copying | Operator | Not possible | Only a Linux shell command is provided. NOT FOUND: a verifier for Windows/macOS (searched `verify`/`openssl` in `README.md`, `docs/devops/*.md`) | [read: `docs/devops/offline-install-v3.md:80-97`] |

## Journey 2 — Install

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J2.1 Open a shell on the server | Operator | Terminal | The server's console, or `ssh <user>@<server-ip>` | [read: `docs/devops/offline-install-v3.md:13-14`] |
| J2.2 Unpack the application | Operator | Terminal | `tar -xzf aireos-offline-<version>.tar.gz` (unpack only that archive) | [read: `docs/devops/offline-install-v3.md:99-106`] |
| J2.3 Find the server's address to give the installer | Operator | Terminal | `ip -4 addr`; skippable if you only browse from the server itself | [read: `docs/devops/offline-install-v3.md:114-119`] |
| J2.4 Run the installer | Operator | Terminal | `sudo AIREOS_SITE_ADDRESS=<address> ./aireos-offline-<version>/setup.sh` (root required; checks everything first, then installs PostgreSQL and the app) | [read: `infra/offline/setup.sh:73`, `:234-237`], [read: `docs/devops/offline-install-v3.md:108-119`] |
| J2.5 Answer the deployment-profile prompt (`lan-only`, `vpn-first` or `dmz-first`) | Operator | Terminal | A prompt; no default. Only `lan-only` is proven end to end | [read: `docs/devops/offline-install-v3.md:123`, `:348-358`], [read: `infra/offline/install.sh:388-396`] |
| J2.6 Answer "Set up the demo target? [Y/n]" | Operator | Terminal | A prompt; creates an `aireos-demo` account and a restricted key; skipped if no SSH server answers on `127.0.0.1:22` | [read: `infra/offline/setup.sh:187-199`], [read: `docs/devops/offline-install-v3.md:124-130`] |
| J2.7 Copy the audit chain-head public key off the server | Operator | Terminal | Select the `[install] AUDIT CHAIN-HEAD PUBLIC KEY` line in the scrolling terminal output and save it elsewhere. Later: `grep AIREOS_AUDIT_CHAIN_PUBLIC_KEY /etc/aireos/aireos.env`. No cockpit screen shows it (searched `apps/web/src` for the variable name and phrase: 0 hits) | [read: `infra/offline/install.sh:598`, `:699`], [read: `docs/devops/offline-install-v3.md:153-157`]; `†` D7 |
| J2.8 Confirm the services are up (optional) | Operator | Terminal | `systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy aireos-postgres` | [read: `docs/devops/offline-install-v3.md:207-212`] |
| J2.9 Install on a host with no bundled PostgreSQL (other OS/version) | Operator | Terminal | `AIREOS_DB_URL=postgres://… ./setup.sh` with your own PostgreSQL 16 + pgcrypto | [read: `infra/offline/setup.sh:75-77`, `:98-100`], [read: `docs/devops/offline-install-v3.md:281`] |
| J2.10 Install with no terminal on the server at all | Operator | Not possible | `setup.sh` requires root and a shell (`This must run as root`). NOT FOUND: any web installer, image or package (searched `infra/offline/`) | [read: `infra/offline/setup.sh:73`] |
| J2.11 Install on a Windows, macOS or non-amd64 server | Operator | Not possible | "Windows is a browser client only"; supported: Ubuntu 24.04/26.04, Rocky/RHEL 9, amd64 | [read: `README.md:124-125`], [read: `docs/devops/offline-install-v3.md:11-12`] |

## Journey 3 — First-run setup

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J3.1 Read the one-time setup code and the certificate fingerprints | Operator | Terminal | Printed once on the server terminal at the end of `setup.sh` ("Setup code: ABCDE-FGHJK-MNPQR-STVWX", valid 60 minutes, single use). Never written to a file or log | [read: `infra/offline/setup.sh:176-183`], [read: `docs/adr/ADR-0078-first-run-setup-code.md:51-62`]; `†` D2 |
| J3.2 Open the cockpit and accept the certificate warning | Operator | Browser | `https://<address>/`; "Advanced" and proceed | [read: `docs/devops/offline-install-v3.md:169-172`] |
| J3.3 Compare the certificate's SHA-256 with the printed one | Operator | Browser | Browser certificate viewer, certificate "Caddy Local Authority - ECC Intermediate". The guide admits the menu names were not checked in a running browser | [read: `docs/devops/offline-install-v3.md:174-192`] |
| J3.4 Enter the code, choose username and a password of 12+ characters | Operator | Browser | The setup page → "Create operator account" | [read: `apps/web/src/routes/Setup.tsx:39`], [read: `docs/devops/offline-install-v3.md:194-196`] |
| J3.5 Land signed in on the Dashboard | Operator | Browser | "Signed in as `<you>` on edge `human-agent`, deployment profile `lan-only`" and a "Get started" panel | [read: `apps/web/src/routes/Dashboard.tsx:31-38`] |
| J3.6 Missed or expired code | Operator | Terminal | `sudo ./aireos-offline-<version>/setup.sh --new-setup-code` (refused once an operator exists) | [read: `infra/offline/setup.sh:221-232`]; `†` D2 |

## Journey 4 — Add a real target (not the demo)

The cockpit can create exactly one kind of resource: the loopback demo. Its form hard-codes the name
`loopback-demo`, host `127.0.0.1` and port `22`, and the source comment says "A general 'add any resource'
form is a real product gap (v1.x candidate)"
[read: `apps/web/src/components/OnboardingChecklist.tsx:171-172`, `:186`, `:241`].

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J4.1 Get the target's SSH **host** public key (mandatory, for host-key pinning) | Operator | Terminal | Not documented anywhere. A connection cannot be made without it: the broker treats `ssh_host_key` as mandatory (`ErrTargetIncomplete`). The only procedure in the repo is the demo script reading `/etc/ssh` host keys. The user must already know to run something like `ssh-keyscan`, or to read `/etc/ssh/ssh_host_*.pub` on the target | [read: `services/session-broker/internal/store/store.go:97-122`], [ran: absence sweep "Host-key capture undocumented"] |
| J4.2 Register the resource (name, type `ssh`, host, port, user, host key, optional `requires_approval`) | Operator | Terminal | No cockpit form exists. The only route is `POST /v1/resources` with `labels` `ssh_host`, `ssh_port`, `ssh_user`, `ssh_host_key`, `requires_approval`. From a script that means `curl` after a cookie login plus the CSRF header (the same dance as J7.2). No guide describes it | [read: `docs/api-contracts/openapi.v3.fragment.yaml:222-242`, `:2029-2044`], [read: `services/session-broker/internal/store/store.go:119-122`], [read: `apps/web/src/components/OnboardingChecklist.tsx:266-290`] |
| J4.3 Generate the target's key pair | Operator | Browser | Sessions → "SSH keys" → "Key for `<name>`" → "Generate a key". Works for any `ssh` resource, not only the demo | [read: `apps/web/src/routes/Sessions.tsx:114-135`], [read: `apps/web/src/components/ResourceCredentialPanel.tsx:58-84`, `:113`] |
| J4.4 Copy the `authorized_keys` line | Operator | Browser | "Copy line" button next to the public key and fingerprint | [read: `apps/web/src/components/ResourceCredentialPanel.tsx:116-141`] |
| J4.5 Install that line on the target account | Target's owner | Terminal | On the target: append the line to `~/.ssh/authorized_keys` (the quickstart's example uses `tee -a`). No service may do this | [read: `docs/devops/quickstart-first-governed-action.md:105-111`], [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:40-45`]; `†` D1 |
| J4.6 Alternatively import a key you already have | Operator | Terminal | `connector-service credentials set-secret <resource-id> --key-file <path> [--passphrase <pp>]` as root with `AIREOS_RESOURCE_SECRET_KEY` and `DATABASE_URL` from the connector's root-only env. The browser never accepts a private key; the panel then says an imported key "cannot be read or replaced from here" | [read: `services/connector-service/main.go:248-249`, `:287-326`], [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:95-97`], [read: `apps/web/src/components/ResourceCredentialPanel.tsx:106`]; `†` D5 |
| J4.7 Require approval for this target | Operator | Terminal | Only the `requires_approval` label at creation time (J4.2). Policy rules (Journey 9) can also force approval, in the browser | [read: `apps/web/src/components/OnboardingChecklist.tsx:275-282`], [read: `apps/web/src/routes/PolicyRules.tsx:118`] |
| J4.8 Change a target's host, port, user, host key or approval flag later | Operator | Not possible | No `PUT`/`PATCH` on `/v1/resources/{id}` exists (only GET and DELETE). The workaround is delete and recreate, which J5.6 shows can be blocked | [ran: `grep -n "^    patch:\|^    put:"` on the OpenAPI fragment: only `patchPolicyRule` at `:1726`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:267-296`] |
| J4.9 Test that the target is reachable and the key works, before first use | Operator | Not possible | NOT FOUND: no "test connection" operation in the REST contract. The first failure surfaces on the first Session or Run | [read: `docs/api-contracts/openapi.v3.fragment.yaml:222-350`] |
| J4.10 See your targets: a list or detail page | Operator | Not possible | NOT FOUND: there is no resources route in the cockpit. Targets appear only as dropdown entries and in the "SSH keys" list on Sessions | [read: `apps/web/src/App.tsx:41-134`], [read: `apps/web/src/routes/Sessions.tsx:84-135`] |
| J4.11 Reach a target through a jump host or bastion | Operator | Not possible | NOT FOUND: no jump-host support in broker or connector | [ran: absence sweep "No jump-host support"] |

## Journey 5 — Remove or rotate a target's key

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J5.1 Rotate the generated key | Operator | Browser | Sessions → SSH keys → "Key for `<name>`" → "Rotate key" → confirm "Rotate and replace the key". The cockpit warns that the target stops working until the new line is installed | [read: `apps/web/src/components/ResourceCredentialPanel.tsx:58-84`, `:140`, `:150-170`] |
| J5.2 Install the new line on the target | Target's owner | Terminal | Append the new line to `authorized_keys` on the target | [read: `docs/devops/quickstart-first-governed-action.md:103-111`]; `†` D1 |
| J5.3 Remove the old line from the target | Target's owner | Terminal | By hand in `~/.ssh/authorized_keys`. The scripts never remove a line the operator added | [read: `docs/devops/offline-install-v3.md:297-300`]; `†` D1 |
| J5.4 Rotate a key that was imported | Operator | Terminal | `connector-service credentials set-secret` again (J4.6). The browser says it "cannot be read or replaced from here" | [read: `apps/web/src/components/ResourceCredentialPanel.tsx:106`]; `†` D5 |
| J5.5 Delete a real target | Operator | Terminal | The cockpit's delete button exists only on the Dashboard checklist, and only for the demo resource (it is rendered only when `demoResourceId` is set). For any other resource, `DELETE /v1/resources/{id}` by `curl` | [read: `apps/web/src/components/OnboardingChecklist.tsx:358-385`, `:444-445`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:288-296`] |
| J5.6 Delete a target that has ever had a terminal session | Operator | Not possible | "A resource that has ever had a terminal session opened against it cannot be deleted" (`409 Conflict`; a permanent database link from session to resource). Not changeable short of a schema change | [read: `docs/devops/quickstart-first-governed-action.md:179-182`] |
| J5.7 Delete the stored key as well as the resource | Operator | Terminal | `connector-service credentials unset-secret <resource-id>`; for the demo `sudo ./setup-loopback-demo.sh --undo --resource-id <id>`. Deleting the resource in the cockpit leaves the key | [read: `services/connector-service/main.go:250-251`, `:270-283`], [read: `docs/devops/quickstart-first-governed-action.md:136-142`] |

## Journey 6 — Start an interactive session

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J6.1 Pick a resource and start a session | Operator | Browser | Sessions → select resource → "Start session" (mode is always `terminal`) | [read: `apps/web/src/routes/Sessions.tsx:52-70`, `:86-107`] |
| J6.2 Approve the session, if the resource requires approval | Operator | Browser | The session page shows "awaiting approval" with approve/deny | [read: `apps/web/src/routes/SessionView.tsx:60`, `:91-96`] |
| J6.3 Type in the terminal | Operator | Browser | `xterm.js` on a WebSocket proxied by Caddy to the session broker | [read: `apps/web/src/routes/SessionView.tsx:1-10`], [read: `infra/offline/Caddyfile:24-27`] |
| J6.4 End the session | Operator | Browser | "End" on the Sessions list or the session page | [read: `apps/web/src/routes/Sessions.tsx:72-75`, `:151`], [read: `apps/web/src/routes/SessionView.tsx:49`] |
| J6.5 Replay a recorded session | Operator | Not possible | Casts are stored under `/var/lib/aireos/sessions`, but no cockpit screen and no REST route serves them (the `Session` schema carries `castPath`; nothing in the cockpit reads it) | [read: `infra/offline/install.sh:600`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:2048`, `:2075`], [ran: `grep -rli "cast\|replay\|asciinema" apps/web/src --include=*.tsx`: only `Memories.tsx`, whose one hit is a comment at `:6`] |
| J6.6 Delete old session recordings | Operator | Terminal | `session-broker casts gc [--older-than-days N]` as root with the broker's env | [read: `services/session-broker/main.go:70-87`] |
| J6.7 Transfer files in a session (SFTP) | Operator | Not possible | The session contract has one mode, `terminal`. SFTP exists only as agent tools `remote.file.read/write/list` on the gateway. `README.md:43` describes "a browser-based SSH terminal and SFTP session"; the contract does not back an operator SFTP mode | [read: `docs/api-contracts/openapi.v3.fragment.yaml:2060-2062`], [read: `services/session-broker/internal/server/server.go:60`], [read: `docs/security/governed-tool-reference.md:106-113`] |

## Journey 7 — Connect an AI agent to the gateway (the integrator)

How an agent obtains a credential today, from the code: the **operator** logs in over REST, then calls
`POST /v1/agent-tokens` with `{agentId, resourceId, operations}`. The result is a PASETO token with
audience `mcp-gateway`, scoped to **one** resource, valid for **5 minutes**, which the agent presents as
`Authorization: Bearer` to `https://<address>/mcp`
[read: `services/api/internal/server/server.go:1295-1355`], [read: `packages/go/auth/auth.go:21`],
[read: `infra/offline/Caddyfile:34-40`]. The only worked example in the repo is a test script
[read: `infra/offline/wd1/fire-agent.sh:17-40`]. No integrator document exists
[ran: absence sweep "No integrator guide"].

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J7.1 Learn how to connect an agent at all | Integrator | Not possible | NOT FOUND: no guide or screen. The nearest text is `docs/security/governed-tool-reference.md` (what each tool does, and that a token "must name the file verbs explicitly"), which does not say how to obtain or present a token | [ran: absence sweep "No integrator guide"], [read: `docs/security/governed-tool-reference.md:106-113`] |
| J7.2 Authenticate as the operator from a script | Operator or integrator | Terminal | `curl -c jar -X POST https://<address>/v1/auth/login -d '{"username":…,"password":…}'`, then read the `aireos_csrf` cookie for the `X-CSRF-Token` header. The operator's password goes into a script | [read: `infra/offline/wd1/fire-agent.sh:17-22`], [read: `apps/web/src/routes/Login.tsx:36`] |
| J7.3 Mint the agent's token | Operator or integrator | Terminal | `curl -X POST https://<address>/v1/agent-tokens -b jar -H "X-CSRF-Token: …" -d '{"agentId":"…","resourceId":"…","operations":["exec"]}'`. No cockpit call site exists | [read: `infra/offline/wd1/fire-agent.sh:26-31`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:505-538`], [ran: sweep "Cockpit cannot mint an agent token"] |
| J7.4 Keep the agent connected past 5 minutes | Integrator | Terminal | Repeat J7.2 and J7.3 every 5 minutes; there is no refresh route. The code names a CLI sibling ("task auth -- issue-agent", for offline bootstrap) as deferred | [read: `packages/go/auth/auth.go:21`], [read: `services/api/internal/server/server.go:1303-1306`], [ran: sweep "No key-rotation helper"]; see D9 |
| J7.5 Get a token that does not expire in 5 minutes | Integrator | Not possible | No such credential exists in any form | [read: `packages/go/auth/auth.go:21`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:505-538`] |
| J7.6 Point the harness at the gateway | Integrator | Terminal | Harness-side configuration (a file or environment variable of the third-party tool): URL `https://<address>/mcp`, header `Authorization: Bearer <token>`. The product provides no screen or file for it | [read: `infra/offline/Caddyfile:34-40`], [read: `infra/offline/wd1/fire-agent.sh:35-40`] |
| J7.7 Make the harness trust the cockpit's private certificate authority | Integrator | Terminal | Fetch `root.crt` on the server (`sudo cat /var/lib/aireos/caddy/caddy/pki/authorities/local/root.crt`) and install it where the harness runs. Not downloadable from the cockpit | [read: `docs/devops/offline-install-v3.md:372-383`], [read: `infra/offline/Caddyfile:15-18`] |
| J7.8 Authorise the agent to launch Runs on its own (delegation grant) | Operator | Browser | Grants → "Issue a grant": agent subject (`agent:<id>`), resources, verbs, bounds. The subject must match the token's (`"agent:" + agentId`) | [read: `apps/web/src/routes/Grants.tsx:75`, `:139-215`], [read: `services/api/internal/server/server.go:1332`] |
| J7.9 Revoke an agent's standing authority | Operator | Browser | Grants → "Revoke" | [read: `apps/web/src/routes/Grants.tsx:102`] |
| J7.10 Revoke an already-minted token before it expires | Operator | Not possible | Revocation at this tier is "key rotation + short token lifetimes", no revocation list | [read: `docs/adr/ADR-0046.md:57`] |
| J7.11 See which agents have been issued tokens | Operator | Browser | Audit → filter Outcome `auth.agent-token.mint` (actor = operator, target = `agent:<id>`) | [read: `services/api/internal/server/server.go:1346`], [read: `apps/web/src/routes/Audit.tsx:101`] |
| J7.12 Plug in a packaged governed harness (OpenHands-style) | Integrator | Not possible | B-041 "has **not been started**" | [read: `README.md:86-91`], [read: `docs/spike/b041-remaining-work.md`] |

## Journey 8 — Approve or deny an agent's tool invocation; pair a phone

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J8.1 Approve or deny a pending `remote.exec` / file-tool call | Operator | Browser | Pending tool invocations (`/invocations`): the verbatim command, approve/deny. The gateway blocks the agent's call about 125 seconds, then fails closed | [read: `apps/web/src/routes/Invocations.tsx:44-60`, `:121-122`], [read: `infra/offline/wd1/fire-agent.sh:10-11`] |
| J8.2 Approve or deny an agent-composed Run plan | Operator | Browser | Runs → "Approve" / "Deny" on a `proposed` Run | [read: `apps/web/src/routes/Runs.tsx:156-162`] |
| J8.3 Be told that something is waiting | Operator | Not possible | NOT FOUND: no email, push, webhook or sound. The list is read when the page is opened and after an action; the file has no polling interval | [ran: sweep "No notification channel"], [read: `apps/web/src/routes/Invocations.tsx:29`, `:55`] |
| J8.4 Turn phone pairing on | Operator | Terminal | `sudo AIREOS_SITE_ADDRESS=<address> AIREOS_DEPLOYMENT_PROFILE=lan-only AIREOS_PAIRING_ENABLED=1 ./aireos-offline-<version>/setup.sh`. Off by default; with it off the Pairing page says "not enabled on this deployment". No cockpit toggle | [read: `docs/devops/offline-install-v3.md:360-370`], [read: `infra/offline/install.sh:414-421`], [read: `apps/web/src/routes/Pairing.tsx:119-123`]; `†` D8 |
| J8.5 Create a pairing request and QR | Operator | Browser | Pairing → label → create; shows a QR and a link `<origin>/pair#<id>.<secret>` | [read: `apps/web/src/routes/Pairing.tsx:73`, `:100-102`] |
| J8.6 Open the link on the phone and redeem it | Operator (phone) | Browser | `/pair` page, outside the sign-in guard | [read: `apps/web/src/routes/Pair.tsx:63`], [read: `apps/web/src/App.tsx:42-45`] |
| J8.7 Phone reaches the cockpit | Operator (phone) | Browser | Works only if the site address is reachable from the phone (the default `localhost` is not), and the certificate warning is clicked through | [read: `docs/devops/offline-install-v3.md:114-119`, `:169-172`], [read: `apps/web/src/routes/Pairing.tsx:100`] |
| J8.8 Stop the phone's warning for good | Operator | Terminal | Fetch `root.crt` on the server (`sudo cat …/root.crt`), copy it to the phone, import it in the phone's settings | [read: `docs/devops/offline-install-v3.md:372-383`] |
| J8.9 Revoke a paired phone | Operator | Browser | Pairing → paired sessions → revoke | [read: `apps/web/src/routes/Pairing.tsx:92`] |

## Journey 9 — Author policy rules

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J9.1 Write a rule (subject, edge, action, target → allow / requires_approval / deny) | Operator | Browser | Policy rules → "author rule", with the blast radius shown inline before saving | [read: `apps/web/src/routes/PolicyRules.tsx:118`, `:238-243`] |
| J9.2 Edit, enable, disable, delete a rule | Operator | Browser | The rule list actions | [read: `apps/web/src/routes/PolicyRules.tsx:148`, `:163`] |
| J9.3 See which rule produced a given decision | Operator | Not possible | The wire carries no rule id; the screen's own comment says an invocation's decision "cannot yet be attributed to the rule that produced it" | [read: `apps/web/src/routes/PolicyRules.tsx:23-30`] |
| J9.4 Write a rule over command arguments | Operator | Not possible | Rules govern the `(subject, edge, action, target)` tuple "not command arguments" | [read: `README.md:53-54`] |

## Journey 10 — Charter a workstream (scheduled governed runs)

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J10.1 Have a target to run against | Operator | Terminal | Journey 4 first (a real target needs the REST call), or the demo target (Journey 25) | see J4.2 |
| J10.2 Charter: objective, scope resources, steps, interval, bounds, TTL | Operator | Browser | Workstreams → "Charter a workstream". One consent act; creates the delegation grant too | [read: `apps/web/src/routes/Workstreams.tsx:299`, `:443-570`] |
| J10.3 Schedule at a wall-clock time ("every day at 02:00") | Operator | Not possible | The schedule field is "Interval seconds" (default 300). NOT FOUND: any cron or calendar field in the charter form | [read: `apps/web/src/routes/Workstreams.tsx:92`, `:569`] |
| J10.4 Charter an adaptive workstream (an agent composes each plan) | Operator | Terminal | Needs a "composer agent" subject, i.e. a connected agent, which means Journey 7's terminal steps | [read: `apps/web/src/routes/Workstreams.tsx:482`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:2318-2320`] |
| J10.5 Pause, resume, retire | Operator | Browser | Buttons on the charter | [read: `apps/web/src/routes/Workstreams.tsx:381`, `:642-654`] |
| J10.6 Watch history and plan requests | Operator | Browser | Charter detail panel, refreshed every 10 seconds while open | [read: `apps/web/src/routes/Workstreams.tsx:16-23`, `:728-759`] |
| J10.7 Edit a charter | Operator | Not possible | "Immutable plan template; editing the work = retire + re-charter" | [read: `apps/web/src/routes/Workstreams.tsx:1-6`] |
| J10.8 Read what a scheduled command printed | Operator | Not possible | Output is not stored anywhere | [read: `docs/devops/quickstart-first-governed-action.md:68-71`, `:176-177`] |

## Journey 11 — Memory and context sources

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J11.1 Author, pin, share, redact a memory | Operator | Browser | Memory page: "author memory", pin/unpin, share/unshare, redact | [read: `apps/web/src/routes/Memories.tsx:241`, `:264`, `:276`, `:293`, `:344`] |
| J11.2 Export memory | Operator | Browser | Client-side markdown manifest of the listed scope (no server call) | [read: `apps/web/src/routes/Memories.tsx:140-150`] |
| J11.3 Put the documents to index on the server | Operator | Terminal | A context source is a directory **on the API host** (`rootPath`, "the local/mounted directory"). Getting files there is `scp`/copy on the server. The API unit has `ProtectHome=true`, so a path under `/home` cannot be read | [read: `docs/api-contracts/openapi.v3.fragment.yaml:2506-2508`], [read: `infra/offline/systemd/aireos-api.service:30`], [read: `services/api/internal/ctxindex/context.go:50-52`] |
| J11.4 Upload documents through the browser | Operator | Not possible | NOT FOUND: no upload route (the contract's only context-source operations are list, register by path, delete) | [read: `docs/api-contracts/openapi.v3.fragment.yaml:1421-1478`] |
| J11.5 Register the directory against a charter | Operator | Browser | Charter detail → "register context source" → root path. Registration is not validated; an unreadable root is marked errored at the next sweep | [read: `apps/web/src/routes/Workstreams.tsx:342`, `:844-855`], [read: `docs/api-contracts/openapi.v3.fragment.yaml:2508`] |
| J11.6 Remove a source | Operator | Browser | "Remove" with a confirm that says it cannot recall what an agent already read | [read: `apps/web/src/routes/Workstreams.tsx:45-48`, `:365`] |
| J11.7 See index freshness | Operator | Browser | "last indexed … ago" per source | [read: `apps/web/src/routes/Workstreams.tsx:52`, `:827`] |

## Journey 12 — Read and verify the audit chain (including `aireos-verify` offline)

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J12.1 Read and filter the timeline | Operator | Browser | Audit: newest first, server-side actor/outcome/edge filters, "Load more" | [read: `apps/web/src/routes/Audit.tsx:1-30`, `:101`], [read: `docs/devops/quickstart-first-governed-action.md:82-92`] |
| J12.2 See whether the chain is intact | Operator | Not possible | The screen shows how many positions the chain has ("The full chain has N positions") and nothing about integrity. Integrity is a CLI | [read: `apps/web/src/routes/Audit.tsx:281`], [read: `services/audit-service/main.go:7-8`] |
| J12.3 Verify the chain on the server | Operator | Terminal | `audit-service verify` as root with the service's environment (not self-configuring; read values with `grep`/`cut`, never `source`) | [read: `services/audit-service/main.go:71`, `:86-100`], [read: `docs/releases/v0.13.0/verification_runbook.md:98-110`] |
| J12.4 Export the evidence | Operator | Terminal | `sudo DATABASE_URL="$DB" AIREOS_AUDIT_ANCHOR_PATH="$AN" /opt/aireos/bin/audit-service export > evidence.ndjson`. No export button in the Audit screen (searched `export` there: nothing) | [read: `docs/releases/v0.13.0/verification_runbook.md:103-110`], [read: `services/audit-service/main.go:77`] |
| J12.5 Hold the chain-head public key somewhere other than the server | Auditor / operator | Terminal | Saved at install (J2.7); the verifier takes keys only from `--pubkey` or `--keyring`, never from the export | [read: `services/audit-service/cmd/aireos-verify/main.go:19-27`]; `†` D7 |
| J12.6 Get the verifier onto the checking machine | Auditor | Terminal | `aireos-verify` is in the bundle's `bin/` (not a separate release file); copy the file out, for example with `scp` | [read: `README.md:126-132`], [ran: `ls dist/offline/aireos-offline-0.17.0/bin` lists `aireos-verify`] |
| J12.7 Run the verifier offline | Auditor | Terminal | `aireos-verify --pubkey <hex> evidence.ndjson`. Exit 0 only for chain INTACT+ATTESTED and anchor IN_SYNC; 1 BROKEN; 2 truncation; 3 degraded; 64 usage | [read: `services/audit-service/cmd/aireos-verify/main.go:50-62`], [read: `docs/releases/v0.13.0/verification_runbook.md:128-136`] |
| J12.8 Run the verifier on Windows or macOS | Auditor | Not possible | The shipped binaries are "static `linux/amd64`". NOT FOUND: a Windows/macOS build in the guide's artifact table | [read: `docs/devops/offline-install-v3.md:411`] |
| J12.9 Check the audit trail with no local tooling (a web verifier) | Auditor | Not possible | NOT FOUND: no web verifier; the claim "an outsider can check us" is carried by the CLI | [read: `services/audit-service/cmd/aireos-verify/main.go:1-10`] |

## Journey 13 — Back up and restore

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J13.1 Back up the install | Operator | Not possible | NOT FOUND: no backup script, command or guide. The only line is "You own PostgreSQL patching and backups" for a bring-your-own database | [ran: sweep "No backup or restore"], [read: `docs/devops/offline-postgres-install.md:58`] |
| J13.2 Know what to back up | Operator | Not possible | Not written down. From the installer, state lives in at least: the companion's data directory under `/var/lib/aireos/pgsql`; the env files under `/etc/aireos` (including `connector.env`, which holds the key that encrypts stored target credentials, and `audit.env`, the chain-head signing key); the anchor file `chain-heads.ndjson`; session casts under `/var/lib/aireos/sessions`; Caddy's CA under `/var/lib/aireos/caddy` | [read: `infra/offline/install.sh:432`, `:450-461`, `:521`, `:599-600`], [read: `infra/offline/setup.sh:67`] |
| J13.3 Restore to a new host | Operator | Not possible | NOT FOUND: no procedure. From the code, stored credentials are encrypted under `AIREOS_RESOURCE_SECRET_KEY`, so a database restore without the matching `connector.env` would leave them undecryptable; no document says so | [read: `services/connector-service/main.go:309`, `:319-326`] |
| J13.4 Do either by hand | Operator | Terminal | Whatever the operator composes (`pg_dump`, `tar`) as root | derived from J13.2 |

## Journey 14 — Upgrade to a new release

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J14.1 Learn a new release exists | Operator | Browser | The GitHub Releases page. NOT FOUND: any in-product notice (no update-check route; the Dashboard shows no version) | [read: `apps/web/src/routes/Dashboard.tsx:31-38`], [ran: `grep -n "^  /v1" …` path list: no update route] |
| J14.2 Download the new files | Operator | Browser | As J1.2 | see J1.2 |
| J14.3 Copy and verify the new files | Operator | Terminal | As J1.4-J1.5 | see J1.4, J1.5 |
| J14.4 Unpack the new bundle and re-run setup | Operator | Terminal | `tar -xzf …` then `sudo ./aireos-offline-<new>/setup.sh`. There is no section called "upgrade"; the nearest is "Running setup.sh again". A re-run keeps keys, the database password, the operator and the saved address, runs migrations, and restarts the seven units | [read: `docs/devops/offline-install-v3.md:252-259`], [read: `infra/offline/install.sh:11-17`] |
| J14.5 Database upgrade | Operator | Terminal | A re-run re-runs the companion installer idempotently; it refuses to cross a PostgreSQL major version | [read: `infra/offline/setup.sh:14`, `:112-131`], [read: `infra/offline/postgres/install-postgres.sh:103`] |
| J14.6 Check what version is running | Operator | Terminal | The bundle's `VERSION` file, or `systemctl`/binary checks. No cockpit screen | [ran: `ls dist/offline/aireos-offline-0.17.0` lists `VERSION`], [read: `apps/web/src/routes/Dashboard.tsx:31-38`] |
| J14.7 Roll back | Operator | Not possible | NOT FOUND: no rollback or downgrade procedure | [ran: sweep "No rollback or downgrade"] |

## Journey 15 — Recover: lost operator password; expired or lost setup code

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J15.1 Setup code lost, expired, used, or locked after repeated wrong guesses | Operator | Terminal | `sudo ./aireos-offline-<version>/setup.sh --new-setup-code` (the old code stops working; all five failure causes show the same message) | [read: `infra/offline/setup.sh:221-232`], [read: `docs/devops/offline-install-v3.md:239`]; `†` D2 |
| J15.2 Reset a forgotten operator password | Operator | Terminal | Re-run the installer as root with `AIREOS_BOOTSTRAP_OPERATOR_USER` and `AIREOS_BOOTSTRAP_OPERATOR_PASSWORD`; "an operator in the environment always wins". The cockpit has no reset UI. **Not run** by me. The install guide's trouble table has no row for it, and its only mention of those variables is "make the operator now"; ADR-0078 says "the release notes must say so", and the v0.17.0 release notes mention only the setup code | [read: `docs/adr/ADR-0078-first-run-setup-code.md:79`, `:140-142`], [read: `infra/offline/install.sh:173-174`], [read: `docs/devops/offline-install-v3.md:282`], [read: `docs/releases/v0.17.0/release_notes_v0_17_0.md:45`]; `†` D3 |
| J15.3 Change the password while signed in | Operator | Not possible | NOT FOUND: no change-password route in the REST contract. ADR-0046 names "password reset", not "change"; whether a change is deliberately excluded is not stated | [read: `docs/api-contracts/openapi.v3.fragment.yaml:381-505` (the auth routes)], [read: `docs/adr/ADR-0046.md:55`] |
| J15.4 Recover cluster keys after losing `/etc/aireos` | Operator | Not possible | No procedure (see J13.3, J17.1) | see J13.3 |

## Journey 16 — Change the site address or deployment profile; enable pairing

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J16.1 Change the address people type | Operator | Terminal | Re-run `sudo AIREOS_SITE_ADDRESS=<new> ./aireos-offline-<version>/setup.sh`; restarts the services; if no operator exists, prints a new setup code. Browsers that use the old address stop working | [read: `docs/devops/offline-install-v3.md:254-258`], [read: `infra/offline/install.sh:398-400`] |
| J16.2 Change the deployment profile | Operator | Terminal | Re-run with `AIREOS_DEPLOYMENT_PROFILE=<profile>` or answer the prompt. `vpn-first`/`dmz-first` refuse a loopback address and are unproven end to end | [read: `docs/devops/offline-install-v3.md:348-358`], [read: `infra/offline/install.sh:388-396`] |
| J16.3 Enable or disable pairing | Operator | Terminal | `AIREOS_PAIRING_ENABLED=1` (or `0`) on a re-run; a value you set is saved | [read: `docs/devops/offline-install-v3.md:360-370`], [read: `infra/offline/install.sh:414-421`]; `†` D8 |
| J16.4 Use your own TLS certificate | Operator | Terminal | Edit `/etc/aireos/Caddyfile` (`tls <cert> <key>`) and `sudo systemctl restart aireos-caddy`; any later `setup.sh`/`install.sh` puts the stock `Caddyfile` back | [read: `docs/devops/offline-install-v3.md:383-385`], [read: `infra/offline/install.sh:443`] |
| J16.5 See the current profile | Operator | Browser | Dashboard header ("deployment profile `lan-only`"). The site address and pairing state are not on that screen (pairing state shows only as "not enabled" on its own page) | [read: `apps/web/src/routes/Dashboard.tsx:34-37`], [read: `apps/web/src/routes/Pairing.tsx:119-123`] |

## Journey 17 — Rotate cluster and service keys; renew the TLS certificate or CA

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J17.1 Rotate the cluster token-signing key (`AIREOS_PASETO_KEY`) | Operator | Not possible | No tool. `authctl` has only `genkey`, `genkeypair`, `genaeskey`, `hash`. ADR-0046 lists an "auth rotate-key operator action" that does not exist. On a re-run the installer re-exports the persisted key over any value you supply, so passing a new one does not take effect | [read: `packages/go/auth/cmd/authctl/main.go:30-66`], [read: `docs/adr/ADR-0046.md:53`], [read: `infra/offline/install.sh:450-456`], [ran: sweep "No key-rotation helper"] |
| J17.2 Rotate the resource-secret key (the one that encrypts stored target credentials) | Operator | Not possible | Same, and no re-encrypt tool exists (the `credentials` subcommands are `genkey`, `set-secret`, `unset-secret`, `generate-pending`, `remove-pending`, `bind-pending`, `migrate-legacy`) | [read: `services/connector-service/main.go:238`, `:248-266`] |
| J17.3 Rotate the agent-plane or gateway key pairs, or the audit chain-head key | Operator | Not possible | Generated once, then reused on every re-run (the audit key's reuse is described as "an evidence-integrity property") | [read: `infra/offline/install.sh:470-500`, `:515-519`] |
| J17.4 Know when the CA root expires | Operator | Not possible | NOT FOUND: no statement of the root's lifetime in the repo (searched the guide's certificate sections and `infra/offline/Caddyfile`). Not determined by this pass | [read: `docs/devops/offline-install-v3.md:372-385`] |
| J17.5 Replace the private CA with your own certificate | Operator | Terminal | As J16.4 | see J16.4 |

Note, not a row: renewal of the cockpit's leaf and intermediate certificates is automatic. Caddy's
`tls internal` issues them, the intermediate rotates about every 7 days, and each `--new-setup-code`
re-reads the current intermediate fingerprint [read: `infra/offline/Caddyfile:15-18`],
[read: `infra/offline/setup.sh:153`]. Nothing for the user to do.

## Journey 18 — Observe health and logs; troubleshoot a failed unit

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J18.1 Is the product up? | Operator | Terminal | `systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy aireos-postgres` (expect `active` eight times) | [read: `docs/devops/offline-install-v3.md:207-212`] |
| J18.2 A status or health screen in the cockpit | Operator | Not possible | NOT FOUND (no health text in `apps/web/src`; routes listed above). Each service has an ungated `/healthz`, reachable only by `curl` on the server | [ran: sweep "No health or status screen"], [read: `services/connector-service/main.go:6-9`] |
| J18.3 Read service logs | Operator | Terminal | `sudo journalctl -u <service> --no-pager -n 50` | [read: `docs/devops/offline-install-v3.md:243`], [read: `infra/offline/install.sh:318`] |
| J18.4 Diagnose a failed install or unit | Operator | Terminal | The guide's "Something went wrong?" table, keyed on the exact message; the installer itself fails loudly when a unit does not stay active (`INS-10`) | [read: `docs/devops/offline-install-v3.md:223-246`], [read: `infra/offline/install.sh:318`, `:366`] |
| J18.5 See what agents are doing | Operator | Browser | Invocations (pending) and Audit (history) | [read: `apps/web/src/routes/Invocations.tsx:29`], [read: `apps/web/src/routes/Audit.tsx:101`] |
| J18.6 Know whether the audit pipeline is falling behind | Operator | Not possible | NOT FOUND: no screen or command documented for outbox depth or anchor lag in the guides (the verifier reports anchor states, J12.7) | [ran: sweep "No health or status screen"] |

## Journey 19 — Uninstall

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J19.1 Remove the product | Operator | Not possible | NOT FOUND: no uninstall script or guide. By hand it would mean stopping the eight units, removing the unit files, `/opt/aireos`, `/etc/aireos`, `/var/lib/aireos` and the `aireos` user; none of it is written down | [ran: sweep "No uninstall or teardown"], [read: `infra/offline/install.sh:50-52`] |
| J19.2 Remove only the demo | Operator | Terminal | `sudo ./aireos-offline-<version>/setup.sh --remove-demo` (plus the cockpit's "Remove the demo target" for the resource) | [read: `infra/offline/setup.sh:204-218`], [read: `docs/devops/offline-install-v3.md:291-310`]; `†` D1 |
| J19.3 Remove the product's keys from every real target | Target's owner | Terminal | Edit each target's `authorized_keys`. The product never wrote to it, so it will not undo it | [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:98-100`]; `†` D1 |

## Journey 20 — Add a second operator

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J20.1 Add another operator account | Operator | Not possible | One operator, enforced by the database ("AT MOST ONE ROW") and by design: "There is no 'add another operator' path; that remains ADR-0046's Zitadel trigger" | [read: `services/api/migrations/0017_setup.up.sql:29-34`], [read: `docs/adr/ADR-0078-first-run-setup-code.md:84-85`]; `†` D4 |
| J20.2 Give a colleague limited access | Operator | Browser | The only partial route: a pairing QR (J8.5) gives a second device an approvals-only session under the **same operator identity**. It needs pairing enabled first (J8.4, Terminal) | [read: `docs/devops/offline-install-v3.md:360-364`], [read: `apps/web/src/routes/Dashboard.tsx:14`] |
| J20.3 Tell two people apart in the audit trail | Operator | Not possible | Every event's actor is `operator:<the one username>` | [read: `docs/devops/quickstart-first-governed-action.md:85-87`] |
| J20.4 Sign in with a company identity provider | Operator | Not possible | "No OAuth providers" at this stage; Zitadel is the named reserve pick | [read: `docs/adr/ADR-0046.md:55`] |

---

## Journeys found while reading (not in the CEO's list)

## Journey 21 — Daily sign-in and sign-out

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J21.1 Sign in | Operator | Browser | `/login`, username + password | [read: `apps/web/src/routes/Login.tsx:36`] |
| J21.2 Stay signed in | Operator | Browser | Operator token lifetime 8 hours | [read: `packages/go/auth/auth.go:22`] |
| J21.3 Sign out | Operator | Not possible | `POST /v1/auth/logout` exists but no cockpit element calls it | [ran: sweep "Cockpit cannot sign out"], [read: `docs/api-contracts/openapi.v3.fragment.yaml:409`] |

## Journey 22 — Make a machine trust the cockpit's certificate

Every browser, phone and agent harness that reaches the server meets a certificate from a private
authority (`tls internal`).

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J22.1 Click through the warning each time | Operator | Browser | "Advanced" and proceed | [read: `docs/devops/offline-install-v3.md:169-172`] |
| J22.2 Get the CA root file | Operator | Terminal | `sudo cat /var/lib/aireos/caddy/caddy/pki/authorities/local/root.crt > aireos-ca-root.crt` on the server, then copy it off. Not downloadable from the cockpit | [read: `docs/devops/offline-install-v3.md:372-383`], [read: `infra/offline/Caddyfile:24-45`] |
| J22.3 Import it into each machine | Operator | Browser | The browser's or operating system's certificate settings (outside the product) | [read: `docs/devops/offline-install-v3.md:374-376`] |

## Journey 23 — Targets other than SSH; remote desktop

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J23.1 Register an `rdp`, `vnc`, `meshcentral`, `workspace` or `agentic_desktop` resource | Operator | Not possible | The schema accepts those names, but no session mode other than `terminal` exists and the cockpit has no screen for them | [read: `docs/api-contracts/openapi.v3.fragment.yaml:2036`, `:2060-2062`] |
| J23.2 A graphical remote desktop in the browser | Operator | Not possible | Guacamole is "still deferred" (a comment in the UI package's index) | [read: `packages/ui/src/index.ts:9`] |

## Journey 24 — Launch and watch a governed command (a Run)

The quickstart's core action, and the daily-use screen after install.

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J24.1 Compose, review, launch | Operator | Browser | Runs → resource, command, "Add step", "Review & launch", "Confirm launch" | [read: `apps/web/src/routes/Runs.tsx:114`], [read: `docs/devops/quickstart-first-governed-action.md:53-65`] |
| J24.2 Watch status; cancel | Operator | Browser | Run list and expanded Run; "Cancel" | [read: `apps/web/src/routes/Runs.tsx:41`, `:78`, `:134`] |
| J24.3 See what the command printed | Operator | Not possible | "Command output is not stored — the evidence of what ran is in the Audit timeline"; "by design for now" | [read: `docs/devops/quickstart-first-governed-action.md:68-71`, `:176-177`] |

## Journey 25 — The demo target (what v0.17.0 made browser-only)

| Step | Who | Browser / Terminal / Not possible | Exact command or screen | Evidence |
|---|---|---|---|---|
| J25.1 Prepare the demo during install | Operator | Terminal | The `setup.sh` question (J2.6); needs an SSH server on `127.0.0.1:22`; creates the `aireos-demo` account and a restricted `authorized_keys` line, as root | [read: `infra/offline/setup.sh:187-199`], [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:40-45`]; `†` D1 |
| J25.2 Create the demo target | Operator | Browser | Dashboard → "Create loopback demo target" (one click, no paste) | [read: `apps/web/src/components/OnboardingChecklist.tsx:201-229`] |
| J25.3 Remove the demo target | Operator | Browser | "Remove the demo target" (the stored key stays, J5.7) | [read: `apps/web/src/components/OnboardingChecklist.tsx:358-385`] |
| J25.4 Add the demo later, or on an SSH port other than 22 | Operator | Terminal | `sudo AIREOS_LOOPBACK_DEMO=yes ./setup.sh`, or the script path (`setup-loopback-demo.sh generate …`, paste the values into a cockpit form, then `bind`) | [read: `docs/devops/quickstart-first-governed-action.md:151-152`], [read: `apps/web/src/components/OnboardingChecklist.tsx:190-191`, `:252-290`] |

---

## Counts per journey

Counted mechanically from column 3 of each table in this file; the tables are the source.

| Journey | Browser | Terminal | Not possible | Total |
|---|---|---|---|---|
| J1 Download and verify | 2 | 3 | 1 | 6 |
| J2 Install | 0 | 9 | 2 | 11 |
| J3 First-run setup | 4 | 2 | 0 | 6 |
| J4 Add a real target (not the demo) | 2 | 5 | 4 | 11 |
| J5 Remove or rotate a target's key | 1 | 5 | 1 | 7 |
| J6 Start an interactive session | 4 | 1 | 2 | 7 |
| J7 Connect an AI agent to the gateway (the integrator) | 3 | 5 | 4 | 12 |
| J8 Approve or deny an agent's tool invocation; pair a phone | 6 | 2 | 1 | 9 |
| J9 Author policy rules | 2 | 0 | 2 | 4 |
| J10 Charter a workstream (scheduled governed runs) | 3 | 2 | 3 | 8 |
| J11 Memory and context sources | 5 | 1 | 1 | 7 |
| J12 Read and verify the audit chain (including `aireos-verify` offline) | 1 | 5 | 3 | 9 |
| J13 Back up and restore | 0 | 1 | 3 | 4 |
| J14 Upgrade to a new release | 2 | 4 | 1 | 7 |
| J15 Recover: lost operator password; expired or lost setup code | 0 | 2 | 2 | 4 |
| J16 Change the site address or deployment profile; enable pairing | 1 | 4 | 0 | 5 |
| J17 Rotate cluster and service keys; renew the TLS certificate or CA | 0 | 1 | 4 | 5 |
| J18 Observe health and logs; troubleshoot a failed unit | 1 | 3 | 2 | 6 |
| J19 Uninstall | 0 | 2 | 1 | 3 |
| J20 Add a second operator | 1 | 0 | 3 | 4 |
| J21 Daily sign-in and sign-out | 2 | 0 | 1 | 3 |
| J22 Make a machine trust the cockpit's certificate | 2 | 1 | 0 | 3 |
| J23 Targets other than SSH; remote desktop | 0 | 0 | 2 | 2 |
| J24 Launch and watch a governed command (a Run) | 2 | 0 | 1 | 3 |
| J25 The demo target (what v0.17.0 made browser-only) | 2 | 2 | 0 | 4 |
| **All** | **46** | **60** | **44** | **150** |

---

## (a) Terminal-only steps, ranked

**How I judged.** Two axes, each a plain scale.

- **How often a user hits it.** F1 = every install (every operator, once). F2 = every target added.
  F3 = every agent connected, or repeated on a short clock. F4 = every release or routine operation.
  F5 = rare, or only after something goes wrong.
- **How hard for a non-terminal user.** D1 = paste one unchanged line. D2 = paste a line after filling
  in blanks. D3 = compose values or sessions yourself (cookies, headers, JSON, environment variables).
  D4 = nothing in the product tells the user what to do, so they must already know how.

The rank is a judgment, stated openly: a step with **no documented path (D4)** outranks a more frequent
step that has one. The install steps (F1) have a guide with a pasted line and a checked pass mark, so they
rank below undocumented F2 and F3 steps even though more people hit them.

| Rank | Step | Freq | Diff | Why this rating |
|---|---|---|---|---|
| 1 | **J7.2-J7.4** Log in over REST, mint an agent token, repeat every 5 minutes | F3 | D4 | Every integrator, and again every 5 minutes. A cookie jar, a CSRF header, JSON, the operator's password in a script. No document; the example is a test script |
| 2 | **J4.1 + J4.2** Capture the target's host key, then register the resource by REST | F2 | D4 | Every real target, and the cockpit cannot create one. A mandatory host key with no instruction, and a labelled REST call with no guide |
| 3 | **J7.6 + J7.7** Configure the harness and make it trust the private CA | F3 | D3 | Every agent host. Harness-specific and undocumented, plus `sudo cat …/root.crt` and an import on the harness machine |
| 4 | **J4.5 / J5.2 / J5.3** Install and remove `authorized_keys` lines on the target | F2 | D2 | Every target and every rotation. Easy to paste, but it happens on a different machine, often owned by someone else (`†`) |
| 5 | **J2.4** Run `setup.sh` as root, with the right address | F1 | D2 | Every install. Needs a shell, `sudo`, and an address found with `ip -4 addr` (J2.3); the one command the whole install depends on |
| 6 | **J1.4 + J1.5** `scp` five files, then the one-line `openssl` verification | F1 | D2-D3 | Every install and every upgrade. A long pasted line with a fingerprint blank; the safety net is a printed `NOT VERIFIED` |
| 7 | **J14.3-J14.4** Copy, verify, unpack and re-run `setup.sh` to upgrade | F4 | D2 | Every release. No "upgrade" section exists; the instruction sits under "Running setup.sh again" |
| 8 | **J12.3-J12.7** Verify the chain: `audit-service verify` / `export` with the service's environment, then `aireos-verify` on another machine | F4 | D3 | Periodic. Needs root, the service's env values read out by hand, a file copy, and a key held off the server (`†` D7) |
| 9 | **J11.3** Copy documents onto the server for indexing | F4 | D2 | Every context source. A directory on the server, not under `/home` |
| 10 | **J15.2** Recover a lost operator password (root re-run with `AIREOS_BOOTSTRAP_OPERATOR_*`) | F5 | D4 | Rare, but the user is locked out. Not in the install guide's trouble table; documented only in an ADR (`†`) |
| 11 | **J22.2 / J8.8** Fetch the CA root from the server to remove certificate warnings | F1 | D2 | Every operator machine that wants no warning. Optional, but the warning is on the first screen |
| 12 | **J8.4 / J16.1-J16.3** Turn on pairing, change the address or profile (re-run `setup.sh` with an environment variable) | F5 | D2 | Occasional. A re-run with the right variable after `sudo`; wrong placement silently drops it (the guide warns) |
| 13 | **J4.6 / J5.4** Import an existing private key with `connector-service credentials set-secret` | F2 (if imported) | D3 | Root, the connector's env, a key file (`†` D5) |
| 14 | **J13.4** Back up by hand | F4 | D4 | Nothing exists; the user must already know what state the product keeps |
| 15 | **J5.5 / J5.7** Delete a real target and its stored key by REST and CLI | F5 | D3 | The cockpit deletes only the demo |
| 16 | **J18.1 / J18.3 / J18.4** Health and logs through `systemctl` and `journalctl` | F5 | D2 | Only when something looks wrong; the guide gives the commands |
| 17 | **J2.9** Bring-your-own PostgreSQL | F1 (subset) | D3 | Only for hosts outside the three supported OSes |

Everything in the table is a step the browser cannot do today. Ranks 10 and 14 are also places where
the *absence of a guide*, not the terminal itself, is what makes the step hard.

## (b) "Not possible at all" gaps

Capabilities that do not exist in any form (no cockpit screen, no REST route, no CLI, no document). Each
cites its journey row.

1. **Sign out of the cockpit** (J21.3). The route exists; nothing calls it.
2. **Change a password while signed in** (J15.3); **reset one in the browser** is also absent but
   deliberate (see c).
3. **A second operator, per-person audit attribution, or company sign-in** (J20.1, J20.3, J20.4).
4. **Edit a resource** (J4.8): host, port, user, host key and approval flag are fixed at creation.
5. **Test a target before first use** (J4.9); **see targets** as a list or detail (J4.10); **jump hosts** (J4.11).
6. **Delete a target that ever had a terminal session** (J5.6).
7. **Replay a recorded session** (J6.5); **SFTP in a session** for an operator (J6.7).
8. **Read what a command printed** (J24.3, J10.8): output is not stored, "by design for now".
9. **A long-lived agent credential** (J7.5); **revoke a minted token** before expiry (J7.10); any
   **integrator guide** (J7.1); a **packaged harness** (J7.12, B-041).
10. **Notification when something awaits approval** (J8.3).
11. **Learn which rule decided** (J9.3); **rules over command arguments** (J9.4).
12. **A wall-clock schedule** for a charter (J10.3); **edit a charter** (J10.7).
13. **Upload documents** for the context index (J11.4).
14. **See integrity of the chain on a screen, export it, or verify without a Linux shell**
    (J12.2, J12.8, J12.9); the browser shows only the chain's length.
15. **Backup, restore, rollback, uninstall** (J13.1-J13.3, J14.7, J19.1): no feature and no document.
16. **Rotate any cluster, agent-plane or chain-head key** (J17.1-J17.3), or know when the CA root
    expires (J17.4).
17. **A health or status screen**, or audit-pipeline lag (J18.2, J18.6).
18. **Install without a server terminal** (J2.10); on Windows, macOS or non-amd64 (J2.11); verify
    downloads on Windows (J1.6).
19. **Resources other than SSH, and a graphical remote desktop** (J23.1, J23.2).

## (c) Steps where the terminal requirement is deliberate

These are security properties. Each cites the ADR. **None may be presented as a defect to remove without
a new ADR** (CLAUDE.md §Forbidden work, gate G2).

| Tag | Property | Rows | ADR |
|---|---|---|---|
| **D1** | **The operator-run trust split.** No service writes a host's `authorized_keys`, creates an account or touches the filesystem; the operator's script (or the target's owner) does all of that, and the cockpit does everything that is governed. A "one-click demo" that let a service write `authorized_keys` "would reverse that decision". Also true for real targets, where only the target's owner can install a key | J4.5, J5.2, J5.3, J19.2, J19.3, J25.1 | [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:40-45`, `:92-100`]; the rule's origin is ADR-0068 §D8 [read: `docs/adr/ADR-0068-paired-operator-sessions.md:250-262`] |
| **D2** | **The setup code is printed only on the server's terminal, to root.** It is shown once, only its hash is stored, and it never appears in the chain, logs or shell history. The terminal is the proof that you are the person at the server. `--new-setup-code` is a root action | J3.1, J3.6, J15.1 | [read: `docs/adr/ADR-0078-first-run-setup-code.md:51-62`, `:86-95`] |
| **D3** | **No self-service signup and no password-reset UI.** Recovery of a lost password stays a root action on the server | J15.2 | [read: `docs/adr/ADR-0046.md:25`, `:55`], [read: `docs/adr/ADR-0078-first-run-setup-code.md:140-142`] |
| **D4** | **One operator; no "add another" path.** Adding users is ADR-0046's named trigger for a reserve identity provider (Zitadel), and "never a reason to keep setup mode open" | J20.1-J20.4 | [read: `docs/adr/ADR-0046.md:25`, `:55`], [read: `docs/adr/ADR-0078-first-run-setup-code.md:84-85`, `:154-155`] |
| **D5** | **The browser never accepts a private key.** Importing one is a server-side CLI action, because uploading it would put plaintext through api, which the connector-only-decrypts rule forbids. The generated private half is never shown or downloadable | J4.6, J5.4 | [read: `docs/adr/ADR-0079-connector-generated-target-credentials.md:95-97`], [read: `docs/adr/ADR-0053-connector-service.md`] |
| **D6** | **Release verification is the operator's step, before extraction, against a fingerprint from a second channel.** The installer cannot verify the archive it came from; "a check that runs after the untrusted code is already unpacked is theatre". That the check is a one-line `openssl` command is a convenience choice, not a mandated shape; the *ordering and the operator doing it* are what is mandated | J1.5 | [read: `docs/adr/ADR-0070-release-provenance.md:116-121`] |
| **D7** | **The audit trust root is held outside the server.** `aireos-verify` takes keys only from `--pubkey` or `--keyring`, never from the export, so a forged export cannot carry its own key. The chain-head public key therefore has to be saved off the server at install and carried to the checker (the *means* of carrying it is not mandated) | J2.7, J12.5, J12.7 | [read: `services/audit-service/cmd/aireos-verify/main.go:19-27`], [read: `docs/adr/ADR-0069-audit-chain-authenticity.md:335`] |
| **D8** | **Phone pairing ships off by default** and answers `404` when off. That it is switched by an install-time operator choice is the shipped mechanism; the ADR fixes the *default*, and I found no text requiring the switch to live in the terminal | J8.4, J16.3 | [read: `docs/adr/ADR-0068-paired-operator-sessions.md:291`, `:330`, `:364`] |
| **D9** | **Short agent tokens (5 minutes), with revocation by expiry.** The short lifetime is a decision; that minting needs a terminal is not | J7.4, J7.10 | [read: `docs/adr/ADR-0046.md:57`] |

**Not deliberate, though they look it.** These are terminal today for lack of a screen, with no ADR
mandating it as far as I found: running `setup.sh` itself (J2.4; it needs root, and no ADR says a
graphical installer is forbidden), registering a real target (J4.2), minting an agent token (J7.3; the
code comment calls the CLI "deferred" and the REST call exists), `audit-service export` and `verify`
(J12.3, J12.4), turning on pairing (D8 note), and every backup, upgrade and uninstall step.

## What I could not determine

- **Whether the guide's browser menu steps are right.** The guide itself says the Chrome/Edge/Firefox
  certificate-viewer instructions were "not checked in a running browser for this release"
  [read: `docs/devops/offline-install-v3.md:188-191`]. I did not open a browser.
- **Anything about live behaviour.** I ran no install. Whether, for instance, a re-run of `setup.sh`
  with `AIREOS_BOOTSTRAP_OPERATOR_*` really replaces the browser-created operator (J15.2) is from
  `install.sh:173-174` and ADR-0078 decision 4 only.
- **The CA root's lifetime** (J17.4): the repo states the intermediate's (about 7 days) but not the root's.
- **Whether a paired phone session can mint an agent token.** The route is operator-gated and the
  Dashboard hides operator actions from paired sessions, but I did not trace the server-side check.
- **The exact label set a non-demo `POST /v1/resources` accepts** beyond the four SSH labels and
  `requires_approval`: the schema is a free map of strings, and the broker reads the labels above.
- **A non-key route into a target** (a password or a forwarded agent): none found, but I did not read
  the broker's dial code end to end.
- **What `infra/offline/docs-rehearsal.sh` covers.** It rehearses the three install commands; I did not
  read it to see whether it also exercises any journey here.
