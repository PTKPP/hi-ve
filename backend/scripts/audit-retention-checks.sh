#!/usr/bin/env bash
# Sourced by test-migrations.sh after migrations are applied. Needs: sql, expect_sql, docker_local, $container.
# Proves OPEN-006 O6-1/O6-2 on a disposable PostgreSQL only. No persistent target is touched.

ar_psql() {
    docker_local exec -i "$container" psql -XAt -v ON_ERROR_STOP=1 -v VERBOSITY=verbose -U postgres -d "$1" -c "$2"
}
ar_expect_error() {
    local db=$1 code=$2 statement=$3 output
    if output=$(ar_psql "$db" "$statement" 2>&1); then
        printf 'Expected SQLSTATE %s, statement succeeded: %s\n' "$code" "$statement" >&2
        return 1
    fi
    if ! grep -q "ERROR:  $code" <<<"$output"; then
        printf 'Expected SQLSTATE %s, got: %s\nfor: %s\n' "$code" "$output" "$statement" >&2
        return 1
    fi
}
ar_run() { ar_psql "$1" "$2" >/dev/null; }

audit_retention_checks() {
    local db=$1
    local role
    for role in hive_ar_runtime hive_ar_retention hive_ar_ops hive_ar_other; do
        ar_run "$db" "DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='$role') THEN CREATE ROLE $role NOLOGIN; END IF; END \$\$"
    done
    ar_run "$db" "GRANT USAGE ON SCHEMA hive TO hive_ar_runtime, hive_ar_retention, hive_ar_ops, hive_ar_other;
        GRANT INSERT, SELECT ON hive.provisioning_audit TO hive_ar_runtime;
        GRANT EXECUTE ON FUNCTION hive.purge_critical_audit(text,integer,boolean) TO hive_ar_retention;
        GRANT INSERT, SELECT ON hive.audit_legal_holds, hive.audit_legal_hold_releases TO hive_ar_ops;
        GRANT SELECT ON hive.audit_purge_runs, hive.tenants TO hive_ar_ops"

    # Fixtures: 20 recent filler rows, tenant A (3 old, 1 recent), tenant B (2 old, will be held).
    ar_run "$db" "INSERT INTO hive.tenants(display_name,status) VALUES ('ar-a','active'),('ar-b','active'),('ar-c','active')"
    ar_run "$db" "INSERT INTO hive.provisioning_audit(action,outcome,occurred_at)
        SELECT 'ar.filler','succeeded',clock_timestamp()-interval '1 day' FROM generate_series(1,20)"
    ar_run "$db" "INSERT INTO hive.provisioning_audit(tenant_id,action,outcome,occurred_at)
        SELECT t.id,'ar.old','succeeded',clock_timestamp()-interval '25 months' FROM hive.tenants t, generate_series(1,3)
        WHERE t.display_name='ar-a'"
    ar_run "$db" "INSERT INTO hive.provisioning_audit(tenant_id,action,outcome,occurred_at)
        SELECT t.id,'ar.recent','succeeded',clock_timestamp()-interval '23 months' FROM hive.tenants t WHERE t.display_name='ar-a'"
    ar_run "$db" "INSERT INTO hive.provisioning_audit(tenant_id,action,outcome,occurred_at)
        SELECT t.id,'ar.old','succeeded',clock_timestamp()-interval '25 months' FROM hive.tenants t, generate_series(1,2)
        WHERE t.display_name='ar-b'"
    local count_a="SELECT count(*) FROM hive.provisioning_audit p JOIN hive.tenants t ON t.id=p.tenant_id WHERE t.display_name='ar-a' AND p.action='ar.old'"
    local count_b="SELECT count(*) FROM hive.provisioning_audit p JOIN hive.tenants t ON t.id=p.tenant_id WHERE t.display_name='ar-b' AND p.action='ar.old'"
    local purge="SELECT r_outcome||'/'||r_reason||'/'||r_rows FROM hive.purge_critical_audit('provisioning_audit',%s,%s)"
    run_purge() { ar_psql "$db" "SET ROLE hive_ar_retention; $(printf "$purge" "$1" "$2")" | tail -n1; }

    # Hold tenant B before any purge. Placement is an INSERT by the ops role only.
    ar_run "$db" "SET ROLE hive_ar_ops; INSERT INTO hive.audit_legal_holds(scope,tenant_id,reason_code)
        SELECT 'tenant',id,'ar.hold-b' FROM hive.tenants WHERE display_name='ar-b'"

    # A fixed transaction snapshot cannot observe a newly committed hold after waiting.
    local level
    for level in 'REPEATABLE READ' 'SERIALIZABLE'; do
        ar_expect_error "$db" 25000 "BEGIN ISOLATION LEVEL $level; SET ROLE hive_ar_retention;
            SELECT pg_current_snapshot(); SELECT * FROM hive.purge_critical_audit('provisioning_audit',2,false)"
    done

    # Positive: a dry run reports the eligible rows, deletes nothing and writes the ledger.
    [[ $(run_purge 500 false) == 'ok/completed/3' ]]
    expect_sql "$db" "$count_a" 3
    expect_sql "$db" "SELECT count(*) FROM hive.audit_purge_runs WHERE mode='dry_run' AND outcome='ok'" 1
    # Negative: apply while the policy is disabled refuses and deletes nothing.
    [[ $(run_purge 500 true) == 'refused/disabled/0' ]]
    expect_sql "$db" "$count_a" 3
    expect_sql "$db" "SELECT count(*) FROM hive.audit_purge_runs WHERE reason='disabled'" 1

    # Policy guard: only enabled may change; the 24 month floor is a CHECK.
    ar_expect_error "$db" 42501 "UPDATE hive.audit_retention_policy SET min_age=interval '1 day'"
    ar_expect_error "$db" 23514 "ALTER TABLE hive.audit_retention_policy DISABLE TRIGGER audit_policy_fixed; UPDATE hive.audit_retention_policy SET min_age=interval '1 day'"
    ar_expect_error "$db" 42501 "DELETE FROM hive.audit_retention_policy"
    ar_expect_error "$db" 0A000 "TRUNCATE hive.audit_retention_policy"  # FK from the ledger rejects it before the trigger
    ar_run "$db" "ALTER TABLE hive.audit_retention_policy ENABLE TRIGGER audit_policy_fixed"
    ar_run "$db" "UPDATE hive.audit_retention_policy SET enabled=true WHERE table_name='provisioning_audit'"

    # Positive: batches delete only expired, unheld rows. Held tenant and recent rows remain.
    [[ $(run_purge 2 true) == 'ok/completed/2' ]]
    expect_sql "$db" "$count_a" 1
    [[ $(run_purge 2 true) == 'ok/completed/1' ]]
    [[ $(run_purge 2 true) == 'ok/completed/0' ]]
    expect_sql "$db" "$count_a" 0
    expect_sql "$db" "$count_b" 2
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action IN ('ar.recent','ar.filler')" 21

    # Negative: input validation, no public execute, no direct writes by any role.
    ar_expect_error "$db" 22023 "SELECT * FROM hive.purge_critical_audit('provisioning_audit; DROP TABLE hive.tenants',10,false)"
    ar_expect_error "$db" 22023 "SELECT * FROM hive.purge_critical_audit('tenants',10,false)"
    ar_expect_error "$db" 22023 "SELECT * FROM hive.purge_critical_audit('provisioning_audit',0,false)"
    ar_expect_error "$db" 22023 "SELECT * FROM hive.purge_critical_audit('provisioning_audit',1001,false)"
    ar_expect_error "$db" 42501 "SET ROLE hive_ar_other; SELECT * FROM hive.purge_critical_audit('provisioning_audit',10,false)"
    ar_expect_error "$db" 42501 "SET ROLE hive_ar_runtime; SELECT * FROM hive.purge_critical_audit('provisioning_audit',10,false)"
    for role in hive_ar_runtime hive_ar_retention hive_ar_other; do
        ar_expect_error "$db" 42501 "SET ROLE $role; UPDATE hive.provisioning_audit SET action=action"
        ar_expect_error "$db" 42501 "SET ROLE $role; DELETE FROM hive.provisioning_audit"
        ar_expect_error "$db" 42501 "SET ROLE $role; TRUNCATE hive.provisioning_audit"
    done
    ar_run "$db" "SET ROLE hive_ar_runtime; INSERT INTO hive.provisioning_audit(action,outcome) VALUES ('ar.runtime','succeeded')"
    # Even the table owner cannot mutate outside the purge function.
    ar_expect_error "$db" 42501 "UPDATE hive.provisioning_audit SET action=action"
    ar_expect_error "$db" 42501 "DELETE FROM hive.provisioning_audit WHERE action='ar.runtime'"
    ar_expect_error "$db" 42501 "TRUNCATE hive.provisioning_audit"
    ar_expect_error "$db" 42501 "UPDATE hive.audit_legal_holds SET reason_code='ar.changed'"
    ar_expect_error "$db" 42501 "UPDATE hive.audit_purge_runs SET outcome='ok'"
    ar_expect_error "$db" 42501 "DELETE FROM hive.audit_purge_runs"
    # The flag alone is not enough for a role other than the function owner.
    ar_expect_error "$db" 42501 "SET ROLE hive_ar_ops; SELECT set_config('hive.audit_purge','on',false); DELETE FROM hive.audit_legal_holds"

    # Hijack attempt: a decoy table earlier in search_path does not change the policy that is read.
    ar_run "$db" "CREATE TABLE public.audit_retention_policy (table_name text, min_age interval, max_purge_fraction numeric, enabled boolean)"
    ar_run "$db" "INSERT INTO public.audit_retention_policy VALUES ('entitlement_audit', interval '0', 1, true)"
    [[ $(ar_psql "$db" "SET search_path=public,hive; SELECT r_outcome||'/'||r_reason FROM hive.purge_critical_audit('entitlement_audit',10,true)" | tail -n1) == 'refused/disabled' ]]
    ar_run "$db" "DROP TABLE public.audit_retention_policy"

    # Global hold refuses everything; release restores normal behaviour.
    ar_run "$db" "SET ROLE hive_ar_ops; INSERT INTO hive.audit_legal_holds(scope,reason_code) VALUES ('global','ar.global')"
    ar_run "$db" "INSERT INTO hive.provisioning_audit(action,outcome,occurred_at) VALUES ('ar.global-old','succeeded',clock_timestamp()-interval '30 months')"
    [[ $(run_purge 500 true) == 'refused/legal_hold_global/0' ]]
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action='ar.global-old'" 1
    ar_run "$db" "SET ROLE hive_ar_ops; INSERT INTO hive.audit_legal_hold_releases(hold_id,reason_code)
        SELECT id,'ar.released' FROM hive.audit_legal_holds WHERE scope='global'"
    [[ $(run_purge 500 true) == 'ok/completed/1' ]]
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action='ar.global-old'" 0
    ar_expect_error "$db" 42501 "DELETE FROM hive.audit_legal_hold_releases"
    ar_expect_error "$db" 42501 "UPDATE hive.audit_legal_hold_releases SET reason_code='ar.changed'"
    expect_sql "$db" "$count_b" 2

    # Fail-closed: a ledger failure rolls the delete back.
    ar_run "$db" "INSERT INTO hive.provisioning_audit(action,outcome,occurred_at) VALUES ('ar.ledger-old','succeeded',clock_timestamp()-interval '30 months')"
    ar_run "$db" "ALTER TABLE hive.audit_purge_runs ADD CONSTRAINT ar_block_ledger CHECK (false) NOT VALID"
    ar_expect_error "$db" 23514 "SELECT * FROM hive.purge_critical_audit('provisioning_audit',500,true)"
    ar_run "$db" "ALTER TABLE hive.audit_purge_runs DROP CONSTRAINT ar_block_ledger"
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action='ar.ledger-old'" 1

    # Fail-closed: a ledger entry from the future means the clock moved backwards.
    ar_run "$db" "UPDATE hive.audit_retention_policy SET enabled=true WHERE table_name='membership_lifecycle_audit'"
    ar_run "$db" "INSERT INTO hive.audit_purge_runs(table_name,mode,outcome,reason,started_at)
        VALUES ('membership_lifecycle_audit','purge','ok','completed',clock_timestamp()+interval '1 day')"
    [[ $(ar_psql "$db" "SELECT r_outcome||'/'||r_reason FROM hive.purge_critical_audit('membership_lifecycle_audit',10,true)" | tail -n1) == 'refused/clock_backwards' ]]

    # Concurrency: a hold committing while a purge waits is honoured; two purgers never overlap.
    ar_run "$db" "INSERT INTO hive.provisioning_audit(tenant_id,action,outcome,occurred_at)
        SELECT id,'ar.race-old','succeeded',clock_timestamp()-interval '30 months' FROM hive.tenants WHERE display_name='ar-c'"
    ar_psql "$db" "BEGIN; INSERT INTO hive.audit_legal_holds(scope,tenant_id,reason_code)
        SELECT 'tenant',id,'ar.hold-c' FROM hive.tenants WHERE display_name='ar-c'; SELECT pg_sleep(2); COMMIT" >/dev/null &
    local holder=$!
    sleep 1
    [[ $(run_purge 500 true) == 'ok/completed/1' ]]
    wait "$holder"
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action='ar.race-old'" 1
    # The remaining deleted row was the ledger-old row; the held tenant row stayed.
    ar_psql "$db" "BEGIN; SELECT * FROM hive.purge_critical_audit('provisioning_audit',10,false); SELECT pg_sleep(2); COMMIT" >/dev/null &
    local first=$!
    sleep 1
    [[ $(ar_psql "$db" "SELECT r_outcome||'/'||r_reason FROM hive.purge_critical_audit('provisioning_audit',10,false)" | tail -n1) == 'refused/busy' ]]
    wait "$first"

    # Fail-closed: an anomalous proportion of expired rows is refused and nothing is deleted.
    ar_run "$db" "INSERT INTO hive.provisioning_audit(action,outcome,occurred_at)
        SELECT 'ar.bulk','succeeded',clock_timestamp()-interval '40 months' FROM generate_series(1,200)"
    [[ $(run_purge 500 true) == 'refused/purge_fraction_exceeded/0' ]]
    expect_sql "$db" "SELECT count(*) FROM hive.provisioning_audit WHERE action='ar.bulk'" 200

    expect_sql "$db" "SELECT count(*) FROM hive.audit_purge_runs WHERE outcome='refused'" 5
    printf 'PASS: audit retention foundation keeps critical audit append-only and purges only fail-closed\n'
}
