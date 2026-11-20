-- Rehearsal only. Keep outside the release migration chain.
SET LOCAL lock_timeout = '5s';
ALTER TABLE hive.tenant_memberships DROP CONSTRAINT tenant_memberships_principal_id_fkey;
