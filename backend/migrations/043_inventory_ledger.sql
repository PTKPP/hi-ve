-- Inventory owns batches, posting receipts, immutable movements and projected balances.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.inventory_batches (
    tenant_id uuid NOT NULL, id uuid NOT NULL, sku_id uuid NOT NULL,
    lot_code text NOT NULL CHECK (length(btrim(lot_code)) BETWEEN 1 AND 128),
    origin text NOT NULL CHECK (length(btrim(origin)) BETWEEN 1 AND 255),
    received_on date NOT NULL, recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,sku_id,id),
    FOREIGN KEY (tenant_id,sku_id) REFERENCES hive.catalog_skus
);
CREATE TABLE hive.inventory_postings (
    tenant_id uuid NOT NULL, kind text NOT NULL, document_id uuid NOT NULL, actor_id uuid NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    posted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,kind,document_id), FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
CREATE TABLE hive.inventory_balances (
    tenant_id uuid NOT NULL, location_id uuid NOT NULL, sku_id uuid NOT NULL, batch_id uuid,
    bucket text NOT NULL CHECK (bucket IN ('available','non_saleable','in_transit','discrepancy')),
    quantity numeric NOT NULL CHECK (quantity>=0 AND quantity<'Infinity'::numeric),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
    UNIQUE NULLS NOT DISTINCT (tenant_id,location_id,sku_id,batch_id,bucket),
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.inventory_locations,
    FOREIGN KEY (tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY (tenant_id,sku_id,batch_id) REFERENCES hive.inventory_batches (tenant_id,sku_id,id)
);
CREATE TABLE hive.inventory_movements (
    tenant_id uuid NOT NULL, kind text NOT NULL, document_id uuid NOT NULL, line int NOT NULL CHECK(line>0),
    location_id uuid NOT NULL, sku_id uuid NOT NULL, batch_id uuid, bucket text NOT NULL
        CHECK (bucket IN ('available','non_saleable','in_transit','discrepancy')),
    delta numeric NOT NULL CHECK(delta<>0 AND delta BETWEEN -1e128::numeric AND 1e128::numeric),
    PRIMARY KEY (tenant_id,kind,document_id,line),
    FOREIGN KEY (tenant_id,kind,document_id) REFERENCES hive.inventory_postings,
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.inventory_locations,
    FOREIGN KEY (tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY (tenant_id,sku_id,batch_id) REFERENCES hive.inventory_batches (tenant_id,sku_id,id)
);
CREATE INDEX inventory_movement_scope ON hive.inventory_movements(tenant_id,location_id,sku_id,batch_id);
CREATE FUNCTION hive.inventory_immutable() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'posted inventory history is immutable' USING ERRCODE='42501';
END
$$;
CREATE TRIGGER inventory_postings_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_postings FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER inventory_movements_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_movements FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
