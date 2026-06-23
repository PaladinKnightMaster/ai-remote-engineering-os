# v0.4.0 — CI/CEO verification runbook

How to verify + tag v0.4.0 ("Hardened Agent Foundation"). The in-session work is
done and Dockerized-verified per rung (`task e2e` green every rung); the gates below
need a **build host with native Go 1.23 + Node 22/pnpm + Docker (privileged)** —
the dev host used for authoring has neither Go nor python3, so these couldn't run
there (same gate class as the v0.2.0/v0.3.0 §human walkthroughs).

Release-candidate commit: **`85aa640`** (`dev` = `main`). No remote; nothing pushed.

---

## Step 0 — sync the RC

```sh
cd ai-remote-engineering-os
git checkout main && git log --oneline -1     # expect: 85aa640 T-053b: DOC-12 + v0.4.0 release artifacts ...
git status --porcelain                        # expect: clean
```

## Step 1 — rebuild the offline bundle FROM HEAD  ⚠ mandatory

`dist/` currently holds the **stale v0.3.0** bundle; the t033 lesson is to rebuild
before any WD run (it grabs the new install.sh, the keyless gateway unit, and the
`authctl genkeypair`/`genaeskey` subcommands).

```sh
task offline-bundle           # pnpm install + web build + build-bundle.sh (BUNDLE_VERSION=0.4.0)
ls -la dist/offline/aireos-offline-0.4.0.tar.gz
```
**Expect:** `dist/offline/aireos-offline-0.4.0.tar.gz` produced; build log reports the
size under the OI-4 caps (≈115–120 MB unpacked / ≈41 MB gz vs 500/150). Bundle binTree
includes `authctl` + `mcp-gateway`. (Postgres companion `aireos-postgres-ubuntu2404.tar.gz`
should already be in `dist/offline/`; else `bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404`.)

## Step 2 — installed WD-4 (`task wd1`)  ← the keyless-gateway proof

```sh
task wd1                      # systemd-in-Docker: real install.sh → all units → WD-4 chain
```
**Expect (exit 0), key lines:**
- `systemctl is-active` lists all **7 units + postgresql** active (incl. `aireos-mcp-gateway`).
- `SEC-20 OK — gateway keyless: own sign + F3 command + api-public; no cluster key; loads gateway.env only`
- `PASS WD-3 installed — agent (Caddy /mcp) → gateway block-and-wait → operator approve → broker exec ...`
- `audit-service verify` intact; all WANTS outcomes present (incl. `tool.invoked` / `remote.exec` / `tool.result`).
- final: `T-053a PASS — WD-4 on the installed stack ... KEYLESS gateway (SEC-20) ...`

This proves install.sh keyed the stack correctly and the **keyless gateway drives the
full governed chain** on the production wiring.

## Step 3 — the machine gate (`task release-check`)

```sh
task release-check            # lint / sec13-gate / licenses / test / integration / typecheck / web build / codegen drift / e2e / smoke
task doc-check                # doc-ref resolution (needs python3)
```
**Expect green throughout.** Spot-checks worth eyeballing:
- `task e2e` → `PASS WD-4/SEC-21 — the keyless gateway's own key forges nothing connector or api accepts (401/401); blast radius bounded` and `audit chain intact: 35 event(s) verified`.
- `task sec13-gate` → `SEC-13(a) gate OK — no decrypt path or key read outside services/connector-service` (confirms F3's app-level AES didn't introduce `pgp_sym_decrypt`).
- `task licenses` → unchanged set (zero new deps; corroborated by an empty `git diff v0.3.0..HEAD -- '**/go.mod' '**/go.sum'`).

## Step 4 — §human WD-4 (interactive feature test)

Stand up a **persistent** installed stack (no teardown; Caddy published to
`localhost:8443`; `fire-agent.sh` staged), then drive it:

```sh
bash infra/offline/wd1/bringup-demo.sh    # → https://localhost:8443 (admin / demo-operator-pw)
```

Then, through Caddy HTTPS:

1. **Log in** as the bootstrap operator (browser; accept the self-signed local CA).
2. **Cockpit (Part A):** Sessions → pick `demo-target` → Create → it parks → **Approve**
   → terminal → run a command → End. Then create a second → **Deny** (blocked).
   Check **Audit** for the chained timeline.
3. **Agent surface (Part B):** drive an agent call (it parks + blocks ~125s):
   ```sh
   docker exec aireos-demo-host fire-agent.sh            # or: fire-agent.sh "echo your command"
   ```
4. In the browser, open **`/invocations`** → you see the agent + the **verbatim command**
   (F3: stored encrypted, decrypted only for you here) → **Approve** within ~2 min →
   `fire-agent` returns the stdout (executed by the **keyless** gateway). Run it again
   and **Deny** → typed error, no exec.
5. **Keyless hand-check (Part C / SEC-20):**
   ```sh
   docker exec aireos-demo-host grep AIREOS_PASETO_KEY /etc/aireos/gateway.env   # → nothing
   docker exec aireos-demo-host systemctl cat aireos-mcp-gateway | grep EnvironmentFile  # → gateway.env only
   ```

**Tear down when done:**
```sh
docker rm -f aireos-demo-host aireos-demo-sshd; docker network rm aireos-demo; docker volume rm aireos-demo-keys
```

## Step 5 — tag (local; CEO decision)

After Steps 1–4 pass and you're satisfied:

```sh
git tag -a v0.4.0 85aa640 -m "v0.4.0 — Hardened Agent Foundation (keyless agent gateway; per-workload asymmetric keys; F3; SEC-21)"
git tag --list 'v0.*'         # local only — DO NOT push
```

---

## Notes
- **No remote / no push** — all tags are local annotated, per repo policy.
- The cumulative **security red-team over T-045…T-052 is already done** (clean, no code fix); only the T-053 closure code/docs were added after — Step 3's `release-check` re-covers them.
- If anything fails, capture the failing command's output and hand back — the fix folds into a follow-up; do **not** tag on a red gate.
- After the tag: **v0.5.0** opens (Workstreams or Memory — the next P1 modules, now on the isolated-key foundation).
