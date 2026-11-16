# v0.17.0 — HUM-1 record: WAIVED (CEO rehearsal only)

**Status: HUM-1 was not run. It was waived by the CEO on 2026-11-16.** HUM-1 (§6a of
`v0.17.0-release-criteria.md`) requires an install by a tester who is **not** the CEO, following only the
public README, on two OSes. That did not happen for this release. What happened instead is recorded below,
so that nobody later reads v0.17.0 as stranger-tested.

## What was run — the CEO's rehearsal

- **Who:** the CEO — who has followed this project's install guides before, so this run cannot measure
  what a stranger would trip on (the reason HUM-1 exists: v0.16.0's PUB-6 found its documents failed a
  reader who had *not* written them).
- **What:** the signed pre-release `v0.17.0-rc1` (tag → `436b383`, the same artifacts later tagged
  `v0.17.0`), on two Hyper-V VMs, following the published guide.
- **Result, as reported by the CEO:** "tested and looks good."
- ⚠ **Not recorded:** which two OSes were used, whether they were fully updated first, how many server
  command lines were typed, whether the certificate-viewer fingerprint steps could be followed in a real
  browser, and any hesitation points. The Commander asked; the run was reported only in summary. These are
  therefore **unverified** for this release.

## The waiver

**Decided by the CEO, 2026-11-16.** Reasons given: the CEO's own test passed, and the product is to get a
broader, ecosystem-level round of enhancement (less terminal, more browser) before an MVP release, at
which point an external tester will be recruited.

**Consequence, stated rather than drifted into:**

- **v1.0.0 Beta 1 must run a stranger test as a precondition**, not as an aspiration — it will then be the
  first release whose install a non-author has completed.
- Until then, the items HUM-1 exists to check remain open: the three-line count *for a reader who does
  not know the product*, the certificate-viewer steps (`[assumed]` in the guide), the deployment-profile
  question, and the `AIREOS_SITE_ADDRESS` value in the setup line.

## Evidence that does exist (automated, on the tagged artifacts)

`task docs-rehearsal` runs the guide's server lines verbatim on a fresh, fully updated Ubuntu 24.04 host:
3 lines, `ALL VERIFIED`, a setup code, and the setup page answering through Caddy. The later-host test
completes setup and a governed action over HTTP alone on Ubuntu 24.04, Ubuntu 26.04 and Rocky 9. See
`verification_runbook.md`. These prove the mechanics and the guide's commands; they do not prove a
stranger can follow the prose.
