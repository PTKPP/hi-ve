SET LOCAL lock_timeout='5s';

-- Pending approvals issued before this migration require fresh approval.
ALTER TABLE hive.device_pairings ADD COLUMN approval_tenant_epoch bigint;

CREATE FUNCTION hive.set_device_pairing_authorization_epoch() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    current_epoch bigint;
BEGIN
    IF OLD.tenant_id IS NULL AND NEW.tenant_id IS NOT NULL THEN
        SELECT authorization_epoch INTO NEW.approval_tenant_epoch
        FROM hive.tenants WHERE id=NEW.tenant_id FOR SHARE;
    END IF;
    IF OLD.redeem_key IS NULL AND NEW.redeem_key IS NOT NULL THEN
        SELECT authorization_epoch INTO current_epoch
        FROM hive.tenants WHERE id=NEW.tenant_id AND status='active' FOR SHARE;
        IF current_epoch IS NULL OR NEW.approval_tenant_epoch IS DISTINCT FROM current_epoch THEN
            RAISE EXCEPTION 'Device pairing approval is no longer current' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER device_pairing_authorization_epoch
BEFORE UPDATE OF tenant_id, redeem_key ON hive.device_pairings
FOR EACH ROW EXECUTE FUNCTION hive.set_device_pairing_authorization_epoch();
