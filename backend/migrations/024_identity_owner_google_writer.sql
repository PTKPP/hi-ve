-- Owner: Identity. Provide a bridge for already-deployed provisioning writers.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.tenant_memberships ADD COLUMN owner_google_principal_id uuid;

CREATE FUNCTION hive.fill_owner_google_principal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.role='primary_owner' AND NEW.owner_google_principal_id IS NULL THEN
        NEW.owner_google_principal_id := NEW.principal_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER fill_owner_google_principal BEFORE INSERT OR UPDATE ON hive.tenant_memberships
FOR EACH ROW EXECUTE FUNCTION hive.fill_owner_google_principal();
