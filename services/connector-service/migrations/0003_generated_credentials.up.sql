-- connector-service schema, migration 0003 — connector-generated target keys (ADR-0079).
--
-- connector-service now GENERATES target SSH key pairs itself (ed25519) and keeps
-- the private half; api only ever sees the public half.
--
-- public_key: the OpenSSH "ssh-ed25519 AAAA..." line of a generated credential, in
-- plaintext (it is public). NULL for credentials imported with `set-secret` (the
-- private half was supplied by the operator; no public half is recorded).
--
-- connector_pending_credentials: a generated key not yet bound to a resource, keyed
-- by LABEL (the loopback demo's "loopback-demo"). It is a SEPARATE table on purpose:
-- the lease reads connector_credentials only, so a pending key is never leasable by
-- construction, not by a flag someone could forget to check. account/host_key are
-- non-secret metadata the operator-run installer records so the cockpit can create
-- the resource without any paste.
ALTER TABLE connector_credentials ADD COLUMN public_key TEXT;

CREATE TABLE connector_pending_credentials (
    label          TEXT        PRIMARY KEY,
    private_secret BYTEA       NOT NULL,   -- pgp_sym_encrypt(material, AIREOS_RESOURCE_SECRET_KEY), same shape as connection_secret
    public_key     TEXT        NOT NULL,
    account        TEXT        NOT NULL,   -- non-secret: the demo account name
    host_key       TEXT        NOT NULL,   -- non-secret: the host's SSH host public key line
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
