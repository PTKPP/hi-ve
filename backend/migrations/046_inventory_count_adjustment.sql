SET LOCAL lock_timeout='5s';
ALTER TABLE hive.inventory_documents ADD CONSTRAINT inventory_all_document_kinds
    CHECK (kind IN ('receiving','repacking','transfer','adjustment')) NOT VALID;
ALTER TABLE hive.inventory_documents VALIDATE CONSTRAINT inventory_all_document_kinds;
ALTER TABLE hive.inventory_documents DROP CONSTRAINT inventory_document_kinds;
CREATE TABLE hive.inventory_stock_counts (
    tenant_id uuid NOT NULL, id uuid NOT NULL, location_id uuid NOT NULL, actor_id uuid NOT NULL,
    started_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,location_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
CREATE TABLE hive.inventory_count_lines (
    tenant_id uuid NOT NULL, count_id uuid NOT NULL, sku_id uuid NOT NULL, batch_id uuid,
    quantity numeric NOT NULL CHECK(quantity>=0 AND quantity<'Infinity'::numeric),
    revision bigint NOT NULL CHECK(revision>=0),
    UNIQUE NULLS NOT DISTINCT(tenant_id,count_id,sku_id,batch_id),
    FOREIGN KEY(tenant_id,count_id) REFERENCES hive.inventory_stock_counts,
    FOREIGN KEY(tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY(tenant_id,sku_id,batch_id) REFERENCES hive.inventory_batches(tenant_id,sku_id,id)
);
CREATE TABLE hive.inventory_count_observations (
    tenant_id uuid NOT NULL, id uuid NOT NULL, count_id uuid NOT NULL, actor_id uuid NOT NULL,
    sku_id uuid NOT NULL, batch_id uuid, quantity numeric NOT NULL CHECK(quantity>=0 AND quantity<'Infinity'::numeric),
    observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,count_id) REFERENCES hive.inventory_stock_counts,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY(tenant_id,sku_id,batch_id) REFERENCES hive.inventory_batches(tenant_id,sku_id,id)
);
CREATE TRIGGER inventory_counts_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_stock_counts FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER inventory_count_lines_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_count_lines FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER inventory_count_observations_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_count_observations FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
