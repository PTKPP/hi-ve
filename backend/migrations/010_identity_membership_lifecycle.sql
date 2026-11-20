-- Owner: Identity. Online lifecycle for existing non-Owner memberships.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_memberships ADD COLUMN lifecycle_revision bigint NOT NULL DEFAULT 0 CHECK (lifecycle_revision >= 0);

CREATE INDEX tenant_sessions_membership ON hive.tenant_sessions (tenant_id,principal_id);
CREATE TABLE hive.membership_lifecycle_receipts (
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    member_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('deactivate','reactivate')),
    request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    result jsonb,
    PRIMARY KEY (tenant_id,actor_id,member_id,operation,request_key),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships
);
