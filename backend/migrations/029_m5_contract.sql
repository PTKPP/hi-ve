-- Owner: Identity. Contract for tenant membership after the validated Primary Owner guard.
SET LOCAL lock_timeout = '5s';
ALTER TABLE hive.tenant_memberships DROP CONSTRAINT tenant_memberships_principal_id_fkey;
