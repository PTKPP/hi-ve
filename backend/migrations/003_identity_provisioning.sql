-- Owner: Identity. Google principals, tenant membership and scoped sessions.
CREATE TABLE hive.google_principals (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer text NOT NULL,
    subject text NOT NULL,
    UNIQUE (issuer, subject)
);
CREATE TABLE hive.platform_principals (
    principal_id uuid PRIMARY KEY REFERENCES hive.google_principals,
    active boolean NOT NULL DEFAULT true
);
CREATE TABLE hive.platform_permissions (
    principal_id uuid NOT NULL REFERENCES hive.platform_principals,
    permission text NOT NULL,
    PRIMARY KEY (principal_id, permission)
);
ALTER TABLE hive.provisioning_receipts
    ADD FOREIGN KEY (actor_id) REFERENCES hive.platform_principals;
CREATE TABLE hive.owner_claims (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    generation uuid NOT NULL UNIQUE,
    issuer text NOT NULL,
    subject text NOT NULL,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    FOREIGN KEY (tenant_id, owner_id) REFERENCES hive.owner_candidates (tenant_id, id)
);
CREATE TABLE hive.tenant_memberships (
    tenant_id uuid NOT NULL REFERENCES hive.tenants,
    principal_id uuid NOT NULL REFERENCES hive.google_principals,
    owner_id uuid,
    role text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id, principal_id),
    FOREIGN KEY (tenant_id, owner_id) REFERENCES hive.owner_candidates (tenant_id, id),
    CHECK ((role = 'primary_owner') = (owner_id IS NOT NULL))
);
CREATE UNIQUE INDEX tenant_active_primary_owner
    ON hive.tenant_memberships (tenant_id) WHERE active AND role = 'primary_owner';
CREATE TABLE hive.platform_sessions (
    digest bytea PRIMARY KEY CHECK (octet_length(digest) = 32),
    principal_id uuid NOT NULL REFERENCES hive.platform_principals,
    expires_at timestamptz NOT NULL
);
CREATE TABLE hive.tenant_sessions (
    digest bytea PRIMARY KEY CHECK (octet_length(digest) = 32),
    tenant_id uuid NOT NULL,
    principal_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    FOREIGN KEY (tenant_id, principal_id) REFERENCES hive.tenant_memberships
);
CREATE TABLE hive.oidc_flows (
    state_digest bytea PRIMARY KEY CHECK (octet_length(state_digest) = 32),
    browser_digest bytea NOT NULL CHECK (octet_length(browser_digest) = 32),
    nonce text NOT NULL,
    verifier text NOT NULL,
    purpose text NOT NULL,
    -- An untrusted tenant login intent is validated against active membership after OIDC.
    tenant_id uuid,
    generation uuid,
    expires_at timestamptz NOT NULL,
    CHECK (
        (purpose = 'platform' AND tenant_id IS NULL AND generation IS NULL)
        OR (purpose = 'tenant' AND tenant_id IS NOT NULL AND generation IS NULL)
        OR (purpose = 'claim' AND tenant_id IS NOT NULL AND generation IS NOT NULL)
    )
);
CREATE INDEX oidc_flows_expiry ON hive.oidc_flows (expires_at);
