-- Owner: Settings. Tenant receipt presentation and scoped mutation receipts.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.tenant_receipt_settings (
    tenant_id uuid PRIMARY KEY REFERENCES hive.tenants,
    receipt_footer text CHECK(receipt_footer IS NULL OR char_length(receipt_footer) BETWEEN 1 AND 160),
    revision bigint NOT NULL DEFAULT 0 CHECK(revision>=0)
);
CREATE TABLE hive.receipt_settings_receipts (
    tenant_id uuid NOT NULL, actor_id uuid NOT NULL, request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32), result jsonb,
    PRIMARY KEY(tenant_id,actor_id,request_key),
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
