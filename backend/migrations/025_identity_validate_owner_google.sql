-- Owner: Identity. Enforce the primary Owner's provider link before the separate Contract gate.
SET LOCAL lock_timeout='5s';
UPDATE hive.tenant_memberships
SET owner_google_principal_id=principal_id
WHERE role='primary_owner' AND owner_google_principal_id IS NULL;

ALTER TABLE hive.tenant_memberships ADD CONSTRAINT membership_owner_google_shape
    CHECK ((role='primary_owner' AND owner_google_principal_id IS NOT NULL AND owner_google_principal_id=principal_id)
        OR (role<>'primary_owner' AND owner_google_principal_id IS NULL)) NOT VALID;
ALTER TABLE hive.tenant_memberships ADD CONSTRAINT membership_owner_google_fk
    FOREIGN KEY (owner_google_principal_id) REFERENCES hive.google_principals(id) NOT VALID;
ALTER TABLE hive.tenant_memberships VALIDATE CONSTRAINT membership_owner_google_shape;
ALTER TABLE hive.tenant_memberships VALIDATE CONSTRAINT membership_owner_google_fk;
-- Keep tenant_memberships_principal_id_fkey. Contract and onboarding require separate review.
