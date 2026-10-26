# ADR-0077 — Self-contained PostgreSQL companion

## Status

**Accepted** — 2026-10-26 (CEO, at v0.17.0 scoping). Proposed 2026-10-06 (v0.16.0 PUB-6 rehearsal, finding R1);
authored in the Commander seat. **Decider: CEO.**

**Refines ADR-0052 decisions 2 and 3** — *how* the postgres companion is packaged (decision 2) and, added
before acceptance at the CEO's request, *which OSes* it is built for (decision 3: Ubuntu 26.04 joins the
scope — see decision 9). ADR-0052's decisions 1 (per-OS companion artifacts, outside the core bundle) and
4 (build artifacts, never committed) are **unchanged**. Supersedes nothing.

⚠ **Nothing in this ADR is implemented or measured yet** beyond what §Context marks `[ran:]`. It is a
design for the release between v0.16.0 and v1.0.0 Beta 1.

---

## Context

### The failure

A PUB-6 rehearsal (`docs/releases/v0.16.0/pub6-rehearsal.md`) installed v0.16.0 on a fresh
`ubuntu:24.04` + systemd host with no network. `install-postgres.sh` failed:

```
perl : Depends: perl-base (= 5.38.2-3.2ubuntu0.4) but 5.38.2-3.2ubuntu0.6 is to be installed
```

The host carried a newer `perl-base` from a security update; apt will not downgrade a core package.
Forcing that one package back (a test manipulation) made the identical step succeed — the skew is the
only cause. **Every host patched after a companion is built hits this.**

### Why it happens — the chain, measured

[ran: `dpkg-deb -f` over the shipped companion's 98 packages, 2026-10-06]

```
postgresql-16 → postgresql-common (>= 252~) → perl:any → perl → perl-base (= 5.38.2-3.2ubuntu0.4)
```

[ran: `ldd` + a script-header scan on the shipped `postgres` server binary]: it links **40** shared
libraries and **none is libperl**; **0 of 15** server tools are Perl scripts. **The PostgreSQL server
does not need Perl.** The Perl is entirely `postgresql-common`'s `pg_*` wrapper scripts
(`pg_ctlcluster`, `pg_createcluster`, `pg_lsclusters`, …).

**So the defect is not PostgreSQL. It is delivering PostgreSQL as distro packages,** whose binary
packages built from one source carry exact-version pins on each other (`perl` ↔ `perl-base`).

### Why ADR-0052 did not foresee it

ADR-0052 decision 2 computes the closure *"on a pristine base"*; its Verification ran *"on pristine
`ubuntu:24.04` and `rockylinux:9`"*; and its Negative consequences state *"the host still owns runtime
patching."* [read: `docs/adr/ADR-0052-postgres-companion-artifacts.md`] **It explicitly expected hosts
to patch, while only ever verifying against hosts that had not.** Those two facts together are the defect.

### Why fifteen releases did not catch it

WD-1 builds its test host and the companion from the **same** `ubuntu:24.04` snapshot at the **same**
moment, so they can never drift. **A harness that co-builds the target and the artifact cannot detect
skew between them.** Only an independently provisioned host could, which is what the rehearsal was.

### Constraints this decision inherits

- **Pure air-gap is a CEO call, recorded in ADR-0052:** *"assume a host package mirror"* was rejected.
  v0.16.0's documented workaround — bring your own PostgreSQL from your mirror — **is that rejected
  path**, accepted only as a temporary stated limitation. **This ADR must restore the pure air-gap
  guarantee**, not ratify the workaround.
- **ADR-0041:** the product's services ship as static binaries precisely so the target's package state
  cannot break them; no container runtime on the target. PostgreSQL is the one component that escaped
  that rule.
- ⚠ **"Link against host libraries" is not sufficient** [ran: the `ldd` above]. The server links ICU 74,
  libxml2, LDAP, Kerberos, GnuTLS, libsasl2 and others — **many are not installed on a minimal server**.
  The package closure currently ships them. A replacement must ship whatever the minimal base lacks.
- ⓘ **Rocky/RHEL 9 is untested.** R1 was observed on Ubuntu only, and **only the `ubuntu2404` companion
  has been in the v0.15.0 and v0.16.0 release sets** [ran: `ls dist/offline/` at each cut]. Whether the
  `rocky9` companion has an analogous pin is unknown.

---

## Decision

**Ship PostgreSQL 16 as a relocatable, self-contained tree that never touches the host package manager.**

1. **Built from source, per OS.** For each supported OS the companion build compiles PostgreSQL 16 from
   the upstream source tarball (checksum-verified) on that OS's base image, into prefix
   `/opt/aireos/pgsql`, with `--with-openssl` (pgcrypto requires it).
2. **Only the features the product uses.** The implementing ticket measures which configure features the
   product actually exercises and disables the rest — each one removed is a library not shipped. ⚠ **ICU
   is a real decision, not a default**: dropping it changes collation behaviour, so it is chosen on
   evidence, not convenience.
3. **Private libraries, one rule.** **Bundle exactly the shared libraries not guaranteed present on the
   OS's minimal base image; link the rest from the host.** Bundled libraries live in `/opt/aireos/pgsql/lib`
   and are found through `RPATH=$ORIGIN/../lib`. They are never registered with dpkg/rpm, so nothing can
   conflict with, or be shadowed by, a host package. The minimal-base set is **measured per OS**, not assumed.
4. **No `postgresql-common`, therefore no Perl.** The product runs PostgreSQL under its own systemd unit
   (`aireos-postgres.service`, data in `/var/lib/aireos/pgsql`); `install-postgres.sh` does `initdb` and
   the existing role / database / `pgcrypto` provisioning.
5. **Coexists with a distro PostgreSQL** already on the host: distinct prefix, data directory and unit
   name; a configurable port that fails loudly if already taken.
6. **Fails fast and says why.** The companion carries a manifest of the host libraries it expects, and
   `install-postgres.sh` checks them before changing anything.
7. ⭐ **A test that can fail — `block`.** The companion is verified on a host built **independently of,
   and later than, the companion** (a newer base with OS updates applied) — and, as the negative control,
   **the v0.16.0 package-closure companion must FAIL that same test.** A fix proven only by WD-1's
   co-build proves nothing, by construction.
8. **Bring-your-own PostgreSQL stays supported** — `install.sh` has only ever needed a connection URL —
   but stops being a workaround for a broken default.
9. **OS scope (refines ADR-0052 decision 3): Ubuntu 24.04, Ubuntu 26.04 and Rocky/RHEL-family 9, `amd64`.**
   Each OS gets its own source-built tree, and **each must pass decision 7's later-host test on its own** — a
   pass on one OS proves nothing about another. ⚠ No `rocky9` companion was in the v0.15.0 or v0.16.0
   release sets [ran: `ls dist/offline/` at each cut], so Rocky is untested ground, not a regression baseline.

---

## Alternatives considered and rejected

1. **Keep the package closure and rebuild it every release.** Narrows the window; never closes it. Fails
   again at the next security update to any pinned package.
2. **Have the operator build a companion matching their host's patch level** (e.g. snapshot-pinned
   repositories). Works, but moves the burden onto exactly the users the product is for.
3. **Drop core packages like `perl-base` from the closure.** Still fails: the shipped `perl` pins
   `perl-base` exactly. Removing `perl` means removing `postgresql-common` — which is this decision anyway.
4. **Force or allow downgrades.** Unacceptable on a production host.
5. **Make bring-your-own PostgreSQL the primary path.** ADR-0052 already rejected it on the pure-air-gap
   CEO call.
6. **A container image.** ADR-0041 rejected a container runtime on the target.
7. **A fully static PostgreSQL.** Not practical: extensions — `pgcrypto` included — are loaded with
   `dlopen`, and glibc's resolver behaviour resists static linking. Private shared libraries via RPATH
   achieve the same independence.
8. **Upstream PGDG repositories.** Still distro packages, with the same `postgresql-common` → Perl chain.

---

## Consequences

**Gained:**
- Installation no longer depends on the target's patch level, and nothing touches the package database.
- PostgreSQL rejoins ADR-0041's rule. The one exception to "the target's package state cannot break us"
  is gone.

**Costs, accepted deliberately:**
- ⚠ **The project owns PostgreSQL updates for the bundled tree** — a new companion for each PostgreSQL
  minor release with security fixes. ⓘ Less of a shift than it looks: an air-gapped host could never
  `apt upgrade` the companion's PostgreSQL either; it already depended on a new companion.
- **The project owns security updates for every library it bundles** (decision 3 keeps that set as small
  as the minimal base allows; libraries linked from the host stay the OS's to patch).
- The companion build becomes a source compile per OS — slower, and more build code.
- Operators lose Debian's `pg_ctlcluster`-style tooling; the product's unit and docs replace it.
- ⚠ **SBOM coverage changes shape.** `check-coverage.mjs` currently compares SBOM components with
  `repo/Packages` entries; a source build has no `Packages` file. The gate must change **in the same
  ticket**, or the companion ships ungated.
- **Existing installs are unaffected.** Their PostgreSQL is already installed and `install.sh` takes a URL;
  only new installs use the new companion. A migration path for an existing package-installed cluster is
  out of scope unless the CEO asks for one.

---

## How we would know this was wrong

- **The bundled-library update cadence becomes a burden** — the signal to link more from the host.
- **A target lacks even the assumed host libraries** (a stripped base) — decision 6 catches it at install
  time, not at runtime.
- **The independent-host test cannot be made to fail with the old companion** — then the test does not
  measure skew, and decision 7 is not met.
