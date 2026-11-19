-- Owner: Audit. A successful explicit logout is recorded with no session digest.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.session_logout_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope text NOT NULL CHECK (scope IN ('platform','tenant')),
    tenant_id uuid REFERENCES hive.tenants,
    actor_id uuid NOT NULL REFERENCES hive.identity_principals,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((scope='tenant') = (tenant_id IS NOT NULL)),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
