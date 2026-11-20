-- Owner: Audit. OPEN-006 critical audit retention foundation. Additive only.
-- Purge stays disabled (enabled=false) and nothing is deleted by this migration.
SET LOCAL lock_timeout='5s';

CREATE INDEX provisioning_audit_retention ON hive.provisioning_audit (occurred_at);
CREATE INDEX tenant_rbac_audit_retention ON hive.tenant_rbac_audit (occurred_at);
CREATE INDEX entitlement_audit_retention ON hive.entitlement_audit (occurred_at);
CREATE INDEX membership_lifecycle_audit_retention ON hive.membership_lifecycle_audit (occurred_at);
CREATE INDEX location_create_audit_retention ON hive.location_create_audit (occurred_at);
CREATE INDEX session_logout_audit_retention ON hive.session_logout_audit (occurred_at);
CREATE INDEX device_pairing_audit_retention ON hive.device_pairing_audit (occurred_at);
CREATE INDEX operational_device_audit_retention ON hive.operational_device_audit (occurred_at);
CREATE INDEX receipt_settings_audit_retention ON hive.receipt_settings_audit (occurred_at);
CREATE INDEX staff_pin_management_events_retention ON hive.staff_pin_management_events (occurred_at);

-- One row per critical audit table. min_age cannot be lowered below 24 months.
CREATE TABLE hive.audit_retention_policy (
    table_name text PRIMARY KEY,
    min_age interval NOT NULL CHECK (min_age >= interval '24 months'),
    max_purge_fraction numeric NOT NULL DEFAULT 0.5 CHECK (max_purge_fraction > 0 AND max_purge_fraction <= 1),
    enabled boolean NOT NULL DEFAULT false
);
INSERT INTO hive.audit_retention_policy (table_name, min_age) VALUES
    ('provisioning_audit', interval '24 months'), ('tenant_rbac_audit', interval '24 months'),
    ('entitlement_audit', interval '24 months'), ('membership_lifecycle_audit', interval '24 months'),
    ('location_create_audit', interval '24 months'), ('session_logout_audit', interval '24 months'),
    ('device_pairing_audit', interval '24 months'), ('operational_device_audit', interval '24 months'),
    ('receipt_settings_audit', interval '24 months'), ('staff_pin_management_events', interval '24 months');

-- Legal holds are append-only events. A hold is active until a release row exists.
CREATE TABLE hive.audit_legal_holds (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scope text NOT NULL CHECK (scope IN ('tenant', 'global')),
    tenant_id uuid REFERENCES hive.tenants,
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z0-9_.-]{3,64}$'),
    placed_by text NOT NULL DEFAULT session_user,
    placed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK ((scope = 'tenant') = (tenant_id IS NOT NULL))
);
CREATE INDEX audit_legal_holds_tenant ON hive.audit_legal_holds (tenant_id);
CREATE TABLE hive.audit_legal_hold_releases (
    hold_id uuid PRIMARY KEY REFERENCES hive.audit_legal_holds,
    reason_code text NOT NULL CHECK (reason_code ~ '^[a-z0-9_.-]{3,64}$'),
    released_by text NOT NULL DEFAULT session_user,
    released_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Purge ledger. It is never purged by the retention job.
CREATE TABLE hive.audit_purge_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name text NOT NULL REFERENCES hive.audit_retention_policy,
    mode text NOT NULL CHECK (mode IN ('dry_run', 'purge')),
    outcome text NOT NULL CHECK (outcome IN ('ok', 'refused')),
    reason text NOT NULL CHECK (reason ~ '^[a-z_]{2,40}$'),
    cutoff timestamptz,
    eligible_rows bigint NOT NULL DEFAULT 0 CHECK (eligible_rows >= 0),
    deleted_rows bigint NOT NULL DEFAULT 0 CHECK (deleted_rows >= 0),
    run_by text NOT NULL DEFAULT session_user,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_purge_runs_table_time ON hive.audit_purge_runs (table_name, started_at DESC);

-- Fail-closed batch purge. Identifiers come only from a constant allowlist.
-- SECURITY DEFINER runs as the owner; search_path is pinned and EXECUTE is not public.
CREATE FUNCTION hive.purge_critical_audit(p_table text, p_batch integer DEFAULT 500, p_apply boolean DEFAULT false)
RETURNS TABLE (r_outcome text, r_reason text, r_rows bigint)
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, hive, pg_temp
SET lock_timeout = '5s'
SET statement_timeout = '120s'
AS $$
DECLARE
    v_allowed constant text[] := ARRAY['provisioning_audit', 'tenant_rbac_audit', 'entitlement_audit',
        'membership_lifecycle_audit', 'location_create_audit', 'session_logout_audit', 'device_pairing_audit',
        'operational_device_audit', 'receipt_settings_audit', 'staff_pin_management_events'];
    v_held constant text := 't.tenant_id IS NOT NULL AND EXISTS (SELECT 1 FROM hive.audit_legal_holds h
        WHERE h.scope = ''tenant'' AND h.tenant_id = t.tenant_id
        AND NOT EXISTS (SELECT 1 FROM hive.audit_legal_hold_releases r WHERE r.hold_id = h.id))';
    v_policy hive.audit_retention_policy%ROWTYPE;
    v_started timestamptz := clock_timestamp();
    v_mode text := CASE WHEN p_apply THEN 'purge' ELSE 'dry_run' END;
    v_cutoff timestamptz;
    v_eligible bigint := 0;
    v_total bigint := 0;
    v_deleted bigint := 0;
    v_last timestamptz;
    v_refusal text;
BEGIN
    IF p_table IS NULL OR NOT (p_table = ANY (v_allowed)) THEN
        RAISE EXCEPTION 'unknown critical audit table' USING ERRCODE = '22023';
    END IF;
    IF p_batch IS NULL OR p_batch < 1 OR p_batch > 1000 THEN
        RAISE EXCEPTION 'invalid purge batch size' USING ERRCODE = '22023';
    END IF;
    IF pg_is_in_recovery() THEN
        RETURN QUERY SELECT 'refused'::text, 'replica'::text, 0::bigint;
        RETURN;
    END IF;
    IF NOT pg_try_advisory_xact_lock(hashtextextended('hive.audit_purge', 0)) THEN
        RETURN QUERY SELECT 'refused'::text, 'busy'::text, 0::bigint;
        RETURN;
    END IF;

    SELECT * INTO v_policy FROM hive.audit_retention_policy WHERE table_name = p_table;
    IF NOT FOUND OR v_policy.min_age < interval '24 months' THEN
        v_refusal := 'policy_invalid';
    ELSIF p_apply AND NOT v_policy.enabled THEN
        v_refusal := 'disabled';
    END IF;

    -- Serialize with hold placement so a committed hold is always visible to the statements below.
    IF v_refusal IS NULL THEN
        LOCK TABLE hive.audit_legal_holds IN SHARE MODE;
        IF EXISTS (SELECT 1 FROM hive.audit_legal_holds h WHERE h.scope = 'global'
            AND NOT EXISTS (SELECT 1 FROM hive.audit_legal_hold_releases r WHERE r.hold_id = h.id)) THEN
            v_refusal := 'legal_hold_global';
        END IF;
    END IF;

    IF v_refusal IS NULL THEN
        SELECT max(started_at) INTO v_last FROM hive.audit_purge_runs WHERE table_name = p_table AND outcome = 'ok';
        IF v_last IS NOT NULL AND v_last > v_started THEN
            v_refusal := 'clock_backwards';
        END IF;
    END IF;

    IF v_refusal IS NULL THEN
        v_cutoff := v_started - v_policy.min_age;
        EXECUTE format('SELECT count(*) FROM hive.%I t WHERE t.occurred_at < $1 AND NOT (%s)', p_table, v_held)
            INTO v_eligible USING v_cutoff;
        EXECUTE format('SELECT count(*) FROM hive.%I', p_table) INTO v_total;
        IF v_total > 0 AND v_eligible::numeric / v_total > v_policy.max_purge_fraction THEN
            v_refusal := 'purge_fraction_exceeded';
        END IF;
    END IF;

    IF v_refusal IS NULL AND p_apply THEN
        PERFORM set_config('hive.audit_purge', 'on', true);
        EXECUTE format('WITH doomed AS (SELECT t.ctid AS row_id FROM hive.%1$I t
                WHERE t.occurred_at < $1 AND NOT (%2$s) ORDER BY t.occurred_at LIMIT $2 FOR UPDATE SKIP LOCKED)
            DELETE FROM hive.%1$I d USING doomed WHERE d.ctid = doomed.row_id', p_table, v_held)
            USING v_cutoff, p_batch;
        GET DIAGNOSTICS v_deleted = ROW_COUNT;
        PERFORM set_config('hive.audit_purge', 'off', true);
    END IF;

    -- Delete and ledger share this transaction: a ledger failure rolls the delete back.
    INSERT INTO hive.audit_purge_runs (table_name, mode, outcome, reason, cutoff, eligible_rows, deleted_rows, started_at)
    VALUES (p_table, v_mode, CASE WHEN v_refusal IS NULL THEN 'ok' ELSE 'refused' END,
        COALESCE(v_refusal, 'completed'), v_cutoff, v_eligible, v_deleted, v_started);
    RETURN QUERY SELECT CASE WHEN v_refusal IS NULL THEN 'ok' ELSE 'refused' END::text,
        COALESCE(v_refusal, 'completed')::text, CASE WHEN p_apply THEN v_deleted ELSE v_eligible END;
END
$$;
REVOKE ALL ON FUNCTION hive.purge_critical_audit(text, integer, boolean) FROM PUBLIC;

-- Defense in depth: UPDATE and TRUNCATE never pass. DELETE passes only inside the purge function.
CREATE FUNCTION hive.audit_append_only_guard() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, hive, pg_temp
AS $$
BEGIN
    IF TG_OP = 'DELETE'
        AND TG_TABLE_NAME NOT IN ('audit_legal_holds', 'audit_legal_hold_releases', 'audit_purge_runs')
        AND current_setting('hive.audit_purge', true) = 'on'
        AND current_user = (SELECT pg_get_userbyid(proowner) FROM pg_proc
            WHERE oid = 'hive.purge_critical_audit(text,integer,boolean)'::regprocedure) THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'critical audit is append-only' USING ERRCODE = '42501';
END
$$;

CREATE FUNCTION hive.audit_policy_guard() RETURNS trigger LANGUAGE plpgsql
SET search_path = pg_catalog, hive, pg_temp
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.table_name = OLD.table_name AND NEW.min_age = OLD.min_age
            AND NEW.max_purge_fraction = OLD.max_purge_fraction THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION 'audit retention policy is fixed except enabled' USING ERRCODE = '42501';
END
$$;

CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.provisioning_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.tenant_rbac_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.entitlement_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.membership_lifecycle_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.location_create_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.session_logout_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.device_pairing_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.operational_device_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.receipt_settings_audit FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.staff_pin_management_events FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.audit_legal_holds FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.audit_legal_hold_releases FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_append_only BEFORE UPDATE OR DELETE ON hive.audit_purge_runs FOR EACH ROW EXECUTE FUNCTION hive.audit_append_only_guard();

CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.provisioning_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.tenant_rbac_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.entitlement_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.membership_lifecycle_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.location_create_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.session_logout_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.device_pairing_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.operational_device_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.receipt_settings_audit FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.staff_pin_management_events FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.audit_legal_holds FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.audit_legal_hold_releases FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();
CREATE TRIGGER audit_no_truncate BEFORE TRUNCATE ON hive.audit_purge_runs FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_append_only_guard();

CREATE TRIGGER audit_policy_fixed BEFORE UPDATE OR DELETE ON hive.audit_retention_policy FOR EACH ROW EXECUTE FUNCTION hive.audit_policy_guard();
CREATE TRIGGER audit_policy_no_truncate BEFORE TRUNCATE ON hive.audit_retention_policy FOR EACH STATEMENT EXECUTE FUNCTION hive.audit_policy_guard();
