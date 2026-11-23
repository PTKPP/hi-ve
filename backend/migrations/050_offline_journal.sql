-- Owner: Offline (journal and evidence), Audit (event history).
SET LOCAL lock_timeout='5s';

-- Owner: Platform. Fence leases across suspension and subsequent restore.
ALTER TABLE hive.tenants ADD COLUMN authorization_epoch bigint NOT NULL DEFAULT 0 CHECK(authorization_epoch>=0);
CREATE FUNCTION hive.tenant_advance_authorization_epoch() RETURNS trigger
    LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status THEN
        NEW.authorization_epoch := OLD.authorization_epoch + 1;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER tenant_advance_authorization_epoch BEFORE UPDATE OF status ON hive.tenants
    FOR EACH ROW EXECUTE FUNCTION hive.tenant_advance_authorization_epoch();

CREATE TABLE hive.offline_leases (
    tenant_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(),
    device_id uuid NOT NULL, outlet_id uuid NOT NULL, operator_id uuid NOT NULL, context_id uuid NOT NULL,
    device_revision bigint NOT NULL, context_revision bigint NOT NULL, credential_revision bigint NOT NULL,
    discount_allowed boolean NOT NULL, tenant_epoch bigint NOT NULL,
    rbac_revision bigint NOT NULL, transactions_revision bigint NOT NULL, payments_revision bigint NOT NULL,
    issued_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
    sealed_count integer CHECK(sealed_count>=0), sealed_at timestamptz,
    closed_at timestamptz,
    PRIMARY KEY(tenant_id,id),
    CHECK(expires_at>issued_at AND expires_at<=issued_at+interval '24 hours'),
    CHECK((sealed_count IS NULL)=(sealed_at IS NULL)),
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY(tenant_id,outlet_id) REFERENCES hive.locations,
    FOREIGN KEY(tenant_id,operator_id) REFERENCES hive.tenant_memberships
);
CREATE UNIQUE INDEX offline_device_open_lease ON hive.offline_leases(tenant_id,device_id) WHERE closed_at IS NULL;

CREATE TABLE hive.offline_sales (
    tenant_id uuid NOT NULL, id uuid NOT NULL, lease_id uuid NOT NULL,
    sequence integer NOT NULL CHECK(sequence>0),
    occurrence timestamptz NOT NULL, received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    checkout jsonb NOT NULL, quoted_prices jsonb NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
    state text NOT NULL CHECK(state IN ('posted','needs_review')),
    reason text,
    CHECK((state='posted')=(reason IS NULL)),
    PRIMARY KEY(tenant_id,id), UNIQUE(tenant_id,lease_id,sequence),
    FOREIGN KEY(tenant_id,lease_id) REFERENCES hive.offline_leases
);
CREATE INDEX offline_review ON hive.offline_sales(tenant_id,lease_id,id) WHERE state='needs_review';

CREATE TABLE hive.offline_sale_resolutions (
    tenant_id uuid NOT NULL, sale_id uuid NOT NULL, actor_id uuid NOT NULL,
    decision text NOT NULL CHECK(decision IN ('posted','manual_refund_recorded')),
    reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
    refund_amount numeric CHECK(refund_amount>0 AND refund_amount=trunc(refund_amount,2)),
    reference text CHECK(length(reference) BETWEEN 1 AND 128),
    CHECK((decision='manual_refund_recorded')=(reference IS NOT NULL)),
    CHECK((decision='manual_refund_recorded')=(refund_amount IS NOT NULL)),
    resolved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,sale_id),
    FOREIGN KEY(tenant_id,sale_id) REFERENCES hive.offline_sales,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);

CREATE TABLE hive.offline_login_receipts (
    tenant_id uuid NOT NULL, event_id uuid NOT NULL, fingerprint bytea NOT NULL CHECK(octet_length(fingerprint)=32),
    PRIMARY KEY (tenant_id,event_id)
);
ALTER TABLE hive.operator_login_history ADD COLUMN offline_occurred_at timestamptz;
ALTER TABLE hive.operator_login_history ADD COLUMN offline_lease_id uuid;
ALTER TABLE hive.operator_login_history ADD FOREIGN KEY (tenant_id,offline_lease_id) REFERENCES hive.offline_leases;

CREATE TABLE hive.offline_control_audit (
    tenant_id uuid NOT NULL, id uuid NOT NULL DEFAULT gen_random_uuid(), device_id uuid NOT NULL,
    actor_id uuid NOT NULL, lease_id uuid, action text NOT NULL CHECK(action IN ('lease.close','lease.lost','outlet.switch')),
    reason text CHECK(length(reason) BETWEEN 1 AND 500), outlet_id uuid,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,device_id) REFERENCES hive.devices,
    FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY(tenant_id,lease_id) REFERENCES hive.offline_leases,
    FOREIGN KEY(tenant_id,outlet_id) REFERENCES hive.locations
);
CREATE TRIGGER offline_sales_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.offline_sales
    FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER offline_resolution_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.offline_sale_resolutions
    FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
CREATE TRIGGER offline_control_immutable BEFORE UPDATE OR DELETE OR TRUNCATE ON hive.offline_control_audit
    FOR EACH STATEMENT EXECUTE FUNCTION hive.inventory_immutable();
