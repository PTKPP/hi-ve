-- Identity grants tenant-wide catalog management explicitly. No preset is changed.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_role_actions ADD CONSTRAINT role_actions_phase03 CHECK(action IN
    ('identity.roles.read','catalog.products.manage')) NOT VALID;
ALTER TABLE hive.tenant_role_actions VALIDATE CONSTRAINT role_actions_phase03;
ALTER TABLE hive.tenant_role_actions DROP CONSTRAINT tenant_role_actions_action_check;

-- Devices binds a one-shot operator command to the exact HTTP request digest.
ALTER TABLE hive.device_operation_challenges ADD COLUMN command_digest text;
ALTER TABLE hive.device_operation_challenges ADD CONSTRAINT device_catalog_command_target CHECK (
    CASE WHEN operation='catalog.products.write' THEN context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
        AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL AND command_digest IS NOT NULL AND command_digest ~ '^[a-f0-9]{64}$'
    ELSE command_digest IS NULL AND (
        (operation='state' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation IN ('context.read','context.logout') AND context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
            AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation='profiles' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NOT NULL AND member_id IS NULL)
        OR (operation='context.login' AND context_id IS NULL AND context_revision IS NOT NULL AND context_revision>=0 AND outlet_id IS NOT NULL AND member_id IS NOT NULL AND page_after IS NULL)
    ) END
) NOT VALID;
ALTER TABLE hive.device_operation_challenges VALIDATE CONSTRAINT device_catalog_command_target;
ALTER TABLE hive.device_operation_challenges DROP CONSTRAINT device_operation_target_check;
