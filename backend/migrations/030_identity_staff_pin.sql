-- Owner: Identity. Staff PIN state; Google sessions and Owner anchors are unchanged.
SET LOCAL lock_timeout = '5s';
CREATE TABLE hive.tenant_staff_profiles (
    tenant_id uuid NOT NULL, member_id uuid NOT NULL,
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 80),
    PRIMARY KEY (tenant_id,member_id),
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_memberships
);
CREATE TABLE hive.member_pin_credentials (
    tenant_id uuid NOT NULL, member_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    pepper_version integer, salt bytea, verifier bytea,
    PRIMARY KEY (tenant_id,member_id),
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_staff_profiles,
    CHECK (CASE WHEN verifier IS NULL THEN salt IS NULL AND pepper_version IS NULL
      ELSE salt IS NOT NULL AND octet_length(salt)=16 AND octet_length(verifier)=32
      AND pepper_version IS NOT NULL AND pepper_version>0 END)
);
CREATE TABLE hive.member_pin_attempt_state (
    tenant_id uuid NOT NULL, member_id uuid NOT NULL,
    failures integer NOT NULL DEFAULT 0 CHECK (failures BETWEEN 0 AND 5),
    locked_until timestamptz,
    attempt_id uuid, lease_until timestamptz,
    PRIMARY KEY (tenant_id,member_id),
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.member_pin_credentials,
    CHECK ((failures=5)=(locked_until IS NOT NULL)),
    CHECK ((attempt_id IS NULL)=(lease_until IS NULL))
);
CREATE TABLE hive.staff_profile_receipts (
    tenant_id uuid NOT NULL, actor_id uuid NOT NULL, request_key text NOT NULL,
    fingerprint bytea NOT NULL CHECK (octet_length(fingerprint)=32), member_id uuid NOT NULL,
    PRIMARY KEY (tenant_id,actor_id,request_key),
    FOREIGN KEY (tenant_id,actor_id) REFERENCES hive.tenant_memberships,
    FOREIGN KEY (tenant_id,member_id) REFERENCES hive.tenant_staff_profiles DEFERRABLE INITIALLY DEFERRED
);
