-- Owner: Audit. Receipt settings changes with metadata only.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.receipt_settings_audit (
    tenant_id uuid NOT NULL, actor_id uuid NOT NULL, revision bigint NOT NULL CHECK(revision>0),
    field_name text NOT NULL CHECK(field_name='receipt_footer'),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,revision),
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
