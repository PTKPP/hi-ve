-- Owner: Devices. Short-lived receipts for authenticated challenge issuance.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.device_bootstrap_receipts (
    tenant_id uuid NOT NULL,
    device_id uuid NOT NULL,
    request_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,device_id,request_id)
);
CREATE INDEX device_bootstrap_receipts_expiry ON hive.device_bootstrap_receipts(expires_at);
-- Deliberately no foreign key. Replay arbitration must precede any device row lock.
