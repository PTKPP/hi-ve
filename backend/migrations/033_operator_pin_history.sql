-- Owner: Audit. Online operator history is separate from Google's S2 history.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.operator_login_history (
    event_id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL, attempt_id uuid NOT NULL, member_id uuid NOT NULL,
    device_id uuid NOT NULL, outlet_id uuid NOT NULL,
    outcome text NOT NULL CHECK(outcome IN ('success','denied','lockout')),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(tenant_id,attempt_id,outcome),
    FOREIGN KEY(tenant_id,member_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY(tenant_id,outlet_id) REFERENCES hive.locations
);
CREATE INDEX operator_history_tenant_time ON hive.operator_login_history(tenant_id,occurred_at DESC,event_id DESC);
CREATE INDEX operator_history_expiry ON hive.operator_login_history(occurred_at,event_id);
