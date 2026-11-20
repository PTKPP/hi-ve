-- Owner: Identity. Existing App sessions were all issued by Google. Mark their provenance
-- without deleting rows or enabling a writer for device PIN sessions.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_sessions ADD COLUMN auth_method text NOT NULL DEFAULT 'google'
    CHECK (auth_method IN ('google','device_pin'));

-- Existing flows are short-lived. Add the default separately to avoid a heap rewrite.
ALTER TABLE hive.oidc_flows ADD COLUMN event_id uuid;
ALTER TABLE hive.oidc_flows ALTER COLUMN event_id SET DEFAULT gen_random_uuid();
UPDATE hive.oidc_flows SET event_id=gen_random_uuid() WHERE event_id IS NULL;
ALTER TABLE hive.oidc_flows ALTER COLUMN event_id SET NOT NULL;
ALTER TABLE hive.oidc_flows ADD UNIQUE (event_id);
