# Offline postgres install (per-OS companion)

Postgres is **required on the host** (ADR-0051 OQ-6); the core bundle does not
embed it. For an air-gapped host, install it from the **per-OS companion artifact**
(`aireos-postgres-<ostag>.tar.gz`, ADR-0052) — a self-contained local package repo
with the complete dependency closure, so **no internet and no host package mirror
are needed**.

Supported at v0.1.0 (amd64): **Ubuntu 24.04** (`ubuntu2404`) and **Rocky/RHEL-family
9** (`rocky9`). Match the companion to your host OS.

## 1. (Build host, connected) build the companions

```sh
# Needs Docker; builds against each base-OS image so the closure matches the target.
bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404 rocky9
# -> dist/offline/aireos-postgres-ubuntu2404.tar.gz
#    dist/offline/aireos-postgres-rocky9.tar.gz
```

Transfer the companion matching your target OS (alongside the core bundle) to the
air-gapped host.

## 2. (Target host, offline, as root) install postgres + the cockpit DB

```sh
tar xzf aireos-postgres-<ostag>.tar.gz
cd aireos-postgres-<ostag>
COCKPIT_PW='choose-a-strong-password' ./install-postgres.sh
```

`install-postgres.sh` (offline):
- wires a `file://` apt/dnf repo from the companion and installs **postgresql-16 +
  postgresql-contrib (pgcrypto)**;
- `initdb`s the cluster (loopback TCP set to `scram-sha-256` so password auth works),
  starts postgres, and creates the `cockpit` role + database with `pgcrypto` enabled;
- prints the connection string for the next step.

It is idempotent: re-running reuses the existing role/database.

> **Manual equivalent** (if you prefer not to run the helper): point apt/dnf at the
> companion's `repo/` directory (`deb [trusted=yes] file:.../repo ./` or a `.repo`
> with `baseurl=file://.../repo`), install `postgresql-16`/`postgresql16-server` +
> the contrib package, `initdb`, then
> `CREATE ROLE cockpit LOGIN PASSWORD '…'; CREATE DATABASE cockpit OWNER cockpit;`
> and `CREATE EXTENSION pgcrypto;` in the `cockpit` database.

## Bring your own PostgreSQL 16

⚠ **Use this if `install-postgres.sh` fails with an unmet dependency** such as
`perl : Depends: perl-base (= …) but … is to be installed`. That happens on any host that
has applied OS security updates since the companion was built: the companion's packages
are pinned to its build-time patch level, and apt will not downgrade a core package. This
is a known v0.16.0 limitation; a proper fix is planned for a later release.

The application only needs a reachable PostgreSQL 16 with `pgcrypto`, plus a `cockpit`
role and database. Install it from your organisation's package mirror instead:

1. Install `postgresql-16` and `postgresql-contrib` (Rocky/RHEL 9: `postgresql16-server`
   and `postgresql16-contrib`) from your mirror, then initialise and start the cluster
   as your distribution documents.
2. Make sure password authentication works over loopback TCP. PostgreSQL 16 on Ubuntu
   defaults to `scram-sha-256` for `127.0.0.1`; check `pg_hba.conf` if unsure.
3. As the `postgres` superuser:
   ```sql
   CREATE ROLE cockpit LOGIN PASSWORD 'choose-a-strong-password';
   CREATE DATABASE cockpit OWNER cockpit;
   \c cockpit
   CREATE EXTENSION pgcrypto;
   ```
4. Continue with §3 below using
   `postgres://cockpit:<password>@localhost:5432/cockpit?sslmode=disable`.

**Verified to the extent stated:** the application installer was run against a supplied
database URL on a fresh, fully offline host and completed — all seven services active,
login working. Installing PostgreSQL from *your* mirror depends on your mirror and was
not exercised.

## 3. point the app installer at it

When you run the application `install.sh` (from the core bundle), give it the
connection string:

```
postgres://cockpit:<password>@localhost:5432/cockpit?sslmode=disable
```

`install.sh` validates the connection and runs the api/audit/session-broker
migrations (migration `0003` enables `pgcrypto` — already available from the
companion). See `docs/devops/offline-install-v3.md` for the full sequence.

## Notes

- **pgcrypto is a trusted extension** (PG13+): the `cockpit` DB owner can
  `CREATE EXTENSION pgcrypto` with no superuser at runtime.
- The host owns postgres patching + backups (ADR-0051). The companion pins the
  closure at build time; rebuild it to pick up postgres point releases.
- A different OS release = a new companion (`build-postgres-companions.sh <ostag>`);
  no code change.
