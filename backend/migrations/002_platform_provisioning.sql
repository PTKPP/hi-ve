-- Owner: Platform. Initial tenant lifecycle and provisioning workflow.
CREATE TABLE hive.tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'active', 'suspended', 'closed'))
);
CREATE TABLE hive.owner_candidates (
    tenant_id uuid PRIMARY KEY REFERENCES hive.tenants,
    id uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    contact_email text NOT NULL,
    UNIQUE (tenant_id, id)
);
CREATE TABLE hive.owner_invitations (
    tenant_id uuid PRIMARY KEY,
    owner_id uuid NOT NULL,
    generation uuid NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    digest bytea NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    FOREIGN KEY (tenant_id, owner_id) REFERENCES hive.owner_candidates (tenant_id, id),
    UNIQUE (tenant_id, owner_id, generation)
);
CREATE TABLE hive.provisioning_receipts (
    actor_id uuid NOT NULL,
    operation text NOT NULL CHECK (operation IN ('create', 'activate')),
    target text NOT NULL,
    request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
    tenant_id uuid REFERENCES hive.tenants,
    owner_id uuid,
    PRIMARY KEY (actor_id, operation, target, request_key),
    CHECK ((tenant_id IS NULL) = (owner_id IS NULL)),
    FOREIGN KEY (tenant_id, owner_id) REFERENCES hive.owner_candidates (tenant_id, id)
);

-- Controlled lifecycle projection for Identity's tenant access checks.
CREATE VIEW hive.active_tenants AS SELECT id FROM hive.tenants WHERE status = 'active';
