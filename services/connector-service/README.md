# connector-service

Credential custody + egress gateway (ADR-0053). connector-service is the **sole**
holder of `AIREOS_RESOURCE_SECRET_KEY` and the only service that runs
`pgp_sym_encrypt`/`pgp_sym_decrypt` on target credentials — closing the v0.1.0
SEC-12 debt (target secrets previously decrypted in `services/api` and mirrored
in the broker).

**Status — v0.2.0 (in progress):**

- **T-029a (this slice):** persistence spine — the `connector_credentials`
  custody table, the store, and the provisioning/relocation CLIs. No HTTP
  surface yet.
- **T-029b:** the credential-lease RPC (sync, `edge: service-service`) + the
  transactional outbox + egress-decision audit events.
- **T-030:** re-point consumers — remove decrypt from `services/api` and the
  broker (which leases at connect), drop the legacy `resources.connection_secret`.

GUI/SSH connection *adapters* (the original Guacamole/MeshCentral framing) are a
deferred future, not v0.2.0 scope.

## CLI

```
connector-service migrate up|version|force <v>
connector-service credentials set-secret <resource-id> --key-file <path> [--passphrase <pp>]
connector-service credentials migrate-legacy   # backfill ciphertext off resources.connection_secret
```

Needs `CONNECTOR_DATABASE_URL` (or `DATABASE_URL`); `set-secret` also needs
`AIREOS_RESOURCE_SECRET_KEY` (authority-separate from `AIREOS_PASETO_KEY`).
