-- Owner: Audit. Successful RBAC mutations, committed with Identity state.
SET LOCAL lock_timeout = '5s';
CREATE TABLE hive.tenant_rbac_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    target_id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('role.create', 'role.replace', 'membership.roles.replace')),
    revision bigint NOT NULL CHECK (revision > 0),
    before_state jsonb NOT NULL,
    after_state jsonb NOT NULL,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id, actor_id) REFERENCES hive.tenant_memberships,
    UNIQUE (tenant_id, revision)
);
