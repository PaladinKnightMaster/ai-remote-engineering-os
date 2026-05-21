-- services/api schema, migration 0001. Standard up/down (services/api is NOT
-- forward-only; only audit-service is, per ADR-0044).

CREATE TABLE resources (
    id          UUID PRIMARY KEY,
    type        TEXT NOT NULL,
    name        TEXT NOT NULL,
    site_id     TEXT,
    labels      JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT resources_type_chk CHECK (type IN (
        'rdp','vnc','ssh','meshcentral','workspace','agentic_desktop'
    ))
);
