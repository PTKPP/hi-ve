-- Owner: Audit. Provisioning and organization verification audit.
CREATE TABLE hive.provisioning_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id uuid REFERENCES hive.platform_principals,
    tenant_id uuid REFERENCES hive.tenants,
    claim_id uuid REFERENCES hive.owner_claims,
    action text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'denied')),
    verification_method text,
    verification_reference text,
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
