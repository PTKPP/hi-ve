SET LOCAL lock_timeout='5s';
-- Realtime owns the subscription and its consumption cursor. Session
-- deletion also covers logout, rotation and tenant/member fencing.
CREATE TABLE hive.web_push_subscriptions (
 endpoint text PRIMARY KEY CHECK(length(endpoint)<=2048),
 tenant_id uuid NOT NULL,
 actor_id uuid NOT NULL,
 session_digest bytea NOT NULL REFERENCES hive.tenant_sessions(digest) ON DELETE CASCADE,
 p256dh text NOT NULL,
 auth text NOT NULL,
 last_sequence bigint NOT NULL CHECK(last_sequence>=0),
 setting_revision bigint NOT NULL,
 entitlement_revision bigint NOT NULL,
 FOREIGN KEY(tenant_id,actor_id) REFERENCES hive.tenant_memberships
);
CREATE INDEX web_push_session ON hive.web_push_subscriptions(session_digest);
CREATE INDEX web_push_owner ON hive.web_push_subscriptions(tenant_id,actor_id);
CREATE INDEX realtime_sale_push_sequence ON hive.realtime_events(tenant_id,sequence) WHERE kind='sale.received';
