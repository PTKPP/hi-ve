-- Owner: Identity. Expand and backfill with stable IDs and legacy Google write compatibility.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.identity_principals (id uuid PRIMARY KEY);

CREATE FUNCTION hive.mirror_google_principal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO hive.identity_principals (id) VALUES (NEW.id)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;

CREATE TRIGGER google_principal_identity AFTER INSERT ON hive.google_principals
FOR EACH ROW EXECUTE FUNCTION hive.mirror_google_principal();

INSERT INTO hive.identity_principals (id)
SELECT id FROM hive.google_principals
ON CONFLICT DO NOTHING;

ALTER TABLE hive.google_principals
    ADD CONSTRAINT google_principal_identity_fk FOREIGN KEY (id)
    REFERENCES hive.identity_principals (id) DEFERRABLE INITIALLY DEFERRED NOT VALID;
ALTER TABLE hive.tenant_memberships
    ADD CONSTRAINT membership_identity_principal_fk FOREIGN KEY (principal_id)
    REFERENCES hive.identity_principals (id) NOT VALID;
ALTER TABLE hive.google_principals VALIDATE CONSTRAINT google_principal_identity_fk;
ALTER TABLE hive.tenant_memberships VALIDATE CONSTRAINT membership_identity_principal_fk;
-- The existing Google-only FK remains for compatibility until the Contract increment.
