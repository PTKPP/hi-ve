-- Owner: Identity. Tenant-wide authorization policy for existing memberships.
SET LOCAL lock_timeout = '5s';
CREATE TABLE hive.tenant_rbac_revisions (
    tenant_id uuid PRIMARY KEY REFERENCES hive.tenants,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0)
);
CREATE TABLE hive.tenant_roles (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    role_key text NOT NULL CHECK (role_key NOT IN ('primary_owner', 'primary-owner')),
    name text NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, role_key)
);
CREATE TABLE hive.tenant_role_actions (
    tenant_id uuid NOT NULL,
    role_id uuid NOT NULL,
    action text NOT NULL CHECK (action = 'identity.roles.read'),
    PRIMARY KEY (tenant_id, role_id, action),
    FOREIGN KEY (tenant_id, role_id) REFERENCES hive.tenant_roles
);
CREATE TABLE hive.tenant_role_assignments (
    tenant_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    role_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, principal_id, role_id),
    FOREIGN KEY (tenant_id, principal_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id, role_id) REFERENCES hive.tenant_roles
);
CREATE TABLE hive.tenant_role_create_receipts (
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    result jsonb,
    PRIMARY KEY (tenant_id, actor_id, request_key),
    FOREIGN KEY (tenant_id, actor_id) REFERENCES hive.tenant_memberships
);
