-- Owner: Audit.
CREATE TABLE hive.staff_pin_management_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL, member_id uuid NOT NULL, revision bigint NOT NULL,
    action text NOT NULL CHECK (action IN ('profile.create','pin.set','pin.reset')),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships,
    CHECK ((revision=0)=(action='profile.create'))
);
