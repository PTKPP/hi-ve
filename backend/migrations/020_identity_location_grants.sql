-- Owner: Identity. Add scoped grants without changing tenant-wide role semantics.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.tenant_role_location_actions (
    tenant_id uuid NOT NULL,
    role_id uuid NOT NULL,
    action text NOT NULL CHECK (action='devices.operators.use'),
    PRIMARY KEY (tenant_id,role_id,action),
    FOREIGN KEY (tenant_id,role_id) REFERENCES hive.tenant_roles
);
CREATE TABLE hive.tenant_location_role_assignments (
    tenant_id uuid NOT NULL,
    member_id uuid NOT NULL,
    role_id uuid NOT NULL,
    location_id uuid NOT NULL,
    PRIMARY KEY (tenant_id,member_id,location_id,role_id),
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,role_id) REFERENCES hive.tenant_roles,
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.locations
);
CREATE TABLE hive.location_rbac_receipts (
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    target_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('role.location_actions.replace','membership.location_roles.replace')),
    request_key text NOT NULL,
    fingerprint bytea NOT NULL,
    result jsonb,
    PRIMARY KEY (tenant_id,actor_id,target_id,operation,request_key),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
