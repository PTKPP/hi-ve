-- Owner: Devices. Extend the existing proof registry without rewriting challenges.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.device_operation_challenges
    ADD COLUMN outlet_id uuid, ADD COLUMN member_id uuid, ADD COLUMN page_after uuid;
ALTER TABLE hive.device_operation_challenges DROP CONSTRAINT device_operation_challenges_operation_check;
ALTER TABLE hive.device_operation_challenges DROP CONSTRAINT device_operation_challenges_check;
ALTER TABLE hive.device_operation_challenges ADD CONSTRAINT device_operation_target_check CHECK (
    (operation='state' AND context_id IS NULL AND context_revision IS NULL
        AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
 OR (operation IN ('context.read','context.logout') AND context_id IS NOT NULL
        AND context_revision IS NOT NULL AND context_revision>0
        AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
 OR (operation='profiles' AND context_id IS NULL AND context_revision IS NULL
        AND outlet_id IS NOT NULL AND member_id IS NULL)
 OR (operation='context.login' AND context_id IS NULL AND context_revision IS NOT NULL
        AND context_revision>=0 AND outlet_id IS NOT NULL AND member_id IS NOT NULL AND page_after IS NULL)
);
ALTER TABLE hive.device_pairing_limits DROP CONSTRAINT device_pairing_limits_scope_check;
ALTER TABLE hive.device_pairing_limits ADD CHECK(scope IN
    ('peer','key','owner','operation_peer','operation_device','pin_peer','pin_device'));
