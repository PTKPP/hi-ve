-- Fixture credentials. Only the script's new disposable database runs this file.
CREATE TABLE public.hive_disposable_test ();
CREATE TABLE public.hive_m5_rehearsal_marker ();
CREATE ROLE hive_m5_runtime LOGIN PASSWORD 'hive_m5_runtime_test' NOSUPERUSER NOCREATEDB NOCREATEROLE;
GRANT CONNECT ON DATABASE hive_test TO hive_m5_runtime;
GRANT USAGE ON SCHEMA hive TO hive_m5_runtime;
GRANT SELECT ON public.hive_schema_version TO hive_m5_runtime;

GRANT SELECT ON hive.active_tenants TO hive_m5_runtime;
-- Owner login locks the active-tenant view.
GRANT UPDATE (id) ON hive.active_tenants TO hive_m5_runtime;
GRANT SELECT,INSERT,UPDATE ON hive.tenants,hive.owner_candidates,hive.owner_invitations,hive.owner_claims,
    hive.google_principals,hive.tenant_memberships,hive.provisioning_receipts,hive.membership_lifecycle_receipts,
    hive.tenant_rbac_revisions TO hive_m5_runtime;
GRANT INSERT ON hive.identity_principals TO hive_m5_runtime;
-- SELECT FOR SHARE uses an UPDATE privilege. Role authorization remains in Identity.
GRANT SELECT ON hive.platform_principals,hive.platform_permissions TO hive_m5_runtime;
GRANT UPDATE (active) ON hive.platform_principals TO hive_m5_runtime;
GRANT UPDATE (permission) ON hive.platform_permissions TO hive_m5_runtime;
GRANT SELECT,INSERT,UPDATE,DELETE ON hive.oidc_flows,hive.platform_sessions,hive.tenant_sessions TO hive_m5_runtime;
GRANT SELECT,DELETE ON hive.tenant_role_assignments,hive.tenant_location_role_assignments,
    hive.device_operator_assignments TO hive_m5_runtime;
GRANT SELECT,UPDATE ON hive.operational_contexts TO hive_m5_runtime;
GRANT INSERT ON hive.provisioning_audit,hive.tenant_rbac_audit,hive.membership_lifecycle_audit,
    hive.session_logout_audit TO hive_m5_runtime;
GRANT SELECT,INSERT,DELETE ON hive.online_login_history TO hive_m5_runtime;
