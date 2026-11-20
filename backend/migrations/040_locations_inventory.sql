-- Locations owns the mapping. The primary node is stable across status changes.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.inventory_locations (
    tenant_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(),
    location_id uuid NOT NULL, PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,location_id),
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.locations
);
-- Cover both current and older Locations writers during rolling upgrades.
CREATE FUNCTION hive.create_primary_inventory_location() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    INSERT INTO hive.inventory_locations(tenant_id,location_id) VALUES(NEW.tenant_id,NEW.id);
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION hive.create_primary_inventory_location() FROM PUBLIC;
CREATE TRIGGER locations_primary_inventory AFTER INSERT ON hive.locations
FOR EACH ROW EXECUTE FUNCTION hive.create_primary_inventory_location();
INSERT INTO hive.inventory_locations(tenant_id,location_id) SELECT tenant_id,id FROM hive.locations;

ALTER TABLE hive.locations ADD COLUMN revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0);
CREATE TABLE hive.location_status_history (
    tenant_id uuid NOT NULL, location_id uuid NOT NULL, actor_id uuid NOT NULL, request_key text NOT NULL,
    revision bigint NOT NULL, status text NOT NULL CHECK(status IN ('active','inactive')),
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,location_id,revision), UNIQUE(tenant_id,actor_id,request_key),
    FOREIGN KEY(tenant_id,location_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
