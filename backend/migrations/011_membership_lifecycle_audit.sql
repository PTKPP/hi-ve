-- Owner: Audit. Lifecycle changes and historical evidence of revoked assignments.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.membership_lifecycle_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    member_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('deactivate','reactivate')),
    previous_active boolean NOT NULL,
    active boolean NOT NULL CHECK (active <> previous_active),
    revision bigint NOT NULL CHECK (revision>0),
    reason_code text NOT NULL,
    revoked_sessions bigint NOT NULL CHECK (revoked_sessions>=0),
    revoked_roles jsonb NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships,
    UNIQUE (tenant_id,member_id,revision)
);
