# v0.17.0 — Install & First-Run Experience

**The release that asks: can someone who rarely uses a terminal get from the downloaded files to a
completed, audited governed action, on any patched supported OS, typing at most three commands on the
server?**

Status: **release candidate.** Not yet signed, tagged or published. The tag waits for the maintainer's
signature and for HUM-1 (a first install by a tester who is not the maintainer). Claims below carry a
source marker: `[read: path]` (opened in the tree) or `[ran: …]` (executed, output seen); the ladder rows
are in `v0.17.0-release-criteria.md` §10.

---

## What this release does NOT do — stated first

- **No new governed capability and no new WD.** Nothing new is governed; the policy → approval →
  connector → broker chain is unchanged [read: v0.17.0-release-criteria.md §2]. The gate is the
  later-host test, the SEC negative proofs, a WD-1..15 regression and a stranger's install (HUM-1).
- **The API surface is NOT frozen.** New routes arrive in this release (setup, credentials, demo target);
  the freeze is at v1.0.0 final [read: CLAUDE.md, SEQUENCING].
- **Governed-harness integration (B-041), containment (B-040) and operator visibility of command output
  are not in this release** [read: criteria §1, §8].

---

## What's new

**Three commands on the server, then the browser.** The install guide's server path is one verification
line, one `tar`, and one `sudo … ./setup.sh`; everything after that is in the browser. Before this release
the guides asked for 22 command lines on the server [read: criteria §1]. `task docs-rehearsal` extracts the
guide's server lines verbatim and runs them on a fresh later host: 3 lines, with negative controls (a 4th
line; a wrong `tar` name) observed failing [read: ladder T-177].

**`setup.sh` — one command installs everything (INS-5, INS-6).** It finds its own directory and the
companion archive beside it, generates the database password itself (never printed, stored `0600`), wires
the database URL, installs PostgreSQL and the application, checks the services stayed up and Caddy
answers before it prints DONE, warns when the site address is not on this server, and ends by printing the
address to open. Every value can still be supplied by environment variable for unattended installs
(`sudo` drops the environment, so write `sudo VAR=… ./setup.sh`) [read: ladder T-174].

**First-operator setup in the browser, with a one-time code (ADR-0078).** `setup.sh` prints a one-time
setup code on the server's terminal. The first operator account is created in the browser by entering it.
Until setup completes every other route answers `503 setup required`; once it completes, the setup routes
answer as if they never existed [read: ladder T-175a/b]. The code lives 60 minutes, works once, and is
stored only as a hash. If it is lost, `sudo ./setup.sh --new-setup-code` issues another; the old one stops
working.

**The certificate fingerprint to compare is the *intermediate*'s.** `setup.sh` prints the SHA-256 of
Caddy's **intermediate** certificate ("Caddy Local Authority - ECC Intermediate") as the value to compare
in the browser, and the root's on a labelled second line. The reason: Caddy serves the leaf and the
intermediate and never the root, so a root fingerprint can never appear in a browser's certificate viewer
[ran: the bundle's Caddy with `tls internal` + `openssl s_client -showcerts`, per ladder T-175e]. The
install guide says where in the certificate viewer to find it; those menu steps are unverified until HUM-1
(see limitations).

**Connector-generated target keys (ADR-0079).** A target's SSH key pair (ed25519) is generated **inside
the connector**, the sole decrypt authority (ADR-0053). The cockpit shows only the public half and the
exact restricted `authorized_keys` line. The private half never leaves the connector. Operator-imported
keys still work.

**The one-click demo.** After `setup.sh` (with the demo step opted in), the cockpit has a button that
creates the loopback demo target: a governed Run on it completes with no further command on the server
(`POST /v1/demo-target`) [read: ladder T-176c/d].

**The key panel, with rotation.** Each target has a credential panel on Sessions: public key,
fingerprint, the `authorized_keys` line with a Copy button. Rotation sits behind a confirmation that names
the consequence: **the next Run fails until you install the new line on the target.** The panel has no
private-key input anywhere [read: ladder T-176d].

**A self-contained PostgreSQL 16.15 for Ubuntu 24.04, Ubuntu 26.04 and Rocky 9.** The companion is now a
source-built PostgreSQL tree under `/opt/aireos/pgsql` with its own `aireos-postgres.service`, built on
each OS's floor image, and it never touches the host's package manager (ADR-0077). This ships the first
Ubuntu 26.04 companion, the first Rocky 9 companion, and the first Rocky SBOM [read: ladder T-172b].

---

## ⭐ R1 is fixed

R1 — "the postgres companion fails on any server patched after it was built" — was stated as a known
limitation in v0.16.0 and has existed since v0.1.0. It was invisible to WD-1 because WD-1 builds its host
and the companion from one snapshot. The fix is structural: the companion no longer depends on the host's
package versions. The evidence:

- **INS-1 passes on all three OSes**, on hosts provisioned independently of the build: the newest image
  plus every update available that day, checked to be strictly newer than the build floor; the companion
  alone, then the full stack through `install.sh` [read: ladder T-173].
- **SEC-63 — the old design is shown to fail the same test.** The v0.16.0 recipe, unchanged, rebuilt
  against the Ubuntu archive snapshot of 2026-09-01, fails on the later host with `perl : Depends:
  perl-base (= …ubuntu0.4) but …ubuntu0.6 is to be installed` — the line the PUB-6 rehearsal found
  [read: ladder T-173; ran: `task later-host-test`].
- **The build-floor finding (INS-11).** A tree built on the newest point release failed on older ones: a
  Rocky tree built on 9.8 linked `pgcrypto.so` to `EVP_MD_CTX_get_size_ex@OPENSSL_3.4.0` and failed on
  Rocky 9.4 while passing on 9.7. Each OS is therefore built on its floor image (Ubuntu 24.04 GA, 26.04
  GA, Rocky 9.0), smoke-tested on floor and newest, and the installer's fail-fast check records each host
  library's minimum symbol version, so an older host is refused before anything changes [read: ladder
  T-171].

## ⚠ R1 is latent, not absent, in the published v0.16.0 companion

The **published v0.16.0 companion still carries R1.** It installs today only because it pins `perl-base`
`…ubuntu0.6`, which is still the head of Ubuntu's archive [read: ladder T-173; ran: Commander re-run
`task later-host-test -- ubuntu2404`, exit 0, 2026-11-01]. **The next perl update will break it.** If you
run v0.16.0, or plan to install it, **move to v0.17.0.**

---

## What changed for an existing v0.16.0 install (INS-8)

- **A re-run of `setup.sh` keeps your PostgreSQL and database URL and installs no companion.** On a host
  whose `DATABASE_URL` is already in `/etc/aireos/aireos.env`, it says so and changes nothing about the
  database. Nothing is migrated; to move onto the new companion, install it deliberately on a fresh host
  [read: docs/devops/offline-install-v3.md §"Running setup.sh again"].
- **The operator and the site address are now kept across re-runs.** A re-run used to reset a custom site
  address to `localhost` because it was never persisted; fixed in review [read: ladder T-174].
- **`/var/lib/aireos` is now `0751`** (its children stay `0750`) so the PostgreSQL service user can reach
  its data directory. With `0750` PostgreSQL ran until its next checkpoint, then panicked on
  `pg_control: Permission denied` [read: ladder T-172c].
- **The app units now order after `aireos-postgres`** (harmless when you bring your own PostgreSQL).
- **DONE now waits for health.** The installer waits for every unit to stay active and for Caddy to answer
  HTTPS before printing DONE; otherwise it fails and names the unit (the v0.16.0 "restart is not a health
  check" limitation) [read: ladder T-174].
- **New migrations apply automatically:** api `0017` (first-run setup) and connector `0003` (generated
  credentials).
- **Setup mode never engages when an operator exists**, so an upgraded install does not drop into
  setup.

---

## Security notes

**A browser path to creating the first operator.** For the first time the first operator account can be
created from a browser rather than at the server's terminal (ADR-0078). It is gated by the setup code and
open for **at most 60 minutes** on the LAN. After **5 wrong attempts the code is locked.** ⚠ Anyone who can
reach the page can use this to lock the legitimate operator out **until a new code is issued from the
server** (`setup.sh --new-setup-code`); it is a denial, not a takeover. The code never enters the audit
chain, an HTTP log or the shell history (SEC-67, planted control) [read: criteria §5].

**The connector's first api-reachable write route.** Rotation can break a target's access, and it is
audited. The connector trusts the api's operator claim for these routes: the routes are api-only (a subject
pin), and paired (`approvals@1`) sessions receive 403 on all three new operator routes [ran:
TestPairingRouteWalkDenyByDefault, per ladder T-176d].

**The demo account and its restricted key.** The demo step creates a dedicated account whose single
`authorized_keys` line is restricted; the demo port is fixed at 22, and the step refuses plainly if sshd is
not reachable there. `setup.sh --remove-demo` removes it.

**New audit events:** `setup.code.issued` and `setup.code.locked` (actor `system:setup`),
`setup.completed` (actor `operator:<name>`, target `setup-client:<ip>`), and `credential.generated`,
`.rotated`, `.bound`, `.pending`, plus `resource.created` for the demo and `auth.login.success` for the
session setup mints [read: ladder T-175c, T-176a/b].

**Three privilege defects found and fixed in review before release**, each caught by review or an automated
check, none by a green suite, each with its test observed failing on the old code:
- **T-172a — data-dir symlink:** a symlink in the PostgreSQL data directory could be followed by the
  root-run installer (`fde52f3`).
- **T-172c — state-dir lockout:** a state directory at `0750` locked the database service out (see the
  `0751` change above).
- **T-176c — demo-home symlink:** root-run `authorized_keys` edits followed symlinks planted by the demo
  account into root's files; all `~/.ssh` operations now run as the account (`2cece04`).

**And a stale-public-key defect (T-176a):** `set-secret` over a generated key left the old public half
recorded; fixed in `2ad877e`.

**The project now owns PostgreSQL 16 security updates.** A PostgreSQL security release means a new
companion from this project; an air-gapped host could never `apt upgrade` the old one either. This is
accepted deliberately (criteria §9). **OpenSSL is linked from the host on all three OSes — nothing is
bundled** [read: ladder T-171], so OpenSSL fixes come with your OS updates.

---

## Known limitations and edges

- **The profile prompt.** `setup.sh` asks the deployment profile first; the reader types `lan-only`. An
  answer, not a command line, but it is a step HUM-1 is meant to judge.
- **The certificate-viewer steps are unverified until HUM-1.** The guide says where to find the
  intermediate's fingerprint in a browser's certificate viewer; the native browser UI could not be driven
  by the tests, so those menu steps are `[assumed]` until a human follows them.
- **A lost bind reply leaves an inert bound credential.** If the connector commits a bind but its reply is
  lost, the api deletes the resource and the credential stays bound to a dead id. It is never leasable
  without the resource; recover with `credentials unset-secret <id>` and re-run setup.
- **The cockpit's remove does not delete the stored key.** "Remove the demo target" leaves the connector
  credential; `--remove-demo` names `setup-loopback-demo.sh --undo --resource-id <id>`.
- **`--remove-demo` leaves a rotated line.** It matches only its own marker, so an operator-installed
  rotated line is not removed.
- **One 404 for "no key" and "imported".** The api collapses the two; the panel infers "imported" from a
  409 on generate.
- **The demo's resource id has no screen** (needed only to delete its stored key after removal).
- **WD-1 is `ubuntu2404` only;** the other OSes are proven by the later-host test and the docs rehearsal.
- **`amd64` only.**

---

## Verification

WD-1..15 on both stacks, `task smoke`, the later-host test on all three OSes, `docs-rehearsal`, the SBOM
coverage gate, the quickstart and setup browser gates, and the instrument gates: see
`verification_runbook.md` Step 1. The results table is filled in at the closure commit, after signing.

---

## Release provenance

| | |
|---|---|
| Tag | `v0.17.0` → TBD (derived from the artifact) |
| Release-key fingerprint (SHA-256 of the DER public key) | `968a29b3623fa79a6d12e37dfe4c9e156b6c66c716b0e57a076026a9d1bc0bbf` — **no new keypair** |
| `aireos-offline-0.17.0.tar.gz` | SHA-256 `TBD at sign` |
| `aireos-postgres-ubuntu2404.tar.gz` | SHA-256 `TBD at sign` |
| `aireos-postgres-ubuntu2604.tar.gz` | SHA-256 `TBD at sign` |
| `aireos-postgres-rocky9.tar.gz` | SHA-256 `TBD at sign` |

Hashes are deliberately not recorded in the commit that gets tagged (recording them would be circular: they
come from artifacts built from that commit); they land in the closure commit after signing, as at v0.16.0.
