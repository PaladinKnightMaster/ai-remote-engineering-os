# PUB-6 rehearsal — v0.16.0, artifacts from `a15dc4b`

**This is a rehearsal, NOT PUB-6.** Run by the Commander, who is the opposite of a stranger and will
have filled gaps without noticing. Its job is to catch mechanical defects so the CEO's real pass
tests what only a human can. **It does not discharge PUB-6.**

**Rule applied:** any step requiring knowledge absent from `README.md` and the docs it links is a
**defect, not a note.**

**Setup.** Fresh `ubuntu:24.04` + systemd (the project's own `aireos-wd1-host` image, rebuilt from
`infra/offline/wd1/Dockerfile.host` — the same image GATE-4's §human walkthrough used). Booted with
**`--network none`**: only `lo`, outbound → `000`, for the whole run. Artifacts: core `548a5a48…`,
companion `7f9e4960…`, signed for the rehearsal with the **FIXTURE** key, staged in scratch — the
production key and `dist/offline/` were never touched.

---

## ⚠⚠ The blocker

### R1 — CRITICAL: the postgres companion fails on any host patched after it was built

`install-postgres.sh` (guide step 1) exits **100**:

```
perl : Depends: perl-base (= 5.38.2-3.2ubuntu0.4) but 5.38.2-3.2ubuntu0.6 is to be installed
```

- The companion pins `perl`, `perl-base`, `perl-modules-5.38` at **`…ubuntu0.4`** — its build-time
  patch level.
- A fresh Ubuntu 24.04 host today has **`perl-base …ubuntu0.6`** (a security update).
- apt will not downgrade a core package, so the install cannot proceed.

**Proven causally.** Forcing `perl-base` back to `0.4` — a **test manipulation, never a user path** —
makes the identical, unchanged step succeed completely: postgres 16, pgcrypto, the `cockpit` role and
database. Version skew is the *only* step-1 blocker.

**This is not new.** It has existed since v0.1.0 (ADR-0052's package-closure design).

⭐ **Why fifteen releases never caught it: WD-1 is structurally incapable of detecting it.** WD-1 builds
its test host and the companion from the **same `ubuntu:24.04` snapshot at the same time**, so they
can never drift. A test harness that co-builds the target and the artifact cannot detect skew between
them. Only an independently provisioned host can — which is what this rehearsal was.

**Not fixable by a README edit.** It is an ADR-0052 design issue: a package closure snapshotted at build
time against a target that keeps patching.

---

## Other defects

| ID | Where | What the stranger meets | Evidence | Severity |
|---|---|---|---|---|
| **F1** | README §Install step 1 | *"Build and sign the release artifacts."* A stranger **cannot sign** — the key is the maintainer's. Conflates the maintainer's release flow with a user's install. **No "download a published release" path exists, and no location for published artifacts has been decided.** | [read] | **HIGH** |
| **F3** | install guide :31 | Claims `task offline-bundle` *"builds inside a container on hosts without a local Go toolchain."* **False** — `build-bundle.sh` has zero container references and calls `go build` directly. | [ran] grep → 0 | **HIGH** for anyone building without Go |
| **R2** | `install-postgres.sh` :33-41 | **Reaches for the internet** — adds the local repo *alongside* the host's online sources, then `apt-get update \|\| true` swallows the failure. Air-gapped hosts get `Failed to fetch http://security.ubuntu.com/…`. apt should be isolated to the local repo. | [ran] W: lines | MEDIUM |
| **F4** | install guide step 2 + §Verify | Names **4** systemd units. `install.sh` enables **7** — connector, mcp-gateway, policy are missing. **Confirmed live**: all 7 active. | [ran] ×7 active | MEDIUM |
| **F2** | install guide :1-3 | Titled **"Offline install (v0.1.0)"**, "the **AI ReOS** v0.1.0 cut" — stale by fifteen releases; "AI ReOS" appears nowhere public. | [read] | MEDIUM |
| **F8** | install guide :106 | Says *"compare its fingerprint against the one published in the release notes"* but **never shows how to compute it.** The command lives only in `SECURITY.md`, which the README's install section does not link. | [ran] grep → 0 | MEDIUM |
| **R3** | install guide step 2 | The example `AIREOS_SITE_ADDRESS=cockpit.example.com ./install.sh` **omits the required `AIREOS_DEPLOYMENT_PROFILE`** → fails non-interactively. The error message is clear, which mitigates. | [ran] exit 1 | LOW–MED |
| **R4** | quickstart step 2 | Leaks internal context: repo paths + **line numbers** (*"lines 207–211"*), *"already on `dev`"*, ticket IDs **T-123 / T-124**. The stranger holds a bundle, not the repo. | [read] | LOW–MED |
| **F7** | install guide "What ships" | Core "~28 MB" (real **45 MB**); companion "~80/~87 MB" (real **90 MB**). | [ran] du | LOW |
| **F5** | `CLAUDE.md` | Still says both publication blockers are open — false since `8faec48`. Internal canon. | [read] | LOW |

---

## Verified working — positive results, not absence of errors

- **Signature verification** on both artifacts → `Signature Verified Successfully`. **Tamper control**
  (one byte changed) → `Signature Verification Failure`, exit 1. The check discriminates.
- **Fingerprint** of the rehearsal key correctly differs from the published `968a29b3…` — a stranger
  comparing would catch a wrong key.
- `openssl` present on the host, as the guide claims.
- `install.sh` completes **fully offline**: profile asserted, five env files written, migrations run,
  seven units enabled, audit chain-head key printed (and the guide does mention it — checked).
- Both `SHA256_MANIFEST.txt` checks pass.
- **The cockpit works**: `GET /` → 200 over Caddy HTTPS; bootstrap login → 200; `/v1/auth/me` →
  `operator:admin`, `lan-only`.
- Quickstart step 2 prints exactly the four values it promises.

---

## Not exercised — left for the CEO's real PUB-6

Quickstart steps **3, 5, 6** run in the cockpit's browser UI. Docker Desktop's WSL2 HTTPS port-forwarding
on this Windows host corrupts TLS for native browsers — an environmental fault already recorded in the
ops conventions, not the product. The governed path itself was proven by `task wd1` at T-163 (8/8).
**What remains untested is a stranger following the UI prose** — which is the part only a human can test.

---

## Environment note

Since the last session, **every Docker image this project uses had been removed** (`aireos-wd1-host`,
`aireos-repro-builder`, `golang:1.26`, `playwright`, …); free space rose 9 → 37 GB. The CEO's supabase
databases and all cache volumes survived. Signing needs no images, so the release is not blocked — but
**any rebuild now requires re-pulling `golang:1.26` and recreating `aireos-repro-builder` first.**
