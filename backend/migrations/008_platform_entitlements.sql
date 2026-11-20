-- Owner: Platform. Explicit tenant capability grants and immutable revision history.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.tenant_entitlements (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    capability_id text NOT NULL CHECK (length(capability_id) > 0),
    effective_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > effective_at),
    source_reference text NOT NULL,
    revoked_at timestamptz,
    revision bigint NOT NULL CHECK (revision > 0),
    PRIMARY KEY (tenant_id, capability_id)
);
CREATE TABLE hive.tenant_entitlement_history (
    tenant_id uuid NOT NULL,
    capability_id text NOT NULL,
    revision bigint NOT NULL,
    snapshot jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, capability_id, revision),
    FOREIGN KEY (tenant_id,capability_id) REFERENCES hive.tenant_entitlements
);
CREATE TABLE hive.tenant_entitlement_receipts (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    capability_id text NOT NULL,
    actor_id uuid NOT NULL REFERENCES hive.platform_principals,
    operation text NOT NULL CHECK (operation IN ('grant','revoke')),
    request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    result jsonb,
    PRIMARY KEY (tenant_id,capability_id,actor_id,operation,request_key)
);
