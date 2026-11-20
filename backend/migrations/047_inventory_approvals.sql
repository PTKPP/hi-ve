SET LOCAL lock_timeout='5s';
CREATE TABLE hive.inventory_approvals (
    tenant_id uuid NOT NULL, kind text NOT NULL CHECK(kind IN ('adjustment','stock_count','discrepancy.resolve')),
    document_id uuid NOT NULL, requester_id uuid NOT NULL, location_id uuid NOT NULL,
    reason text NOT NULL CHECK(length(reason)<=255 AND (kind<>'discrepancy.resolve' OR length(btrim(reason))>0)),
    observation_ids uuid[], CHECK ((kind='stock_count')=(observation_ids IS NOT NULL)),
    PRIMARY KEY (tenant_id,kind,document_id),
    FOREIGN KEY (tenant_id,kind,document_id) REFERENCES hive.inventory_postings,
    FOREIGN KEY (tenant_id,requester_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,location_id) REFERENCES hive.locations
);
CREATE TRIGGER inventory_approvals_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_approvals FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
ALTER TABLE hive.inventory_transfers ADD COLUMN resolved_discrepancy numeric NOT NULL DEFAULT 0
    CHECK(resolved_discrepancy>=0 AND resolved_discrepancy<=discrepancy);

-- Expand explicit scoped RBAC and device proofs. Existing grants are unchanged.
ALTER TABLE hive.tenant_role_location_actions ADD CONSTRAINT role_location_inventory_action CHECK (action IN (
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
    'inventory.stock_count.approve')) NOT VALID;
ALTER TABLE hive.tenant_role_location_actions VALIDATE CONSTRAINT role_location_inventory_action;
ALTER TABLE hive.tenant_role_location_actions DROP CONSTRAINT role_location_supported_action;
ALTER TABLE hive.device_operation_challenges ADD CONSTRAINT device_bound_command_target CHECK (
    CASE WHEN operation IN ('catalog.products.write','inventory.write') THEN context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
        AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL AND command_digest IS NOT NULL AND command_digest ~ '^[a-f0-9]{64}$'
    ELSE command_digest IS NULL AND (
        (operation='state' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation IN ('context.read','context.logout') AND context_id IS NOT NULL AND context_revision IS NOT NULL AND context_revision>0
            AND outlet_id IS NULL AND member_id IS NULL AND page_after IS NULL)
        OR (operation='profiles' AND context_id IS NULL AND context_revision IS NULL AND outlet_id IS NOT NULL AND member_id IS NULL)
        OR (operation='context.login' AND context_id IS NULL AND context_revision IS NOT NULL AND context_revision>=0 AND outlet_id IS NOT NULL AND member_id IS NOT NULL AND page_after IS NULL)
    ) END
) NOT VALID;
ALTER TABLE hive.device_operation_challenges VALIDATE CONSTRAINT device_bound_command_target;
ALTER TABLE hive.device_operation_challenges DROP CONSTRAINT device_catalog_command_target;

CREATE TABLE hive.inventory_rejections (
    tenant_id uuid NOT NULL, kind text NOT NULL CHECK(kind IN ('adjustment','stock_count')),
    document_id uuid NOT NULL, actor_id uuid NOT NULL, requester_id uuid NOT NULL, location_id uuid NOT NULL,
    reason text NOT NULL CHECK(length(btrim(reason)) BETWEEN 1 AND 255),
    decided_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,kind,document_id),
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,requester_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,location_id) REFERENCES hive.locations
);
CREATE TRIGGER inventory_rejections_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON hive.inventory_rejections FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();

ALTER TABLE hive.inventory_postings ADD COLUMN device_id uuid,
    ADD FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices;
ALTER TABLE hive.inventory_rejections ADD COLUMN device_id uuid,
    ADD FOREIGN KEY (tenant_id,device_id) REFERENCES hive.devices;
CREATE INDEX inventory_adjustment_location_history
    ON hive.inventory_documents(tenant_id,location_id,created_at DESC,id DESC) WHERE kind='adjustment';
