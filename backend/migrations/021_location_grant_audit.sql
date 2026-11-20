-- Owner: Audit. Extend the existing revision-ordered RBAC stream.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_rbac_audit ADD CONSTRAINT tenant_rbac_audit_actions
    CHECK (action IN ('role.create','role.replace','membership.roles.replace',
        'role.location_actions.replace','membership.location_roles.replace')) NOT VALID;
ALTER TABLE hive.tenant_rbac_audit VALIDATE CONSTRAINT tenant_rbac_audit_actions;
ALTER TABLE hive.tenant_rbac_audit DROP CONSTRAINT tenant_rbac_audit_action_check;
