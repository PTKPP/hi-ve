-- Owner: Audit. Entitlement administration events.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.entitlement_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    capability_id text NOT NULL,
    actor_id uuid NOT NULL REFERENCES hive.platform_principals,
    action text NOT NULL CHECK (action IN ('grant','revoke')),
    previous_revision bigint NOT NULL,
    revision bigint NOT NULL CHECK (revision=previous_revision+1),
    reason_code text,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id,capability_id,revision)
);
