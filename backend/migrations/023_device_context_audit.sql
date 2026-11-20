-- Owner: Audit. Device assignment and scoped logout events.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.operational_device_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL, device_id uuid NOT NULL, actor_id uuid NOT NULL,
    action text NOT NULL CHECK(action IN ('assignments.replace','context.logout')),
    revision bigint NOT NULL, context_id uuid, outlet_id uuid, assignment_count integer,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    CHECK ((action='context.logout' AND context_id IS NOT NULL AND outlet_id IS NOT NULL AND assignment_count IS NULL)
        OR (action='assignments.replace' AND context_id IS NULL AND outlet_id IS NULL AND assignment_count IS NOT NULL))
);
