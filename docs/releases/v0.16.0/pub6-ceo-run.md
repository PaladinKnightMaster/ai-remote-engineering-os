# PUB-6 — the CEO's fresh-host run (2026-10-14)

**Result: the product passed every step; the documents FAILED the stranger test.** PUB-6 is therefore
**not discharged**. The fix pass is T-164…T-166 (criteria §10), and PUB-6 re-runs on a fresh host
afterwards.

**Setup.** Hyper-V VM, Ubuntu Server 24.04 installed by the CEO from the official ISO, default options,
security updates applied by the installer (an independently provisioned, *patched* host — the condition
WD-1 cannot produce). The production-signed `e7c14ee` artifacts (core `28c4ad23…`, companion `3a6050ef…`)
plus `aireos-release.pub`, copied in by `scp` in place of the not-yet-published Releases page. The CEO
followed `README.md` and the documents it links; the Commander answered only when the CEO was stuck, and
every such answer is recorded below as a finding.

⚠ **Commander contamination, stated:** from P1 onward the Commander pointed the CEO to the right section
when the guide's order misled; from P12 onward the Commander supplied a workaround command. Everything
after those points is "the product works", not "a stranger succeeds".

---

## What passed — positive results

| Step | Result |
|---|---|
| Fingerprint | printed exactly the published `968a29b3623fa79a…` |
| Signatures | `Signature Verified Successfully` on both artifacts |
| Companion install | PostgreSQL 16 + pgcrypto from the companion's `file:` repo only; role + database created. ⓘ **R1 did not trigger** — no newer `perl-base` has shipped since the 2026-10-06 rebuild, so the exact-pin chain was never exercised. **R1 is latent, not disproven** (ADR-0077 still needed). |
| `install.sh` | profile asserted first; keys preserved across a failed run (idempotent); migrations; 7 units enabled; chain-head public key printed. A raw `@` in the database password worked. |
| Cockpit | HTTPS login; header and "Get started" panel exactly as the quickstart describes |
| Demo target | generate → Create → bind; the panel's own "Next: launch … from Runs" hint |
| Governed action | a one-step `whoami` Run → `completed` |
| Audit | `governed-actions@1` preset, newest first; `run.launched`, `tool.result`, `run.completed` present, every actor `operator:ceo` (10-position chain) |
| Clean-up + Verify | demo target removed in the cockpit; `--undo` complete; `sha256sum -c --quiet` exit 0 on both unpacked artifacts; 7/7 units `active` |

---

## Findings — ranked

| ID | Where | What happened | Severity |
|---|---|---|---|
| **P1** | `docs/devops/offline-install-v3.md` structure | README step 3 sends the user here; the guide opens with **Build** (`task offline-bundle`) → Sign → Release key custody, and the user's section ("Install (target host)") is ~100 lines down with no signpost. **The CEO ran `task offline-bundle` on the target twice** (`task` not found; Ubuntu suggests `taskwarrior`, an unrelated package). | **HIGH** |
| **P2** | `SECURITY.md` §Verifying | says verify **each** artifact and that the companion "matters most" (it runs as root) — but shows a command for the **core bundle only**. Followed exactly, **the companion went unverified.** | **HIGH (security)** |
| **P9** | `infra/offline/install.sh` (systemd step) | **A re-run never restarts running units** — `systemctl enable --now` is a no-op on an active unit, and Caddy reads `AIREOS_SITE_ADDRESS` only at start (`infra/offline/Caddyfile:17`). After correcting a typo'd address, Caddy kept serving the old one **while the installer printed "Caddy serves https://<new address>" — a false success message.** Same for a changed deployment profile. Found by the Commander reading the code before sending the CEO to the browser. | **HIGH** |
| **P12** | quickstart step 4 | needs the demo resource's id; says *"hover/inspect the resource … or read it back from the script's own state"*. **The cockpit never renders the id** (only an `<option value>`, `apps/web/src/routes/Runs.tsx`), and the script stores it only *after* `bind`. A stranger is stuck; the CEO used a `psql` query the Commander supplied. | **HIGH** |
| **P15** | quickstart step 5 | *"expand its step detail and confirm the command's output"* — the expanded step shows only the resource UUID and status. **stdout is never stored, by design (ADR-0058)**, so no screen can show it. P15a: the doc claim is false and the row is unreadable (UUID, no command). P15b: whether an operator should see the output of a Run they launched is a product question → its own ADR, not this release. | **HIGH** (doc) / product question |
| **P5** | install guide step 2 example | the CEO ran `AIREOS_SITE_ADDRESS=cockpit.example.com` **verbatim** — a realistic-looking example with no "replace this" instruction. | **HIGH** |
| **P10** | README → quickstart; Dashboard | logged in, the CEO: *"still confusing what the next step is and how to do it."* No pointer from the finished install to the quickstart (the only one is a `#` comment inside a code block), and **"Create loopback demo target" depends on a host-side script the UI never mentions.** | **HIGH** |
| **P14** | `infra/offline/setup-loopback-demo.sh` bind | a placeholder (`PASTE-THE-ID`, the Commander's) was pasted literally — **the second verbatim-placeholder event after P5**. The script forwarded it → raw `SQLSTATE 22P02`. Validate the UUID; plain-English error. | MEDIUM |
| **P4** | install guide, quickstart | "as root" in a heading, but no command carries `sudo`; Ubuntu's default user is a sudoer. Both installers stopped with `must run as root`; the CEO added `sudo` unaided. | MEDIUM |
| **P7** | `install.sh` database URL | `cockpit` (not a URL) was accepted, **all five env files written**, then migration failed with a hint naming the wrong cause (*"is postgres 16 reachable…?"*). Validate the URL shape at the prompt; re-ask. | MEDIUM |
| **P6** | `install.sh` prompts | no line editing — arrow keys print `^[[D`; the CEO could not edit a pasted template URL. | MEDIUM |
| **P11** | quickstart step 2 | script path given as `/opt/aireos-offline-<version>/`, then "assume you copied it to `/root/…`" — neither is where the guide had the user unpack. `useradd` without `sudo`. | MEDIUM |
| **P13** | shipped UI, `apps/web/src/routes/Dashboard.tsx` | *"The resource picker lands with its feature ticket."* — internal ticket language in the product itself. | MEDIUM |
| **P3** | install guide vs `SECURITY.md` | verification documented twice, differently; the CEO, having done it via SECURITY.md, found the guide's block confusing. One canonical place. | MEDIUM |
| **C1–C7** | install guide, quickstart | internal references a stranger cannot use: ticket ids (T-021a/b/c, ONB-3, DOC-24, OI-24), "before this ticket", "CI / WD-1", source paths and function names (`OnboardingChecklist.tsx`, `store.go`'s `Finish`, `services/api/migrations/0002`, …). Seen by the Commander, not raised by the CEO. | MEDIUM |
| **P8** | `install.sh` site address | a typo'd IP not on this host was accepted silently. A warning is cheap. | LOW |

---

## Lessons

1. ⭐ **The rehearsal could not have found P1, P10 or P12** — the Commander knows where every section is
   and what every id is. Three of the four HIGH documentation findings needed a real outsider.
2. ⭐ **P9 and P15 are the release's own defect class, again: a success claim nobody had checked against
   the thing claimed.** P9's message reads its *input*, not the running service; P15's sentence describes
   a screen that was never built.
3. **People copy placeholders verbatim** (P5, P14). A placeholder must be impossible to mistake for a real
   value — and preferably fail loudly if pasted unchanged.
4. **Commander instrument error, stated:** the first manifest check was piped to `tail -3`, hiding the exit
   code (ops rule: never pipe a gate). Re-run unpiped: exit 0 on both.

---

## Re-run — PASSED (recorded 2026-10-25)

After T-165 (installer), T-166 (cockpit) and T-164 (documents), both artifacts were rebuilt from `7ca592f` (published as `e43cece`),
re-signed and re-gated (`verification_runbook.md`, Re-cut). The CEO reverted the VM to its `fresh-install`
checkpoint and followed `README.md` again: *"works well"*, with the CA-root path confirmed on the host and no
friction reported. **PUB-6 is discharged; `v0.16.0` is tagged at `7ca592f` (published as `e43cece`).**

⭐ The CEO's follow-up after the pass — make the guides friendlier for less terminal-fluent users and move
terminal steps into the browser — is recorded as the direction for the next release, not as a v0.16.0 defect.
