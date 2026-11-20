-- Owner: Devices. Online context and assignments. No credential issuer is enabled.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.devices ADD COLUMN context_revision bigint NOT NULL DEFAULT 0 CHECK (context_revision>=0);
ALTER TABLE hive.devices ADD COLUMN last_outlet_id uuid;
ALTER TABLE hive.devices ADD FOREIGN KEY (tenant_id,last_outlet_id) REFERENCES hive.locations;
CREATE TABLE hive.device_operator_assignments (
    tenant_id uuid NOT NULL, device_id uuid NOT NULL, outlet_id uuid NOT NULL, member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id,device_id,outlet_id,member_id),
    FOREIGN KEY (tenant_id,device_id,outlet_id) REFERENCES hive.device_outlets,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships
);
CREATE INDEX device_operator_member ON hive.device_operator_assignments(tenant_id,member_id);
CREATE TABLE hive.operational_contexts (
    tenant_id uuid NOT NULL, device_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(),
    outlet_id uuid NOT NULL, member_id uuid NOT NULL, digest bytea NOT NULL UNIQUE CHECK(octet_length(digest)=32),
    revision bigint NOT NULL CHECK(revision>0), device_revision bigint NOT NULL CHECK(device_revision>0),
    idle_expires_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, ended_at timestamptz,
    PRIMARY KEY (tenant_id,device_id,id),
    UNIQUE (tenant_id,device_id,revision),
    FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY (tenant_id,outlet_id) REFERENCES hive.locations,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships,
    CHECK(idle_expires_at<=expires_at)
);
CREATE UNIQUE INDEX one_open_device_context ON hive.operational_contexts(tenant_id,device_id) WHERE ended_at IS NULL;
CREATE INDEX operational_context_member ON hive.operational_contexts(tenant_id,member_id) WHERE ended_at IS NULL;
CREATE TABLE hive.device_operation_challenges (
    tenant_id uuid NOT NULL, device_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(),
    nonce text NOT NULL, device_revision bigint NOT NULL,
    operation text NOT NULL CHECK(operation IN ('state','context.read','context.logout')),
    context_id uuid, context_revision bigint,
    expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '60 seconds',
    PRIMARY KEY (tenant_id,device_id,id),
    FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices,
    CHECK ((operation='state' AND context_id IS NULL AND context_revision IS NULL)
        OR (operation<>'state' AND context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0))
);
CREATE INDEX device_operation_expiry ON hive.device_operation_challenges(expires_at);
CREATE TABLE hive.device_assignment_receipts (
    tenant_id uuid NOT NULL, device_id uuid NOT NULL, actor_id uuid NOT NULL, request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32), result jsonb NOT NULL,
    PRIMARY KEY(tenant_id,device_id,actor_id,request_key),
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
ALTER TABLE hive.device_pairing_limits DROP CONSTRAINT device_pairing_limits_scope_check;
ALTER TABLE hive.device_pairing_limits ADD CHECK(scope IN ('peer','key','owner','operation_peer','operation_device'));
ALTER TABLE hive.device_pairing_limits DROP CONSTRAINT device_pairing_limits_attempts_check;
ALTER TABLE hive.device_pairing_limits ADD CHECK(cardinality(attempts)<=600);
