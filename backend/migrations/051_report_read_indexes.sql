-- Domain-owned access paths for read-only reports. No historical rewrite.
SET LOCAL lock_timeout='5s';
CREATE INDEX sales_orders_tenant_report_range ON hive.sales_orders(tenant_id,created_at DESC,id DESC);
CREATE INDEX inventory_postings_tenant_report_range ON hive.inventory_postings(tenant_id,posted_at DESC,kind DESC,document_id DESC);
CREATE INDEX inventory_balances_report_page ON hive.inventory_balances
 (tenant_id,location_id,sku_id,COALESCE(batch_id,'00000000-0000-0000-0000-000000000000'::uuid),bucket);
CREATE INDEX provisioning_audit_tenant_report ON hive.provisioning_audit(tenant_id,occurred_at DESC,id DESC);
CREATE INDEX entitlement_audit_tenant_report ON hive.entitlement_audit(tenant_id,occurred_at DESC,id DESC);
