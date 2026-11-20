-- Owner: Devices. Online-only challenge, credential, outlet links and receipts.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.device_pairings (
    id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{48}$'),
    public_key jsonb NOT NULL,
    thumbprint bytea NOT NULL CHECK (octet_length(thumbprint)=32),
    start_key text NOT NULL,
    code_key_id text NOT NULL,
    nonce text NOT NULL,
    expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '10 minutes',
    tenant_id uuid REFERENCES hive.tenants,
    actor_id uuid,
    approval_key text,
    approval_fingerprint bytea,
    redeem_key text,
    redeem_proof bytea,
    bad_codes smallint NOT NULL DEFAULT 0 CHECK (bad_codes BETWEEN 0 AND 5),
    bad_proofs smallint NOT NULL DEFAULT 0 CHECK (bad_proofs BETWEEN 0 AND 5),
    UNIQUE (thumbprint,start_key),
    UNIQUE (tenant_id,id),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    CHECK ((tenant_id IS NULL AND actor_id IS NULL AND approval_key IS NULL AND approval_fingerprint IS NULL)
        OR (tenant_id IS NOT NULL AND actor_id IS NOT NULL AND approval_key IS NOT NULL AND
            approval_fingerprint IS NOT NULL AND octet_length(approval_fingerprint)=32)),
    CHECK ((redeem_key IS NULL AND redeem_proof IS NULL) OR
        (tenant_id IS NOT NULL AND redeem_key IS NOT NULL AND redeem_proof IS NOT NULL AND octet_length(redeem_proof)=32))
);
CREATE INDEX device_pairings_expiry ON hive.device_pairings (expires_at,id);
CREATE TABLE hive.device_pairing_outlets (
    tenant_id uuid NOT NULL,
    pairing_id text NOT NULL,
    outlet_id uuid NOT NULL,
    PRIMARY KEY (tenant_id,pairing_id,outlet_id),
    FOREIGN KEY (tenant_id,pairing_id) REFERENCES hive.device_pairings (tenant_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,outlet_id) REFERENCES hive.locations (tenant_id,id)
);
CREATE TABLE hive.devices (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    pairing_id text NOT NULL UNIQUE,
    public_key jsonb NOT NULL,
    thumbprint bytea NOT NULL CHECK (octet_length(thumbprint)=32),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    revoked_at timestamptz,
    PRIMARY KEY (tenant_id,id)
);
CREATE UNIQUE INDEX devices_active_key ON hive.devices (thumbprint) WHERE revoked_at IS NULL;
CREATE TABLE hive.device_outlets (
    tenant_id uuid NOT NULL,
    device_id uuid NOT NULL,
    outlet_id uuid NOT NULL,
    PRIMARY KEY (tenant_id,device_id,outlet_id),
    FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices (tenant_id,id),
    FOREIGN KEY (tenant_id,outlet_id) REFERENCES hive.locations (tenant_id,id)
);
CREATE TABLE hive.device_pairing_limits (
    scope text NOT NULL CHECK (scope IN ('peer','key','owner')),
    subject bytea NOT NULL CHECK (octet_length(subject)=32),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attempts timestamptz[] NOT NULL DEFAULT ARRAY[]::timestamptz[] CHECK (cardinality(attempts)<=10),
    PRIMARY KEY (scope,subject)
);
CREATE INDEX device_pairing_limits_window ON hive.device_pairing_limits (updated_at);
CREATE TABLE hive.device_revoke_receipts (
    tenant_id uuid NOT NULL,
    device_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    request_key text NOT NULL,
    expected_revision bigint NOT NULL,
    result_revision bigint NOT NULL,
    PRIMARY KEY (tenant_id,device_id,actor_id,request_key),
    FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices (tenant_id,id),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
