# v0.13.0 — verification runbook

**Release: "Evidence Hardening". Gates: GATE-1…GATE-5.**

Steps 0–2 are automated and were recorded at **T-136a**. Steps 3–5 are **yours** — they need the
production release key and the `§human` offline verification, neither of which any automated run may
perform.

---

## Step 0 — sync the RC

```bash
git -C /d/1_PROJECT/PRIVATE_WORK/AI-Agentic-OS/ai-remote-engineering-os log --oneline -1
```

`main` and `dev` must be equal and the tree clean. **No commit may land after the build in Step 1** —
a docs-only commit changes every binary's `vcs.revision` and therefore the artifact hash (measured at
T-130b). If anything is committed after the build, rebuild before signing.

---

## Step 1 — the automated gates (recorded at T-136a)

| Gate | What | Where recorded |
|---|---|---|
| **GATE-1** | S-21 security-audit capstone — PASS, one LOW finding (F-1) | `v0.13.0-security-capstone.md` |
| **GATE-3** | SEC-43 + SEC-45, both shown failing correctly | capstone §1 |
| **GATE-4** | Quickstart from the built bundle on a fresh host | T-133a/b |
| **GATE-2** | WD-1..11 both stacks + smoke | this runbook, Step 2 |

---

## Step 2 — GATE-2, the full regression

Run against the **same build** that will be signed:

```bash
task e2e
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/wd1/run-wd1.sh
```
```bash
MSYS_NO_PATHCONV=1 task smoke
```

Assert the **positive** markers, never the absence of failures: `ok aireos.local/tests/e2e`,
`WD1_EXIT=0`, and `INTACT+ATTESTED` from the chain verify. A run that merely prints no `FAIL` has not
passed.

---

## Step 3 — mint the release key and sign (**CEO, out-of-band**)

**No script in this repo creates this key**, by design (ADR-0070 D3). On a machine that does not run
build code:

```bash
openssl genpkey -algorithm ed25519 -out aireos-release.key
```
```bash
openssl pkey -in aireos-release.key -pubout -out aireos-release.pub
```

Sign both shipped artifacts:

```bash
task sign-release -- --key /path/to/aireos-release.key
```

Then **both** provenance gates, against the real key. This is capstone finding **F-1**'s obligation:
`task release-check` green does **not** imply provenance was verified, because its provenance leg
self-skips when `dist/offline` is empty.

```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task provenance-check
```
```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task sec45-check
```

Record the public key's SHA-256 fingerprint — `sign-release` prints it — for the release notes.

---

## Step 4 — GATE-5, the `§human` offline verification

**This is the release's entire claim: a stranger, with no database and no product install, can prove
our audit chain is authentic.** Drive it as a third party would.

**4.0 — Make sure the chain has something in it.** A fresh stack may have an empty chain, and
verifying zero events proves nothing. Log in to the cockpit and perform one governed action first
(creating the demo target is enough). Confirmed during T-136a: a login plus a resource creation
yields a verifiable chain.

**4.1 — On the installed host, export the evidence.**

The `export` subcommand needs the same environment `systemd` gives the service — it is **not**
self-configuring. Read the values with `grep`/`cut`, never by `source`-ing the file (a config file is
data, not code):

```bash
DB=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
```
```bash
AN=$(grep "^AIREOS_AUDIT_ANCHOR_PATH=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
```
```bash
sudo DATABASE_URL="$DB" AIREOS_AUDIT_ANCHOR_PATH="$AN" /opt/aireos/bin/audit-service export > evidence.ndjson
```

⚠ **The export must be the only thing writing to that file.** If you run `checkpoint` in the same
redirect its output lands in the export and line 1 stops being valid JSON.

Confirm line 1 is a manifest carrying `formatVersion`, `eventCount`, `attestedThroughSeq`,
`anchorAvailable` — and a key **fingerprint only**, never the key itself.

**4.2 — Read the public key from the installed host.**

```bash
grep "^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=" /etc/aireos/aireos.env | cut -d= -f2-
```

A different key from the release key in Step 3 — per-install and machine-minted, versus per-project
and human-held. Never substitute one for the other.

**4.3 — Move to a machine with NO database access and NO product install.**

Copy only two things: `evidence.ndjson` and the public key string. Take `aireos-verify` from the
bundle's `bin/`. Copying anything else weakens the test.

**4.4 — Verify as a third party.**

```bash
./aireos-verify --pubkey <the-hex-public-key> evidence.ndjson
```

**Expected:**

```
chain:  INTACT+ATTESTED — N event(s) verified, attested through seq N
anchor: IN_SYNC — the anchor's last recorded head agrees with the exported heads
verdict: OK (exit 0)
```

**4.5 — Prove it can fail.** Do not skip: a verifier you have not seen refuse is not known to work.
Change any actor or value inside `evidence.ndjson` and re-run:

**Expected:**

```
chain:  BROKEN — first divergence at event index 0 (seq=1 id=...)
verdict: TAMPERED (exit 1)
```

**4.6 — Confirm what was *not* required.** You used no database, no credentials, no running service
and no network. If any of those turned out to be necessary, GATE-5 has failed regardless of the
verdict printed.

**4.7 — The two tests that make 4.6 provable rather than asserted.** 4.6 as originally written was an
assertion; a `§human` reviewer correctly asked how to *test* it. Both were run at T-136a.

**Test A — destroy the stack, then verify again.** The strongest form of "no database, no service, no
product install": delete every container and network, then re-run 4.4 against a clean export.

```bash
docker rm -f aireos-demo-host aireos-demo-sshd && docker network rm aireos-demo
```

**Expected — unchanged, with the product erased:**

```
chain:  INTACT+ATTESTED — N event(s) verified, attested through seq N
verdict: OK (exit 0)
```

⚠ Take the clean export **before** destroying the stack, and keep it separate from the file mutated
in 4.5. (Learned the hard way at T-136a: this test was first run against the tampered copy and
reported BROKEN for the wrong reason.)

**Test B — verify with the WRONG key. This is the test that proves the *signature*.**

4.5 proves the hash chain detects tampering — but a hash chain would catch that with no signatures at
all, so 4.5 alone does not demonstrate what this release added. A wrong key isolates it:

```bash
./aireos-verify --pubkey $(openssl rand -hex 32) evidence.ndjson
```

**Expected:**

```
chain:  BROKEN — head at seq N was signed under key id <id>, which does not match
        any of the 1 supplied key(s) (a rotation/operator question per ADR-0069 D5,
        not by itself a tamper finding — but this key cannot verify it)
verdict: TAMPERED (exit 1)
```

The verifier names the key id it expected and refuses — and distinguishes a rotation question from a
tamper finding rather than crying wolf. **This is the difference between v0.13.0 and every release
before it: the chain now proves authenticity, not merely ordering.**

> **Windows note:** git-bash `/tmp` is not a path Docker can bind-mount. If you run the clean room as
> a container, stage the files under the repo and mount that directory instead.

---

## Step 5 — sign-off and tag

Sign off only if Steps 2–4 are green **and** the release notes' non-claims still read as true.

```bash
git tag -a v0.13.0 -m "v0.13.0 — Evidence Hardening"
```

**The tag is local. This repo has no remote — never push, including tags.**

Tag the commit the artifacts were **built from**. If any commit landed after the build, go back to
Step 1: the artifact you are about to attest would describe a tree that no longer exists.
