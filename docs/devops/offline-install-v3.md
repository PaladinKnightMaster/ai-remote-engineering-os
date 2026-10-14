# Install guide

How to install AI Remote Engineering OS on a Linux server. It works on a server with no
internet access. **You do not build anything to install** — you verify the downloads, install
PostgreSQL, install the application, and check that it runs.

> ⚠ **Known limitation (v0.16.0) — read before step 2.** The prebuilt PostgreSQL companion
> fails on a server that has applied operating-system security updates since the companion
> was built, with an error like `perl : Depends: perl-base (= …) but … is to be installed`.
> That is most well-maintained servers. If you hit it, use your own PostgreSQL 16 instead —
> [Bring your own PostgreSQL 16](offline-postgres-install.md#bring-your-own-postgresql-16) —
> and carry on at step 3. A proper fix is planned for a later release.

## What you need

- **A server**: Ubuntu 24.04 or Rocky/RHEL 9, `amd64`, and a login user that can run `sudo`.
  (Windows is a browser client only — there is no Windows server.)
- **Five files** from the project's **Releases** page, copied onto the server into one empty
  directory (the examples below assume you are in that directory):

  | File | What it is |
  |---|---|
  | `aireos-offline-<version>.tar.gz` and `aireos-offline-<version>.tar.gz.sig` | the application, and its signature |
  | `aireos-postgres-ubuntu2404.tar.gz` and `aireos-postgres-ubuntu2404.tar.gz.sig` | the PostgreSQL 16 companion, and its signature (prebuilt for Ubuntu 24.04 only — on Rocky/RHEL 9 see the [postgres guide](offline-postgres-install.md)) |
  | `aireos-release.pub` | the release public key |

  Copy them from your own computer with `scp`, run on your computer, not on the server:

  ```sh
  scp aireos-offline-<version>.tar.gz aireos-offline-<version>.tar.gz.sig aireos-postgres-ubuntu2404.tar.gz aireos-postgres-ubuntu2404.tar.gz.sig aireos-release.pub <user>@<server-ip>:
  ```

- **The key fingerprint** from the Releases page notes. Read it through a different channel
  than the downloads (see [what verification proves](../../SECURITY.md#verifying-a-release-before-you-install-it)).

**How to read the commands.** Anything in `<angle brackets>` is a blank you must fill in, and
the line tells you what to put there. If you paste a line without filling it in, the shell
stops with an error such as `No such file or directory` — that is the intended safety net, so
do not "fix" it by deleting the brackets only. In file names, `<version>` is the version
number in the name of the file you downloaded. In the `scp` line, replace `<user>` with your
login name on the server and `<server-ip>` with the server's IP address.

---

## 1. Verify the downloads

Do this **before you extract anything**. The installer runs from inside the extracted
application bundle, so it can never check the archive it came from — and the PostgreSQL
companion's installer runs as root. Verification uses only `openssl`, which is already on the
server. Run these in the directory holding the five files.

Print the public key's fingerprint and compare it, character for character, with the one on
the Releases page:

```sh
openssl pkey -pubin -in aireos-release.pub -pubout -outform DER | openssl dgst -sha256
```

The output ends with a 64-character hex string; that string must equal the published
fingerprint. If it does not, **stop** — the key is not the project's.

Then verify the application bundle and the PostgreSQL companion:

```sh
openssl pkeyutl -verify -pubin -inkey aireos-release.pub -rawin -in aireos-offline-<version>.tar.gz -sigfile aireos-offline-<version>.tar.gz.sig
openssl pkeyutl -verify -pubin -inkey aireos-release.pub -rawin -in aireos-postgres-ubuntu2404.tar.gz -sigfile aireos-postgres-ubuntu2404.tar.gz.sig
```

**Both must print `Signature Verified Successfully`.** A missing `.sig` file, or any other
output, means **stop**: do not extract or run anything from that file.

## 2. Install PostgreSQL

The application needs PostgreSQL 16 with `pgcrypto`, and a database and login role named
`cockpit`. The companion installs all of that from its own packages, with no internet.

Choose a password for the `cockpit` database login. **Use letters and digits only** (no
spaces or punctuation — it goes straight into a command and later into a URL). Write it down:
step 3 asks for it. Replace `<password>` below with it, without quotes:

```sh
tar xzf aireos-postgres-ubuntu2404.tar.gz
cd aireos-postgres-ubuntu2404
sudo COCKPIT_PW=<password> ./install-postgres.sh
cd ..
```

**What you should see**, at the end:

```
[postgres] DONE — postgres 16 + pgcrypto ready.
[postgres] Connection for install.sh:  postgres://cockpit:<password>@localhost:5432/cockpit?sslmode=disable
```

Keep that second line: it is the template for the database URL in step 3. Re-running is safe
(it reuses the existing role and database). On a server with no internet you may also see
`Failed to fetch http://security.ubuntu.com/…` warnings — they are harmless; the packages come
from the companion. If it stops with a `perl-base` error instead, see the limitation box at
the top.

## 3. Install the application

Unpack the application bundle and run its installer. **Replace `<address>`** with the address
operators will type into their browser to reach the cockpit: the server's IP address (find it
with `ip -4 addr` — use the one on your network, not `127.0.0.1`) or a DNS name that resolves
to it. Do not use `localhost` unless you will only ever browse from the server itself.

```sh
tar xzf aireos-offline-<version>.tar.gz
cd aireos-offline-<version>
sudo AIREOS_SITE_ADDRESS=<address> AIREOS_DEPLOYMENT_PROFILE=lan-only ./install.sh
```

`lan-only` is the deployment profile proven end to end; the others are described under
[Deployment profiles](#deployment-profiles) below. The installer then asks three things:

1. **Operator username** and 2. **Operator password** — you choose both. They are the
   login for the cockpit (the operator account is created for you); the password is not shown
   as you type. Write them down.
3. **Postgres URL** — type the line step 2 printed, with `<password>` replaced by the
   password you chose there (remove the angle brackets too):
   `postgres://cockpit:` + your password + `@localhost:5432/cockpit?sslmode=disable`.
   Arrow keys edit the line. If the text is not a usable database URL, the installer says so
   and asks again. A raw `@` inside a password is accepted, but `/`, `#`, `?` and `%` break the
   URL and would have to be percent-encoded (`%2F`, `%23`, `%3F`, `%25`) — another reason to
   stick to letters and digits.

When it finishes, it prints a line like this, which you should copy somewhere that is **not
this server**:

```
[install] AUDIT CHAIN-HEAD PUBLIC KEY (record out-of-band — …): <64 hex characters>
```

That is this installation's audit-signing public key. Anyone holding it can later check, on a
machine with no access to this server, that an exported copy of the audit log has not been
altered — with the `aireos-verify` program in the bundle's `bin/` directory (procedure:
[Step 4 of the v0.13.0 verification runbook](../releases/v0.13.0/verification_runbook.md)). A
copy kept off the server is what makes that check independent of the server.

**Re-running `install.sh` is safe** and is how you correct a mistake — a wrong site address,
for example. Your keys are kept, and it restarts all seven services so the change takes
effect. **Pass `AIREOS_SITE_ADDRESS` every time**: if you leave it out the address goes back
to `localhost`.

## 4. Check it works

**The files are intact.** In each unpacked directory, run the manifest check. It prints
nothing when every file matches:

```sh
cd aireos-offline-<version>
sha256sum -c --quiet SHA256_MANIFEST.txt; echo EXIT=$?
cd ../aireos-postgres-ubuntu2404
sha256sum -c --quiet SHA256_MANIFEST.txt; echo EXIT=$?
cd ..
```

Expect `EXIT=0` after each. (Do not pipe the command into anything — that hides the exit
status.)

**The services are running.** Expect the word `active` seven times:

```sh
systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy
```

Anything else (`failed`, `inactive`, `activating`) — look at
`sudo journalctl -u aireos-api -u aireos-caddy`.

**The cockpit answers.** In a browser on a machine that can reach the server, open
`https://<address>` (the address you chose in step 3). **Expect a certificate warning**:
the cockpit uses a certificate from its own private certificate authority, which your browser
has never heard of. Accept it to continue (to make the warning go away for good, see
[Trusting the certificate](#trusting-the-certificate)), and sign in with the operator username
and password from step 3. You should land on the Dashboard.

The server is now in the state the quickstart starts from.

## Next: the quickstart

**[Run your first governed action →](quickstart-first-governed-action.md)** It takes you from
this installation to a completed, audited action.

---

# Reference

## Deployment profiles

Every install declares a deployment profile — `lan-only`, `vpn-first` or `dmz-first` (see
[network topologies](../deep-dive/03-network-topologies.md)). There is no default. Without
`AIREOS_DEPLOYMENT_PROFILE` in the command the installer asks; an empty answer (when no earlier
run saved one) or an unrecognised value stops it before it changes anything. A value in the
command wins over one saved by an earlier run, which wins over the prompt.

`vpn-first` and `dmz-first` also **refuse** a loopback-only site address, because those
profiles exist to be reached from outside the server. Give them a routable name or IP, or
install as `lan-only`. Only `lan-only` has been proven end to end.

## Pairing a phone (optional)

Setting `AIREOS_PAIRING_ENABLED=1` turns on QR device pairing: a phone can be paired to a
narrow, revocable operator session that can read and approve or deny pending approvals, and
nothing else. It is **off by default**, and with it off every pairing route answers `404`.
Nothing prompts for it. A value you set is saved, so a later plain `install.sh` re-run keeps
it; `AIREOS_PAIRING_ENABLED=0` turns it off again.

```sh
sudo AIREOS_SITE_ADDRESS=<address> AIREOS_DEPLOYMENT_PROFILE=lan-only AIREOS_PAIRING_ENABLED=1 ./install.sh
```

## Trusting the certificate

Caddy serves `https://<address>` with a certificate issued by a private certificate authority
it created on this server. To stop browsers warning, import that authority's root
certificate into the operators' browsers or operating systems. Its file is, by default:

```sh
sudo cat /var/lib/aireos/caddy/caddy/pki/authorities/local/root.crt > aireos-ca-root.crt
```

(If that path does not exist, `sudo find /var/lib/aireos/caddy -name root.crt` finds it.)
Copy `aireos-ca-root.crt` to the operators' machines. Alternatively, put your own certificate
in: edit `/etc/aireos/Caddyfile`, replace `tls internal` with `tls <certificate-file>
<key-file>`, and run `sudo systemctl restart aireos-caddy`. Note that re-running `install.sh`
puts the stock `Caddyfile` back.

## Unattended install (no prompts)

Every prompt has an environment variable; supply them all and nothing is asked. Replace each
`<…>` first.

```sh
sudo AIREOS_DB_URL=postgres://cockpit:<password>@localhost:5432/cockpit?sslmode=disable \
  AIREOS_BOOTSTRAP_OPERATOR_USER=<operator-username> \
  AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=<operator-password> \
  AIREOS_SITE_ADDRESS=<address> AIREOS_DEPLOYMENT_PROFILE=vpn-first ./install.sh
```

A malformed `AIREOS_DB_URL` is refused before anything is written.

## What the installer does

It creates the `aireos` system user and `/opt/aireos`, `/etc/aireos`, `/var/lib/aireos`;
installs the programs and the web app; generates the cluster's signing keys on the server;
creates the operator account; writes `/etc/aireos/aireos.env` (readable by `aireos` only);
checks the database and runs every migration; then enables and (re)starts seven systemd
services: `aireos-api`, `aireos-audit`, `aireos-broker`, `aireos-connector`, `aireos-policy`,
`aireos-mcp-gateway` and `aireos-caddy`.

---

# For maintainers: build, sign and release

**You do not need anything below this line to install.** It is for the person who produces the
release artifacts.

## What ships

| Artifact | Contents | Built by |
|---|---|---|
| `aireos-offline-<version>.tar.gz` (~45 MB) | static `linux/amd64` binaries (api, audit-service, session-broker, connector-service, policy-service, mcp-gateway, authctl, aireos-verify) + Caddy + the web app + `Caddyfile` + systemd units + `install.sh` + `setup-loopback-demo.sh` + `SHA256_MANIFEST.txt` | `task offline-bundle` |
| `aireos-postgres-<ostag>.tar.gz` (~90 MB) | a self-contained postgres-16 + pgcrypto local package repository (full closure) + `install-postgres.sh` + `SHA256_MANIFEST.txt` | `build-postgres-companions.sh` |

Design records: ADR-0051 (architecture) and ADR-0052 (postgres companions). OS scope: Ubuntu
24.04 and Rocky/RHEL-family 9, amd64.

## Build (connected host)

```sh
task offline-bundle                                           # core bundle
bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404 rocky9
# artifacts land in dist/offline/ ; verify with sha256sum -c on the unpacked core bundle.
```

⚠ **`task offline-bundle` runs directly on the host — it needs Go 1.26, Node 22 and pnpm
installed there.** It does **not** containerise itself. On a host without them, run the build
inside a container that has both toolchains; the procedure the project itself uses is
recorded in `docs/releases/v0.15.0/verification_runbook.md` (Step 2).

**Sign both artifacts — a separate, host-side step, never part of the build above**
(ADR-0070 D1/D3). The release private key must never enter a build environment. Signing runs
afterward, on the machine holding the release private key:

```sh
task sign-release -- --key /path/to/aireos-release.key      # signs BOTH artifacts present
# -> dist/offline/aireos-offline-<version>.tar.gz.sig
# -> dist/offline/aireos-postgres-<ostag>.tar.gz.sig
```

**Both artifacts are signed, not just the core bundle.** The postgres companion is ~90 MB,
ships a full package repository, and runs `install-postgres.sh` as root on the target host —
leaving it unsigned would be indefensible (ADR-0070 Context, gap 1).

The signing step prints the public key's SHA-256 fingerprint — publish it in the release
notes (ADR-0070 D4) so an operator who obtains the public key over a second channel can
cross-check it. Transfer to the release page: both archives, both `.sig` files, and the
public key `aireos-release.pub` (SEC-44: the verifier is `openssl`, already on the target
host; only the public key needs distributing).

## Release key custody

**The production release key is never generated, stored, printed, or committed by any script
in this repo.** It is minted out-of-band by the CEO, on a machine that never runs untrusted
build code:

```sh
openssl genpkey -algorithm ed25519 -out aireos-release.key
openssl pkey -in aireos-release.key -pubout -out aireos-release.pub
```

- The **private key** (`aireos-release.key`) is held by the maintainer, **never** committed
  to this repository, and **never** placed on a build host — the same host that runs
  `task offline-bundle`'s container must not also hold this key.
- It is **distinct from every key `install.sh` mints** (the per-workload PASETO keys,
  ADR-0069's chain-head key) — those are per-install and machine-generated; this one is
  per-project and human-held. Never substitute one for the other.
- The **public key** (`aireos-release.pub`) and its SHA-256 fingerprint are published with
  each release (out-of-band distribution, SEC-44) and never retired — key rotation is
  additive-forever (ADR-0070 D8): an operator verifying an older release needs the key it
  was signed under.
- **What the signature proves, stated plainly:** this artifact was produced by someone
  holding the release key and has not been altered since. **The signature itself** does not
  prove the build was clean, and does not tie the artifact to a source revision, and claims
  **no SLSA level** — this repo has no CI and no remote (ADR-0070 D3). Separately — and NOT
  part of what the signature attests — every shipped Go binary embeds its own
  `vcs.revision`/`vcs.modified` (Go's `buildvcs` stamping, on by default, not stripped by
  `-trimpath -ldflags "-s -w"`); inspect with `go version -m bin/api`. That is a real,
  checkable provenance fact, but it is self-reported by the build and unsigned, and it is
  only trustworthy when the release is cut from a clean tree (`vcs.modified=false`).

**A throwaway fixture keypair for tests** lives at `infra/offline/testdata/fixture-release-key/`
— clearly labelled `FIXTURE-DO-NOT-USE-*`, committed on purpose, and unsuitable for any real
release (anyone with this repo already holds it). Use it only to exercise `task sign-release`
and `task provenance-check`; never point a real verification at it.
