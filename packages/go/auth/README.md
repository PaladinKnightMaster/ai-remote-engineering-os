# packages/go/auth

In-process capability-token adapter (ADR-0046). Issues and verifies **PASETO v4
local** (symmetric) tokens whose claims are the typed `CapabilityToken`
(`sub`, `edge`, `scope`, `iat`, `exp`, `jti`, `aud`, `iss`). No IdP daemon at P0.

Module: `aireos.local/packages/go/auth`. This is the **only** package allowed to
import the PASETO library (`aidanwoods.dev/go-paseto`) — the T-011 `depguard`
rule enforces that. Other packages consume `Signer`, `Verifier`,
`CapabilityToken`, and `AsMiddlewareVerifier`.

## Usage

```go
key, hexKey := auth.GenerateKey()              // provision once (install.sh); store hex
signer := auth.NewSigner(key, "cluster-id")
tok, _ := signer.IssueService("svc:policy-service", "audit-service")  // edge service-service, 1h

verifier := auth.NewVerifier(key, "cluster-id", "audit-service")      // audience = this service
mw.RequireCapability(auth.AsMiddlewareVerifier(verifier), "service-service")
```

`AsMiddlewareVerifier` adapts a `*Verifier` to the `middleware.Verifier` port so
`RequireCapability` (ADR-0043) verifies via PASETO and extracts `edge` + `scope`.

## Tiered lifetimes (ADR-0046)

| Actor | Helper / TTL |
|---|---|
| Operator session | `IssueOperator` — 8h |
| Agent session | `Issue` w/ `AgentTTL` — 5m |
| Service workload | `IssueService` — 1h |
| Egress lease | `Issue` w/ lease expiry + `Scope.LeaseID` |

## Forbidden features at P0 (ADR-0046 — review-enforced)

This package does **not**, and must not at P0, ship:

- password reset UI
- self-service signup
- OAuth / external IdP provider integration
- JWKS-style asymmetric key rotation

If any becomes a load-bearing requirement, that is the trigger to adopt
**Zitadel** (the named P1 reserve), not to grow this package. Revocation at P0 is
by key rotation + short TTLs, not a revocation list.

## Deferred to the `services/api` ticket (ADR-0046 §Hand-off)

- Operator login (`POST /v1/auth/login`, argon2id bootstrap account).
- Signing-key custody + `task auth rotate-key`.
- `install.sh` provisioning of the bootstrap operator + cluster key.
