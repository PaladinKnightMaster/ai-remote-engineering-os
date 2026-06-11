---
name: T-033b Ticket
about: Governed Egress Foundation — OI-6 offline re-point + installed-stack WD-2
title: "T-033b: OI-6 offline re-point — connector+policy shipped, key relocated, WD-2 on the installed stack"
labels: ["ticket", "infra", "security"]
---

## Ticket ID and link

T-033b — second slice of **T-033** (split a/b/c at the 2026-06-11 consult; a =
the compose-leg WD-2 e2e, done `3a12169`; c = VER-5 legs + DOC-7..10 +
ADR-0055). Closes **OI-6** and re-enables the installed-stack wedge demo as
**WD-2** (release-criteria §2 — WD-2 supersedes WD-1). Conforms ADR-0051/0052
(offline architecture; companion reuse) + ADR-0053 (key custody: the relocation
of `AIREOS_RESOURCE_SECRET_KEY` out of the shared env file is SEC-13's
*install-time* enforcement) + ADR-0054.

## Goal

The offline bundle ships and provisions the v0.2.0 spine, and the WD-2
walkthrough passes against the installer-produced stack:

> bundle gains connector-service + policy-service (static binaries + units +
> migrations); `install.sh` works again at HEAD (genkey re-pointed off the
> deleted `api resources` CLI); the resource key moves to a connector-only
> `connector.env`; `run-wd1.sh` un-guarded and re-pointed; the installed
> walkthrough drives the **flagged** resource: parked create → approve → SSH →
> command → end → deny leg; host-internal chain assert gains
> `policy.decision:requires_approval` / `approval.granted` /
> `approval.denied:operator` / `credential.leased`.

## Priority

P1 — OI-6/OI-8 are must-pass release criteria; the installer is broken at HEAD
until this lands.

## Files in scope (8)

- Modify: `infra/offline/build-bundle.sh` — `SERVICES` += connector-service,
  policy-service; default `BUNDLE_VERSION` → 0.2.0.
- Create: `infra/offline/systemd/aireos-connector.service` — the ONLY unit
  reading `/etc/aireos/connector.env` (the key's custody boundary).
- Create: `infra/offline/systemd/aireos-policy.service` — shared env only.
- Modify: `infra/offline/install.sh` — genkey via `connector-service
  credentials genkey`; key preservation reads `connector.env` first, the legacy
  `aireos.env` line second (= the documented v0.1.0→v0.2.0 upgrade path, then
  the rewrite drops the legacy line); writes `connector.env` (0600);
  `aireos.env` gains `CONNECTOR_URL`/`POLICY_URL`/`CONNECTOR_ADDR`/`POLICY_ADDR`
  (127.0.0.1 binds); migrations + `systemctl enable` cover all five services.
- Modify: `infra/offline/wd1/run-wd1.sh` — T-030a guard REMOVED; seeded
  resource flagged `requires_approval:"true"`; set-secret via
  `connector-service credentials set-secret` (key from `connector.env`);
  six units asserted; outcome asserts extended (with relay-lag retry).
- Modify: `tests/e2e/wd1_test.go` — approve round-trip + deny leg (reuses the
  T-033a harness helpers; all T-021d-b/T-024 assertions kept).
- Create: this file + `docs/tickets/README.md` status flip.

## G2

No divergence. The ungated cockpit path is no longer driven on the installed
stack (the one seeded resource is flagged) — deliberate per the consult: WD-2
supersedes WD-1 and the ungated path stays live-proven by the compose e2e
(T-033a) on every run. ADR-0051's installer contract (idempotent, env-driven,
prompt-default) is preserved; the second env file is an additive refinement
within ADR-0053's "key relocates api→connector env" decision.

## Verification

Stale `dist/offline/aireos-offline-0.1.0*` deleted; bundle REBUILT FROM HEAD
(SPA via node:22-alpine, `build-bundle.sh` via golang:1.23) — OI-4 caps + OI-9
manifest self-enforced, OI-7 re-measured; then `task wd1` (systemd-in-Docker,
reusing the ubuntu2404 postgres companion) green end-to-end: bring-up, six
units, walkthrough incl. approve + deny, `audit-service verify` OK, all WD-2
outcomes in the chain. `gofmt -l` clean; `go vet -tags wd1` and `-tags e2e`
clean in golang:1.23.

## Definition of done

OI-6 closed (bundle + units + provisioning re-point + key relocation); OI-8
demonstrated by the green installed-stack run; WD-1 guard gone. Commit on
`dev`, fast-forward `main` when verified. No push.
