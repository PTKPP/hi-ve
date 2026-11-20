-- Identity owns location-scoped actions. No existing role gains this action.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_role_location_actions ADD CONSTRAINT role_location_supported_action
    CHECK (action IN ('devices.operators.use','catalog.pricing.read')) NOT VALID;
ALTER TABLE hive.tenant_role_location_actions VALIDATE CONSTRAINT role_location_supported_action;
ALTER TABLE hive.tenant_role_location_actions DROP CONSTRAINT tenant_role_location_actions_action_check;
