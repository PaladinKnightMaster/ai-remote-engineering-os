# v0.16.0 — Public Readiness

**The release that asks one question of everything already shipped: can somebody who is not us rely on
what we claim?**

---

## What this release does NOT do — stated plainly, first

**No new governed capability.** No new edge, verb, or decision point was added anywhere in the
governance plane. `mcp-gateway`, `policy-service`, `session-broker`, `connector-service`, `api`, and
`audit-service` behave, at runtime, exactly as they did at the end of v0.15.0.

**No change to the governance plane at runtime.** The PUB-6 fixes changed no file under `services/` or
`packages/`, and no API. ⚠ **Two operator-facing changes
did land, both found by PUB-6** (see "Found by PUB-6 and fixed before release"): `install.sh` now
restarts its services on a re-run and checks the database URL, and the cockpit's Get-started panel and
Run detail now show the information the quickstart needs. They change what an operator sees and how a
re-install behaves — not what is governed. The install wiring is unchanged: no new daemon, edge, key,
network path, or datastore.

**No new WD.** By the project's own gate-sizing rule — a new WD only for new governed capability — a
release that adds no governed capability has nothing for a new WD to measure. Minting one anyway would
have produced a gate measuring nothing new, which is precisely the defect class this release exists to
remove (§2 of the release criteria records this ratified explicitly, because the reflex on a new release
is to mint one).

If a change here reads as touching *what the product does* rather than *what can be verified about it or
read about it*, that is a defect in this document, not an intended feature — flag it.

---

## ⚠ Known limitation — the postgres companion on patched hosts

**The prebuilt postgres companion fails to install on any host that has applied OS security updates
since it was built.** Its packages are pinned to the build-time patch level; a patched host carries newer
core packages (found: `perl-base …ubuntu0.6` against the companion's `…0.4`), and apt will not downgrade
them. **This is not new** — it has existed since v0.1.0 (ADR-0052's package-closure design) — and it was
**invisible to every prior release's testing**, because the installed-stack test builds its host and the
companion from one snapshot, so they can never drift. A fresh-host rehearsal of this release's own PUB-6
gate found it.

**Workaround:** bring your own PostgreSQL 16 + `pgcrypto` from your organisation's package mirror —
`docs/devops/offline-postgres-install.md` §"Bring your own PostgreSQL 16". Everything after postgres
installs and runs fully offline. **The proper fix needs its own ADR** and is deferred to a later release
rather than smuggled into a release that ships no runtime change.

**R1 is latent, not disproven.** On 2026-10-14 the CEO installed the signed artifacts on a freshly
installed, security-updated Ubuntu 24.04 host and the companion installed cleanly — but only because no
newer `perl-base` update had shipped since the companion was rebuilt on 2026-10-06. The next such update
will trigger it again. Do not read that clean install as the limitation being gone.

---

## Found by PUB-6 and fixed before release (2026-10-14)

PUB-6 is the release's own stranger test: the CEO installed the signed artifacts on a fresh Ubuntu 24.04
virtual machine following only the public `README.md` and what it links. **The product passed every step;
the documents failed.** The full record is `pub6-ceo-run.md`. Three tickets answered it, and PUB-6 is
re-run on a fresh host after the artifacts are rebuilt and re-signed.

- **T-165 — the installer and the demo script.** Re-running `install.sh` now restarts all seven services,
  so a corrected site address or profile actually takes effect; before, Caddy kept serving the old address
  while the installer printed that it served the new one. The database URL is shape-checked and re-asked
  at the prompt instead of being written into five files and failing later at the migration; prompts now
  support arrow-key editing; `setup-loopback-demo.sh bind` refuses anything that is not a resource id,
  with a plain-English message.
- **T-166 — the cockpit.** The Get-started panel shows the `generate` command before "Create loopback
  demo target"; once the target exists it shows the resource id and the exact `bind` command to run. A
  Run's expanded step shows the resource name and says that command output is not stored and the evidence
  is in the Audit timeline. A leftover internal sentence on the Dashboard was removed.
- **T-164 — the documents.** The install guide now starts with the reader's own path (what you need,
  verify, PostgreSQL, install, check it works, then the quickstart) and keeps build, signing and key
  custody in a maintainer section at the end. Verification has one home, in the install guide, and covers
  **both** artifacts — `SECURITY.md` had shown a command for the application bundle only, so a reader
  following it exactly would never have verified the companion that runs as root. Every command that needs
  root carries `sudo`; every placeholder is written so that pasting it unchanged fails loudly; the
  quickstart's paths, resource id and output steps match what now ships; and internal ticket, criterion and
  source-file references were removed from the pages a stranger reads.

---

## Known limitations

- **The postgres companion on patched hosts (R1)** — above. Latent, not fixed; the fix is ADR-0077
  (Proposed) and is planned before v1.0.0 Beta 1.
- **A site address that is not on this host is not warned about (PUB-6 P8).** Typing an IP the server does
  not hold is accepted silently; you find out at the browser. Re-running the installer with the right
  address now corrects it.
- **An operator cannot see a Run's command output (PUB-6 P15b).** Output is never stored — by design for
  now, a deliberate evidence and privacy decision (ADR-0058). The audit trail records that a command ran and
  under which decision, not what it printed. Whether an operator should see the output of a Run they
  launched is a product question that needs its own decision; it is not changed here. A launched Run's
  expanded step also shows the resource, not the command text.
- **The installer's restart is not a health check.** The seven services are systemd `Type=simple` units,
  so a successful `systemctl restart` proves the process was launched, not that it stayed up; a service
  that crashes at start can still be followed by the installer's success line. The install guide's
  step 4 (`systemctl is-active` on all seven) is the check that catches it. Deferred to the next installer
  change.

---

## What this release DID do

Two forces converged on the same question this cycle: a measured finding from v0.15.0 (five separate
instances of a control whose caller was never wired up — see that release's notes) and the decision to
publish this project as an open-source repository. Both are answered by the same work: make the
product's own verification instruments trustworthy, then make the product itself legible to a stranger.

**Five instrument gates, each mutation-proven.**

| Gate | What it closes | Proof it can fail |
|---|---|---|
| `task history-secret-scan` (PUB-1, T-157) | A content-level scan of every blob reachable from every ref — not a filename sweep — before the first push | SEC-60: 3 planted secret shapes (doc, code/test, a realistic DSN beside an already-placeholder-carrying file), all 3 detected and correctly attributed, scratch commits discarded |
| `task block-criterion-check` (VER-41, T-158) | Every `block`-type `SEC-` criterion must name a real test, or an explicit `[test: none -- reason]` | SEC-61: renaming the real backing test made the gate FAIL naming that exact marker; reverted, re-confirmed green |
| Extended `doc-check` (VER-43, T-160) | `docs/releases/**` and `docs/requirements/*.md` now scanned — the release criteria are no longer ungated themselves | SEC-62: a planted broken path under `docs/releases/v0.16.0/` made the gate FAIL naming it exactly; deleted, re-confirmed green |
| `integration_suite_check.py` (VER-42, T-159) | Every `//go:build integration` suite is discovered from the tree, not hand-enumerated, and asserted to have actually RUN (not silently self-skipped) | Discovery mutation (a planted tagged file moved the count and was reverted) + an env-unset run that FAILED loudly naming every self-skipped suite |
| `sign-release.sh`'s mixed-bundle-version guard (VER-45, T-162) | Refuses to sign a `dist/offline/` directory carrying more than one core-bundle version | Proven both ways: a mixed `0.14.0`+`0.15.0` pair dies; one version + the OS-independent postgres companion proceeds |

**A provably clean history.** `infra/scripts/history_secret_scan.py` scanned every blob reachable from
every ref (5392 objects / 2225 blobs at the commit checked) and found zero genuine secrets — a
template-marker + 5-entry exact-pair allowlist discriminates real credentials from the ~34 `change-me`
placeholders and test fixtures already in the tree, rather than blinding whole files to future secrets by
allowlisting paths. This is the release's one irreversible step (PUB-1): once history is public it cannot
be unpublished, and it now carries its own negative control (SEC-60) rather than a first-time-only pass.

**Rot found and fixed, exactly where VER-42 predicted it would be.** Deriving the integration-suite list
from the tree (rather than a hand-maintained enumeration) surfaced two suites that had never run —
`mcp-gateway/internal/server` and `session-broker/internal/store` — and one real defect inside them: a
test's ad-hoc `CREATE TABLE IF NOT EXISTS` silently no-op'd once `api`'s real migration already owned the
`resources` table, corrupting an insert. Fixed at the test-fixture layer, not in product code.

**The first public documents.** `README.md` rewritten for a reader with zero context (what this is, what
it governs, what it does not do, an honest pre-1.0 maturity signal); `SECURITY.md` states the product's
three real limitations in plain language — harness governance is cooperative, not enforced, while B-040
is deferred; R1's post-approval half is open; the audit chain records *that* a policy decision happened,
not *which rule* fired; `CONTRIBUTING.md` carries the DCO 1.1 text and states plainly that PRs are not yet
being accepted; `LICENSE` (Apache-2.0, ADR-0075) + `NOTICE`. The license choice is a binding ADR, not a
default — the product's no-SaaS-runtime doctrine and the desire not to deter contributors both weighed on
it (see ADR-0075 for the rejected alternatives). Contribution provenance is DCO, not a CLA (ADR-0076) —
recorded explicitly that DCO does **not** preserve relicensing optionality; not merging PRs does, and that
option is being spent deliberately, not by accident, for as long as commercialization stays possible.

**Two release-key fingerprints retro-published (PUB-7, T-162).** ADR-0070 §D4 has required the
release public key's fingerprint in the release notes since v0.13.0; it was never landed anywhere a
recipient could read, through v0.13.0 and v0.14.0 both. Both notes now carry a plainly dated
"Retro-published 2026-09-02" section — not a silent backdate.

---

## Verification (VER-46)

WD-1..15 regression, both stacks, plus `task smoke` — see `verification_runbook.md` for the full
transcript. Summary:

| Leg | Result |
|---|---|
| Compose (`task e2e`) | **46 PASS / 0 FAIL** — `ok aireos.local/tests/e2e 420.039s`; audit chain `INTACT+ATTESTED through seq 716` (includes the SEC-43 forged-chain negative proof, both legs) |
| Browser smoke (`task smoke`) | **23 passed / 2 skipped (quickstart-a/b, unaffected by this release) / 0 failed** (3.2m) |
| Installed stack (`task wd1`) | Offline bundle + postgres companion **rebuilt from HEAD** first (T-033 lesson). **8 PASS / 0 FAIL** — `ok aireos.local/tests/e2e 651.917s`; 8 systemd units active, Caddy serves HTTPS; chain `INTACT+ATTESTED through seq 239` before the deliberate final SEC-43 forged-chain leg, which both `audit-service verify` and the offline `aireos-verify` correctly reported `BROKEN` |

---

## Release provenance

**Signed, PUB-6 passed, tagged — not yet pushed** (closure commit, 2026-10-25).

| | |
|---|---|
| Tag | `v0.16.0` → `e43cece6bc33c775b8f64b21617a3467fd9c92cd` — the commit both artifacts' `vcs.revision` attests (`vcs.modified=false`) |
| Release-key fingerprint (SHA-256 of the DER public key) | `968a29b3623fa79a6d12e37dfe4c9e156b6c66c716b0e57a076026a9d1bc0bbf` |
| `aireos-offline-0.16.0.tar.gz` | SHA-256 `e208d12e57d300b922f89977bf8fb9a2c6acd64e0dfc87e9a474c2291c47d146` |
| `aireos-postgres-ubuntu2404.tar.gz` | SHA-256 `61aff02d526c37468c1be38fe506c7c79404adb61ad530c2b26f2715e00f85a6` |

These supersede the first signed set (built from `e7c14ee`), which PUB-6's first run rejected on documents
and which was never tagged. The push is the **last** act and still the CEO's (PUB-9, PUB-10).

**Artifact hashes are deliberately NOT recorded in this commit.** Putting a SHA-256 here and then tagging
this same commit would be circular: the artifacts must be rebuilt from the exact commit the CEO tags
(see `verification_runbook.md` Step 1a), and a rebuild changes both hashes — which would make this
section wrong the moment it was written, exactly the failure mode this release exists to close. Per the
v0.15.0 precedent (tagged commit `53782dd` carried no hashes; they landed in closure commit `8126d34`,
*after* the tag), the fingerprint and both artifact SHA-256s land in the **closure commit**, after
signing, alongside the tag sha itself.

**Both publication blockers are resolved** (2026-09-02): vulnerability reports go through GitHub
private vulnerability reporting, and `NOTICE` names *The AI Remote Engineering OS Project
(github.com/paladinknightmaster)*.

---

## Install-wiring diff: zero

No new daemon, edge, key, network path, or datastore. Every gate added this release is a verification
instrument, not a product code path.

---

## What remains — the CEO's, not this ticket's

✅ Steps 1–3 below are DONE (signed, PUB-6 passed, tagged `e43cece`); only the push remains.

1. **Sign** both artifacts with the production key (custody outside this repo).
2. **PUB-6** — a fresh-host install from the signed artifacts, following only the public `README.md`. It
   must follow signing: the install path says a missing `.sig` means stop. The first run (2026-10-14)
   failed on documents; this one runs on a fresh host after the artifacts are rebuilt from the fixed
   commit and re-signed.
3. **Tag** the commit the artifacts were built from; the sha lands in a closure commit afterward (the
   v0.15.0 "tag + 1" pattern).
4. **Push** — last, only after PUB-6 passes. This repo has no git remote today and must not get one
   before that pass succeeds.

See `verification_runbook.md` for the full handover.
