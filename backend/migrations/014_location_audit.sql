-- Owner: Audit. Location creation evidence.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.location_create_audit (
    tenant_id uuid NOT NULL,
    location_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('outlet','warehouse')),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,location_id),
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.locations,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
