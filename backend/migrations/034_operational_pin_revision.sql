-- Owner: Devices. Existing data and binaries remain compatible.
SET LOCAL lock_timeout='5s';
ALTER TABLE hive.operational_contexts ADD COLUMN credential_revision bigint CHECK(credential_revision > 0);
