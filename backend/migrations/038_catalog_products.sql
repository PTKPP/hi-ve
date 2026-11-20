-- Catalog owns tenant master data. Location and stock balances stay outside Catalog.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.catalog_products (
    tenant_id uuid NOT NULL REFERENCES hive.tenants, id uuid NOT NULL,
    name text NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 128),
    category text NOT NULL CHECK (length(trim(category)) BETWEEN 1 AND 128),
    revision bigint NOT NULL CHECK (revision > 0),
    PRIMARY KEY (tenant_id,id)
);
CREATE TABLE hive.catalog_skus (
    tenant_id uuid NOT NULL, id uuid NOT NULL, product_id uuid NOT NULL,
    code text NOT NULL CHECK (code ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
    variant text NOT NULL CHECK (length(trim(variant)) BETWEEN 1 AND 128),
    barcode text CHECK (length(trim(barcode)) BETWEEN 1 AND 128),
    unit hive.catalog_unit NOT NULL, active boolean NOT NULL,
    batch_tracked boolean NOT NULL, revision bigint NOT NULL CHECK (revision > 0),
    PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,code), UNIQUE (tenant_id,barcode),
    FOREIGN KEY (tenant_id,product_id) REFERENCES hive.catalog_products
);
CREATE INDEX catalog_product_skus ON hive.catalog_skus(tenant_id,product_id,id);
CREATE TABLE hive.catalog_write_receipts (
    tenant_id uuid NOT NULL, actor_id uuid NOT NULL, request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32), revision bigint,
    PRIMARY KEY (tenant_id,actor_id,request_key),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
