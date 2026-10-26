# No fixture release key is committed here (T-168)

This directory used to hold a throwaway Ed25519 keypair, committed on purpose
and labelled `FIXTURE-DO-NOT-USE-*`, to exercise `infra/offline/sign-release.sh`
and `infra/offline/check-provenance.sh` (ADR-0070 D1–D4, D9; T-129a). It was
**removed** because:

- nothing in the repo could still sign against it — the one consumer that used
  its public half, `infra/offline/sec45_bundle_mutation_test.sh`, now requires
  `RELEASE_PUBLIC_KEY` and has no default; and
- a PEM private key in a public repository trips secret scanners (GitGuardian
  raised an incident on it), whatever its label says.

It signed nothing real, so nothing was rotated. The two files remain in git
history only.

## Throwaway keys for a manual test

Mint your own pair in a scratch directory **outside the repository**, sign with
it, and point the gates at the matching public key:

```sh
d="$(mktemp -d)"
openssl genpkey -algorithm ed25519 -out "$d/test.key"
openssl pkey -in "$d/test.key" -pubout -out "$d/test.pub"

bash infra/offline/sign-release.sh --key "$d/test.key"
RELEASE_PUBLIC_KEY="$d/test.pub" bash infra/offline/check-provenance.sh
RELEASE_PUBLIC_KEY="$d/test.pub" bash infra/offline/sec45_bundle_mutation_test.sh
```

A throwaway key proves the sign/verify mechanism works end to end; **never use
one to vouch for an artifact you intend to ship or trust.**

## The real release key is not created by any script in this repo

The CEO mints the production release keypair **out-of-band**, on a machine that
never runs untrusted build code (same two `openssl` commands as above, with the
output files named `aireos-release.key` / `aireos-release.pub`). The private key
is then held outside this repository and outside any build host, per ADR-0070
D3. The public key and its fingerprint are published in the release notes and
documented in `docs/devops/offline-install-v3.md` ("Release key custody").
