SET LOCAL lock_timeout='5s';
CREATE TABLE hive.sales_reversal_requests (
    tenant_id uuid NOT NULL, id uuid NOT NULL, sale_id uuid NOT NULL, kind text NOT NULL CHECK(kind IN ('return','void')),
    requester_id uuid NOT NULL, device_id uuid, reason text NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 255),
    payload jsonb NOT NULL CHECK(jsonb_typeof(payload)='object'),
    requested_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,sale_id,id),
    FOREIGN KEY(tenant_id,sale_id) REFERENCES hive.sales_orders,
    FOREIGN KEY(tenant_id,requester_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices
);
CREATE INDEX sales_reversal_requests_original ON hive.sales_reversal_requests(tenant_id,sale_id);
CREATE TABLE hive.sales_reversal_postings (
    tenant_id uuid NOT NULL, id uuid NOT NULL, approver_id uuid NOT NULL, device_id uuid,
    reason text NOT NULL CHECK(length(reason)<=255), refund numeric NOT NULL CHECK(refund>=0 AND refund<'Infinity'::numeric),
    posted_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,id) REFERENCES hive.sales_reversal_requests,
    FOREIGN KEY(tenant_id,approver_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices
);
CREATE TABLE hive.sales_reversal_items (
    tenant_id uuid NOT NULL, reversal_id uuid NOT NULL, sale_id uuid NOT NULL, line int NOT NULL,
    quantity numeric NOT NULL CHECK(quantity>0 AND quantity<'Infinity'::numeric),
    refund numeric NOT NULL CHECK(refund>=0 AND refund=trunc(refund,2) AND refund<'Infinity'::numeric),
    saleable boolean NOT NULL,
    PRIMARY KEY(tenant_id,reversal_id,line),
    FOREIGN KEY(tenant_id,reversal_id) REFERENCES hive.sales_reversal_postings,
    FOREIGN KEY(tenant_id,sale_id,reversal_id) REFERENCES hive.sales_reversal_requests(tenant_id,sale_id,id),
    FOREIGN KEY(tenant_id,sale_id,line) REFERENCES hive.sales_items(tenant_id,sale_id,line)
);
CREATE INDEX sales_reversal_items_original ON hive.sales_reversal_items(tenant_id,sale_id,line);

CREATE TABLE hive.sales_refund_confirmations (
    tenant_id uuid NOT NULL, reversal_id uuid NOT NULL, actor_id uuid NOT NULL, device_id uuid,
    reference text NOT NULL CHECK(length(btrim(reference)) BETWEEN 1 AND 128),
    confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,reversal_id),
    FOREIGN KEY(tenant_id,reversal_id) REFERENCES hive.sales_reversal_postings,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices
);
CREATE TRIGGER sales_reversal_requests_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_reversal_requests
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER sales_reversal_postings_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_reversal_postings
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER sales_reversal_items_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_reversal_items
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();
CREATE TRIGGER sales_refunds_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.sales_refund_confirmations
    FOR EACH STATEMENT EXECUTE FUNCTION hive.sales_immutable();

-- Inventory records the source posting and guards cumulative restocks itself.
CREATE TABLE hive.inventory_sale_reversals (
    tenant_id uuid NOT NULL, reversal_id uuid NOT NULL, sale_id uuid NOT NULL,
    kind text GENERATED ALWAYS AS ('sale.reversal'::text) STORED,
    sale_kind text GENERATED ALWAYS AS ('sale'::text) STORED,
    PRIMARY KEY(tenant_id,reversal_id),
    FOREIGN KEY(tenant_id,kind,reversal_id) REFERENCES hive.inventory_postings,
    FOREIGN KEY(tenant_id,sale_kind,sale_id) REFERENCES hive.inventory_postings
);
CREATE INDEX inventory_sale_reversals_original ON hive.inventory_sale_reversals(tenant_id,sale_id);
CREATE TRIGGER inventory_sale_reversals_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.inventory_sale_reversals
    FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();

ALTER TABLE hive.tenant_role_location_actions ADD CONSTRAINT role_location_sales_reversal_action CHECK (action IN (
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
    'sales.history.read', 'sales.receipts.read', 'sales.cashier.search',
    'sales.returns.create','sales.returns.approve','sales.voids.create','sales.voids.approve','sales.payments.refund_confirm')) NOT VALID;
ALTER TABLE hive.tenant_role_location_actions VALIDATE CONSTRAINT role_location_sales_reversal_action;
ALTER TABLE hive.tenant_role_location_actions DROP CONSTRAINT role_location_sales_action;
