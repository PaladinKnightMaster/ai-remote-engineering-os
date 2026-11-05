# PostgreSQL for the install — Reference

**This page is Reference.** `setup.sh` installs the database for you — the three-command install
in the [install guide](offline-install-v3.md#the-install-three-commands) never sends you here. Read
it to see what the PostgreSQL companion does, to **use your own PostgreSQL 16 instead**, or to
install the companion by hand. (The words are in the install guide's
[Words used in this guide](offline-install-v3.md#words-used-in-this-guide).)

## What the companion is

`aireos-postgres-<os>.tar.gz` (`<os>` = `ubuntu2404`, `ubuntu2604` or `rocky9`, `amd64`) is a
self-contained PostgreSQL 16 with `pgcrypto`. It **never touches the operating system's package
manager**, so a server patched after the companion was built cannot break it the way the v0.16.0
companion could. `setup.sh` unpacks it to a temporary folder, and `install-postgres.sh`:

- checks the host first and changes **nothing** if a check fails (wrong OS, a missing or too-old
  host library, a port in use — the messages are in the install guide's
  [Something went wrong?](offline-install-v3.md#something-went-wrong));
- installs the tree under `/opt/aireos/pgsql`, creates the data directory `/var/lib/aireos/pgsql`
  and the service user `aireos-pg`, and runs it as the systemd unit `aireos-postgres` (TCP login
  needs a password: `scram-sha-256`);
- creates the `cockpit` role and database with `pgcrypto` enabled, and prints the connection URL;
- is safe to re-run, and refuses a port that is taken. It coexists with a PostgreSQL the
  distribution installed, as long as they use different ports (`AIREOS_PG_PORT`).

`setup.sh` generates the `cockpit` password (256 bits), keeps it in `/etc/aireos/db-password`
(root only, never printed) and builds the URL for `install.sh`.

## Bring your own PostgreSQL 16

Use this if your OS has no companion, or you already run PostgreSQL 16 you want to use.

1. Install `postgresql-16` and `postgresql-contrib` (Rocky/RHEL 9: `postgresql16-server` and
   `postgresql16-contrib`) from your organisation's package mirror, then initialise and start
   the cluster the way your distribution documents.
2. Make sure password login works over loopback TCP. PostgreSQL 16 on Ubuntu defaults to
   `scram-sha-256` for `127.0.0.1`; check `pg_hba.conf` if unsure.
3. Open a database shell as the `postgres` superuser with `sudo -u postgres psql`, and run the
   lines below. `\password cockpit` asks you for the password (twice, without showing it) —
   choose one with letters and digits only, and write it down:

   ```sql
   CREATE ROLE cockpit LOGIN;
   \password cockpit
   CREATE DATABASE cockpit OWNER cockpit;
   \c cockpit
   CREATE EXTENSION pgcrypto;
   ```

4. Give `setup.sh` the URL, in the same `sudo` line, **after** `sudo` (it discards variables set
   before it). No companion is installed; no companion file is needed:

   ```sh
   sudo AIREOS_DB_URL='postgres://cockpit:<password>@localhost:5432/cockpit?sslmode=disable' ./aireos-offline-<version>/setup.sh
   ```

`setup.sh` still asks the questions and ends at the browser setup, as in the install guide. A
malformed URL is refused before anything is written. You own PostgreSQL patching and backups.

**Verified to the extent stated:** the application installer was run against a supplied database
URL on a fresh, fully offline server and completed — all seven services active, login working.
Installing PostgreSQL from *your* mirror depends on your mirror and was not exercised.

## Install the companion by hand

This is what `setup.sh` runs; use it only to see the pieces. Verify the downloads first (the install
guide's command 1). Choose a password (letters and digits only), then:

```sh
tar -xzf aireos-postgres-<os>.tar.gz
sudo COCKPIT_PW=<password> ./aireos-postgres-<os>/install-postgres.sh
```

It ends with `[postgres] DONE — PostgreSQL 16 + pgcrypto ready under aireos-postgres (port 5432).`
and the URL template for `install.sh`. A taken port is refused; choose another with
`sudo COCKPIT_PW=<password> AIREOS_PG_PORT=5433 ./aireos-postgres-<os>/install-postgres.sh`. Then
run `install.sh` as the install guide's [old flow](offline-install-v3.md#the-old-flow-installsh-and-a-companion-by-hand)
describes.

## Notes

- `pgcrypto` is a trusted extension (PostgreSQL 13+), so the `cockpit` role, as the owner of
  its database, can enable it without superuser rights.
- Only the three OS companions above are shipped. A companion you build yourself has no published
  signature, so the install guide's command 1 does not cover it.
