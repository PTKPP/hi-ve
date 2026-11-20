-- Owner: Audit. Persist successful approval, enrollment and revocation with the mutation.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.device_pairing_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    action text NOT NULL CHECK (action IN ('approved','redeemed','revoked')),
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    pairing_id text NOT NULL,
    device_id uuid,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((action='approved') = (device_id IS NULL)),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices (tenant_id,id)
);
