-- Catalog owns selling prices. Each version is appended, preserving snapshot references.
CREATE TABLE hive.catalog_prices (
    tenant_id uuid NOT NULL, sku_id uuid NOT NULL, outlet_id uuid, id uuid NOT NULL DEFAULT gen_random_uuid(),
    revision bigint NOT NULL CHECK (revision > 0), amount numeric NOT NULL CHECK (amount >= 0 AND amount < 'Infinity'::numeric),
    actor_id uuid NOT NULL, updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,id),
    UNIQUE NULLS NOT DISTINCT (tenant_id,sku_id,outlet_id,revision),
    FOREIGN KEY (tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY (tenant_id,outlet_id) REFERENCES hive.locations,
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);

CREATE FUNCTION hive.keep_catalog_price_version() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'selling price versions are immutable' USING ERRCODE='42501';
END $$;
CREATE TRIGGER catalog_prices_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.catalog_prices FOR EACH STATEMENT EXECUTE FUNCTION hive.keep_catalog_price_version();
