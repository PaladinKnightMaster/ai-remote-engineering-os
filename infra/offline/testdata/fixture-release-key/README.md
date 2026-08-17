# FIXTURE release keypair — NOT for a real release

This directory holds a **throwaway Ed25519 keypair** used only to exercise
`infra/offline/sign-release.sh` and `infra/offline/check-provenance.sh`
(ADR-0070 D1–D4, D9; T-129a) in tests and manual verification. It is
deliberately generated in-repo, committed, and documented — the opposite of
how the real release key must be handled.

| File | Contents |
|---|---|
| `FIXTURE-DO-NOT-USE-release.key.pem` | fixture Ed25519 **private** key |
| `FIXTURE-DO-NOT-USE-release.pub.pem` | fixture Ed25519 **public** key |

Fixture public key fingerprint (SHA-256 of the DER-encoded SPKI, same
computation `sign-release.sh` prints for a real key):

```
f6cb5349074e94e9418451a16fc39918deb1780305348f5e58d331ecd1bb3a28
```

**This keypair signs nothing real.** Anyone holding this repo already holds
this private key — that is disqualifying for a release key by definition
(ADR-0070 D3: "held by the maintainer, never in the repository"). Use it only
to prove the sign/verify mechanism works end to end; never point
`RELEASE_PUBLIC_KEY` at `FIXTURE-DO-NOT-USE-release.pub.pem` when verifying an
artifact you intend to ship or trust.

## The real release key is not created by any script in this repo

The CEO mints the production release keypair **out-of-band**, on a machine
that never runs untrusted build code:

```sh
openssl genpkey -algorithm ed25519 -out aireos-release.key
openssl pkey -in aireos-release.key -pubout -out aireos-release.pub
```

The private key is then held outside this repository and outside any build
host, per ADR-0070 D3. The public key and its fingerprint are published in
the release notes and documented in
`docs/devops/offline-install-v3.md` ("Release key custody"). See that file
for the full procedure, including where the private key must **never** live.
