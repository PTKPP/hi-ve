-- Owner: Locations. Real tenant-scoped outlet/warehouse IDs for Phase 02 and beyond.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.locations (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 128),
    kind text NOT NULL CHECK (kind IN ('outlet','warehouse')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    PRIMARY KEY (tenant_id,id),
    CONSTRAINT locations_tenant_code_unique UNIQUE (tenant_id,code)
);
CREATE TABLE hive.location_create_receipts (
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    result jsonb,
    PRIMARY KEY (tenant_id,actor_id,request_key),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
