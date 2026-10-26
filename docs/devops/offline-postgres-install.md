# PostgreSQL for the install

**What you will have at the end:** PostgreSQL 16 with the `pgcrypto` extension running on the
server, with a database and a login both named `cockpit` — what the install guide's step 3
needs. **Time:** 5–10 minutes of your own work, plus however long your package source takes to
download. (The *PostgreSQL companion* is the project's prebuilt, offline database package; the
other words are in the install guide's
[Words used in this guide](offline-install-v3.md#words-used-in-this-guide).)

The core bundle does not include PostgreSQL. There are three ways to get it; use the first one
that fits.

| Your server | Use |
|---|---|
| Ubuntu 24.04 | try the prebuilt companion first — [install guide](offline-install-v3.md#2-install-postgresql), step 2 |
| The companion failed with an unmet dependency (see below), or you run Rocky/RHEL 9 | [Bring your own PostgreSQL 16](#bring-your-own-postgresql-16) |
| Rocky/RHEL 9, and you want the same offline experience | [Build a companion yourself](#build-a-companion-yourself-rockyrhel-9) |

Supported OS: **Ubuntu 24.04** (`ubuntu2404`) and **Rocky/RHEL-family 9** (`rocky9`), `amd64`.
Only the Ubuntu companion is prebuilt for this release.

## Using the companion

This is step 2 of the [install guide](offline-install-v3.md#2-install-postgresql); the commands
live there. For reference, `install-postgres.sh`:

- sets up the companion's `repo/` directory as a local package source and installs
  `postgresql-16` and `postgresql-contrib` (which contains `pgcrypto`) from it;
- initialises the cluster (loopback TCP uses `scram-sha-256` password login), starts it, and
  creates the `cockpit` role and database with `pgcrypto` enabled;
- prints the connection URL template that `install.sh` asks for.

It is safe to re-run: it reuses an existing role and database.

## Bring your own PostgreSQL 16

⚠ **Use this if `install-postgres.sh` fails with an unmet dependency** such as
`perl : Depends: perl-base (= …) but … is to be installed`. That happens on any server that
has applied OS security updates since the companion was built — the companion's packages are
fixed at its build day's versions. This is a known v0.16.0 limitation; a proper fix is planned.

1. Install `postgresql-16` and `postgresql-contrib` (Rocky/RHEL 9: `postgresql16-server` and
   `postgresql16-contrib`) from your organisation's package mirror, then initialise and start
   the cluster the way your distribution documents.
2. Make sure password login works over loopback TCP. PostgreSQL 16 on Ubuntu defaults to
   `scram-sha-256` for `127.0.0.1`; check `pg_hba.conf` if unsure.
3. Open a database shell as the `postgres` superuser with `sudo -u postgres psql`, and run the
   lines below. `\password cockpit` asks you for the password (twice, without showing it) —
   choose one with letters and digits only, and write it down for the install guide's step 3:

   ```sql
   CREATE ROLE cockpit LOGIN;
   \password cockpit
   CREATE DATABASE cockpit OWNER cockpit;
   \c cockpit
   CREATE EXTENSION pgcrypto;
   ```

4. Carry on at [step 3 of the install guide](offline-install-v3.md#3-install-the-application).
   When it asks for the Postgres URL, type
   `postgres://cockpit:` + your password + `@localhost:5432/cockpit?sslmode=disable`.

**Verified to the extent stated:** the application installer was run against a supplied
database URL on a fresh, fully offline server and completed — all seven services active,
login working. Installing PostgreSQL from *your* mirror depends on your mirror and was not
exercised.

## Build a companion yourself (Rocky/RHEL 9)

You need a connected machine with Docker and a checkout of this repository. It builds against
the target OS's own container image so the package set matches:

```sh
bash infra/offline/postgres/build-postgres-companions.sh rocky9
```

The result is `dist/offline/aireos-postgres-rocky9.tar.gz`. Move it to the server and follow
the install guide's step 2 with `rocky9` in place of `ubuntu2404`. A companion you built
yourself has no published signature, so step 1's check for it does not apply — and it is
pinned to the patch level of the day you built it, like the prebuilt one.

## Notes

- `pgcrypto` is a trusted extension (PostgreSQL 13+), so the `cockpit` role, as the owner of
  its database, can enable it without superuser rights.
- You own PostgreSQL patching and backups. A companion pins its package set when it is built;
  rebuild it to pick up PostgreSQL point releases.
