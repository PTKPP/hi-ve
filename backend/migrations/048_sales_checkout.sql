-- Sales owns immutable checkout snapshots and recorded payment confirmations.
SET LOCAL lock_timeout='5s';
CREATE TABLE hive.sales_orders (
    tenant_id uuid NOT NULL, id uuid NOT NULL, outlet_id uuid NOT NULL, actor_id uuid NOT NULL, device_id uuid,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32),
    total numeric NOT NULL CHECK(total>=0 AND total=trunc(total,2) AND total<'Infinity'::numeric),
    method text NOT NULL CHECK(method IN ('cash','manual_non_cash')),
    tendered numeric, change numeric,
    receipt_footer text, outlet_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,outlet_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    CHECK ((method='cash' AND tendered IS NOT NULL AND change IS NOT NULL AND
            tendered >= total AND tendered=trunc(tendered,2) AND tendered < 'Infinity'::numeric AND change=tendered-total)
        OR (method='manual_non_cash' AND tendered IS NULL AND change IS NULL))
);
CREATE INDEX sales_orders_outlet_history ON hive.sales_orders(tenant_id,outlet_id,created_at DESC,id DESC);
CREATE INDEX sales_orders_actor_history ON hive.sales_orders(tenant_id,outlet_id,actor_id,created_at DESC,id DESC);
CREATE INDEX sales_orders_total_history ON hive.sales_orders(tenant_id,outlet_id,total,created_at DESC,id DESC);

CREATE TABLE hive.sales_items (
    tenant_id uuid NOT NULL, sale_id uuid NOT NULL, line int NOT NULL CHECK(line>0),
    sku_id uuid NOT NULL, batch_id uuid, name text NOT NULL, variant text NOT NULL, unit hive.catalog_unit NOT NULL,
    quantity numeric NOT NULL CHECK(quantity>0 AND quantity<'Infinity'::numeric),
    price_id uuid NOT NULL, price_revision bigint NOT NULL CHECK(price_revision>0),
    unit_price numeric NOT NULL CHECK(unit_price>=0 AND unit_price<'Infinity'::numeric),
    discount numeric NOT NULL CHECK(discount>=0 AND discount<=quantity*unit_price),
    net numeric GENERATED ALWAYS AS (quantity*unit_price-discount) STORED CHECK(net=trunc(net,2)),
    PRIMARY KEY(tenant_id,sale_id,line),
    FOREIGN KEY(tenant_id,sale_id) REFERENCES hive.sales_orders,
    FOREIGN KEY(tenant_id,sku_id) REFERENCES hive.catalog_skus,
    FOREIGN KEY(tenant_id,sku_id,batch_id) REFERENCES hive.inventory_batches(tenant_id,sku_id,id),
    FOREIGN KEY(tenant_id,price_id) REFERENCES hive.catalog_prices
);
CREATE TABLE hive.sales_manual_confirmations (
    tenant_id uuid NOT NULL, sale_id uuid NOT NULL, actor_id uuid NOT NULL, device_id uuid,
    reference text NOT NULL CHECK(length(btrim(reference)) BETWEEN 1 AND 128),
    confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,sale_id),
    FOREIGN KEY(tenant_id,sale_id) REFERENCES hive.sales_orders,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices
);
CREATE FUNCTION hive.sales_immutable() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'posted sales history is immutable' USING ERRCODE='42501';
END
$$;
CREATE TRIGGER sales_orders_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_orders
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER sales_items_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_items
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER sales_confirmations_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_manual_confirmations
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();

ALTER TABLE hive.tenant_role_location_actions ADD CONSTRAINT role_location_sales_action CHECK (action IN (
    'devices.operators.use', 'catalog.pricing.read',
    'inventory.balance.read',
    'inventory.traceability.read',
    'inventory.receiving.create',
    'inventory.receiving.post',
    'inventory.repacking.create',
    'inventory.repacking.post',
    'inventory.transfer.create',
    'inventory.transfer.send',
    'inventory.transfer.quick',
    'inventory.transfer.receive',
    'inventory.transfer.record_discrepancy',
    'inventory.transfer.approve_discrepancy',
    'inventory.adjustment.create',
    'inventory.adjustment.read',
    'inventory.adjustment.approve',
    'inventory.stock_count.create',
    'inventory.stock_count.observe',
    'inventory.stock_count.approve',
    'sales.checkout.post', 'sales.transactions.discount', 'sales.payments.confirm',
    'sales.history.read', 'sales.receipts.read', 'sales.cashier.search')) NOT VALID;
ALTER TABLE hive.tenant_role_location_actions VALIDATE CONSTRAINT role_location_sales_action;
ALTER TABLE hive.tenant_role_location_actions DROP CONSTRAINT role_location_inventory_action;
ALTER TABLE hive.device_operation_challenges ADD CONSTRAINT device_sales_command_target CHECK (
    CASE WHEN operation IN ('catalog.products.write','inventory.write','sales.write') THEN context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
        AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL AND command_digest IS NOT NULL AND command_digest ~ '^[a-f0-9]{64}$'
    ELSE command_digest IS NULL AND (
        (operation='state' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation IN ('context.read','context.logout') AND context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
            AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation='profiles' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NOT NULL AND member_id IS NULL)
        OR (operation='context.login' AND context_id IS NULL AND context_revision IS NOT NULL AND context_revision>=0 AND outlet_id IS NOT NULL AND member_id IS NOT NULL AND page_after IS NULL)
    ) END
) NOT VALID;
ALTER TABLE hive.device_operation_challenges VALIDATE CONSTRAINT device_sales_command_target;
ALTER TABLE hive.device_operation_challenges DROP CONSTRAINT device_bound_command_target;

