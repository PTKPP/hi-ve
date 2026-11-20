-- Owner: Identity. Fence PIN credentials across lifecycle changes by rollback binaries.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.member_pin_credentials ADD COLUMN membership_revision bigint NOT NULL DEFAULT 0 CHECK(membership_revision>=0);
