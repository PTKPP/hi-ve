-- Owner: Audit. Short-lived operational history, independent of critical audit tables.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.online_login_history (
    event_id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    flow_class text NOT NULL CHECK (flow_class='tenant_owner_google'),
    outcome text NOT NULL CHECK (outcome IN ('success','denied')),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
CREATE INDEX online_login_history_tenant_page ON hive.online_login_history (tenant_id,received_at DESC,event_id DESC);
CREATE INDEX online_login_history_expiry ON hive.online_login_history (received_at,event_id);
