-- Owner: Identity. Support bounded deletion of expired session digests.
SET LOCAL lock_timeout = '5s';
CREATE INDEX platform_sessions_expiry ON hive.platform_sessions (expires_at);
CREATE INDEX tenant_sessions_expiry ON hive.tenant_sessions (expires_at);
