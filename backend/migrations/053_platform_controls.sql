SET LOCAL lock_timeout='5s';
-- Settings owns typed global configuration. Audit owns the immutable history.
CREATE TABLE hive.global_boolean_settings (
    key text PRIMARY KEY CHECK(key='notifications.web_push_enabled'),
    enabled boolean NOT NULL,
    revision bigint NOT NULL CHECK(revision>=0)
);
INSERT INTO hive.global_boolean_settings VALUES('notifications.web_push_enabled',false,0);
CREATE TABLE hive.global_setting_audit (
    actor_id uuid NOT NULL REFERENCES hive.platform_principals,
    request_id text NOT NULL, key text NOT NULL REFERENCES hive.global_boolean_settings,
    previous boolean NOT NULL, enabled boolean NOT NULL,
    revision bigint NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(actor_id,request_id)
);
CREATE TABLE hive.tenant_administration_audit (
    id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    case_id text NOT NULL, actor_id uuid NOT NULL REFERENCES hive.platform_principals,
    action text NOT NULL CHECK(action IN ('suspend','reactivate','close','transfer')),
    evidence_reference text, reason text NOT NULL,
    previous_owner uuid, new_owner uuid, revision bigint NOT NULL,
    status text NOT NULL, fingerprint bytea NOT NULL, occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,case_id)
);
CREATE INDEX tenant_admin_history ON hive.tenant_administration_audit(tenant_id,occurred_at DESC,id DESC);
CREATE TRIGGER global_setting_audit_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.global_setting_audit
 FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER tenant_admin_audit_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.tenant_administration_audit
 FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
