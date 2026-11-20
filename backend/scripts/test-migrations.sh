#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# Always provision the target here. No caller-supplied database connection is used.
docker_local() {
    env -u DOCKER_HOST -u DOCKER_CONTEXT docker --context default "$@"
}
if [[ $(docker_local context inspect default --format '{{(index .Endpoints "docker").Host}}') != unix://* ]]; then
    printf 'Disposable tests require a local Docker socket.\n' >&2
    exit 1
fi
scratch=$(mktemp -d)
container=
cleanup() {
    if [[ -n "$container" ]]; then docker_local rm -f "$container" >/dev/null; fi
    rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

container=$(docker_local run --rm -d -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql     -e POSTGRES_PASSWORD=hive_disposable_test -e POSTGRES_DB=hive_test     postgres:18@sha256:e754b839f447639b7004895f4d8a4bfcb4e9dbe3a1bc035f27f646562a16fd61)
port=$(docker_local port "$container" 5432/tcp)
for ((attempt = 0; attempt < 60; attempt++)); do
    if docker_local exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test >/dev/null; then
        break
    fi
    sleep 1
done
docker_local exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test

sql() {
    docker_local exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U postgres -d "$1" -c "$2"
}
migrate() {
    # Explicit config and connection settings isolate the runner from local defaults.
    env -i PATH="$PATH" tern migrate --config migrations/tern.conf \
        --conn-string "postgres://postgres:hive_disposable_test@$port/$1?sslmode=disable&connect_timeout=5&lock_timeout=10000&statement_timeout=30000" \
        --migrations "$2"
}
expect_sql() {
    local actual
    actual=$(sql "$1" "$2")
    if [[ "$actual" != "$3" ]]; then
        printf 'Expected <%s>, got <%s> for %s\n' "$3" "$actual" "$2" >&2
        return 1
    fi
}

mkdir "$scratch/previous" "$scratch/next"
cp migrations/001_create_hive_schema.sql "$scratch/previous/"
cp migrations/*.sql "$scratch/next/"
migrations=(migrations/*.sql)
latest=${#migrations[@]}
version='SELECT version FROM public.hive_schema_version'

printf 'PostgreSQL %s\n' "$(sql hive_test 'SHOW server_version')"
migrate hive_test migrations
expect_sql hive_test "$version" "$latest"
expect_sql hive_test "SELECT to_regnamespace('hive')::text" 'hive'
migrate hive_test migrations
expect_sql hive_test "$version" "$latest"
printf 'PASS: fresh migration and retry\n'

source scripts/audit-retention-checks.sh
sql hive_test 'CREATE DATABASE hive_retention'
migrate hive_retention migrations
audit_retention_checks hive_retention

sql hive_test 'CREATE DATABASE hive_upgrade'
migrate hive_upgrade "$scratch/previous"
expect_sql hive_upgrade "$version" 1
sql hive_upgrade "CREATE TABLE hive.migration_test_fixture (id integer PRIMARY KEY); INSERT INTO hive.migration_test_fixture VALUES (17)"
printf 'ALTER TABLE hive.migration_test_fixture ADD COLUMN note text;\n' > "$(printf '%s/%03d_test_expand.sql' "$scratch/next" "$((latest + 1))")"

migrate hive_upgrade "$scratch/next" &
first=$!
migrate hive_upgrade "$scratch/next" &
second=$!
first_status=0
second_status=0
wait "$first" || first_status=$?
wait "$second" || second_status=$?
[[ "$first_status" == 0 && "$second_status" == 0 ]]
expect_sql hive_upgrade "$version" "$((latest + 1))"
expect_sql hive_upgrade 'SELECT id FROM hive.migration_test_fixture WHERE note IS NULL' 17
printf 'PASS: concurrent forward upgrade preserved the previous data\n'

cat > "$(printf '%s/%03d_test_failure.sql' "$scratch/next" "$((latest + 2))")" <<'SQL'
CREATE TABLE hive.migration_test_partial (id integer);
SELECT 1 / 0;
SQL
if migrate hive_upgrade "$scratch/next" >"$scratch/failure.log" 2>&1; then
    printf 'Expected failing migration to be rejected\n' >&2
    exit 1
fi
if ! grep -q 'division by zero' "$scratch/failure.log"; then
    cat "$scratch/failure.log" >&2
    exit 1
fi
expect_sql hive_upgrade "$version" "$((latest + 1))"
expect_sql hive_upgrade "SELECT to_regclass('hive.migration_test_partial') IS NULL" t
printf 'PASS: failed migration rolled back DDL and retained schema version\n'

mkdir "$scratch/provisioning_baseline"
cp migrations/00[1-4]_*.sql "$scratch/provisioning_baseline/"
sql hive_test 'CREATE DATABASE hive_sessions_upgrade'
migrate hive_sessions_upgrade "$scratch/provisioning_baseline"
expect_sql hive_sessions_upgrade "$version" 4
sql hive_sessions_upgrade "WITH principal AS (
    INSERT INTO hive.google_principals (issuer,subject) VALUES ('https://accounts.google.com','session-upgrade-fixture') RETURNING id
), admin AS (
    INSERT INTO hive.platform_principals (principal_id) SELECT id FROM principal RETURNING principal_id
) INSERT INTO hive.platform_sessions (digest,principal_id,expires_at)
  SELECT decode(repeat('a5',32),'hex'),principal_id,statement_timestamp()+interval '1 hour' FROM admin"
cp migrations/005_identity_session_expiry.sql "$scratch/provisioning_baseline/"
migrate hive_sessions_upgrade "$scratch/provisioning_baseline"
expect_sql hive_sessions_upgrade "$version" 5
session_query="SELECT count(*) FROM hive.platform_sessions WHERE digest=decode(repeat('a5',32),'hex') AND expires_at>statement_timestamp()"
expect_sql hive_sessions_upgrade "$session_query" 1
printf 'PASS: session expiry migration preserves active sessions from version 004\n'
sql hive_sessions_upgrade "WITH tenant AS (
    INSERT INTO hive.tenants(display_name,status) VALUES ('legacy membership fixture','active') RETURNING id
) INSERT INTO hive.tenant_memberships (tenant_id,principal_id,role)
  SELECT tenant.id,g.id,'staff' FROM tenant CROSS JOIN hive.google_principals g
  WHERE g.subject='session-upgrade-fixture'"
migrate hive_sessions_upgrade migrations
expect_sql hive_sessions_upgrade "$version" "$latest"
expect_sql hive_sessions_upgrade "$session_query" 1
expect_sql hive_sessions_upgrade 'SELECT count(*) FROM hive.tenant_roles' 0
expect_sql hive_sessions_upgrade 'SELECT count(*) FROM hive.tenant_rbac_audit' 0
printf 'PASS: RBAC schema upgrade preserves sessions and starts without default grants\n'
expect_sql hive_sessions_upgrade "SELECT count(*) FROM hive.tenant_memberships m
    JOIN hive.google_principals g ON g.id=m.principal_id
    JOIN hive.identity_principals p ON p.id=m.principal_id
    WHERE g.subject='session-upgrade-fixture' AND m.active AND m.lifecycle_revision=0" 1
expect_sql hive_sessions_upgrade "SELECT count(*) FROM pg_constraint
    WHERE conrelid='hive.tenant_memberships'::regclass AND convalidated
    AND conname IN ('membership_identity_principal_fk','membership_owner_google_fk','membership_owner_google_shape')" 3
printf 'PASS: principal backfill preserves member IDs and retained constraints\n'
sql hive_sessions_upgrade "CREATE ROLE hive_legacy_identity_fixture NOLOGIN;
    GRANT USAGE ON SCHEMA hive TO hive_legacy_identity_fixture;
    GRANT SELECT, INSERT, UPDATE ON hive.google_principals TO hive_legacy_identity_fixture;
    GRANT INSERT ON hive.identity_principals TO hive_legacy_identity_fixture;
    SET ROLE hive_legacy_identity_fixture;
    INSERT INTO hive.google_principals(issuer, subject)
      VALUES ('https://accounts.google.com','legacy-writer-fixture')
      ON CONFLICT (issuer,subject) DO UPDATE SET subject=EXCLUDED.subject;
    RESET ROLE"
expect_sql hive_sessions_upgrade "SELECT count(*) FROM hive.google_principals g
    JOIN hive.identity_principals p ON p.id=g.id WHERE g.subject='legacy-writer-fixture'" 1
printf 'PASS: legacy Google upsert works with a scoped role after expand\n'

mkdir "$scratch/pairing_baseline"
cp migrations/0{01..15}_*.sql "$scratch/pairing_baseline/"
sql hive_test 'CREATE DATABASE hive_online_upgrade'
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 15
sql hive_online_upgrade "WITH tenant AS (
    INSERT INTO hive.tenants(display_name,status) VALUES ('existing tenant','active') RETURNING id
) INSERT INTO hive.locations(tenant_id,code,name,kind)
  SELECT id,'existing','Existing outlet','outlet' FROM tenant"
cp migrations/0{16..17}_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 17
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.locations WHERE code='existing' AND status='active'" 1
expect_sql hive_online_upgrade 'SELECT count(*) FROM hive.devices' 0
printf 'PASS: device pairing upgrade from 015 preserves authoritative outlet IDs\n'

sql hive_online_upgrade "WITH principal AS (
    INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','existing-owner') RETURNING id
), candidate AS (
    INSERT INTO hive.owner_candidates(tenant_id,contact_email) SELECT tenant_id,'fixture@example.test' FROM hive.locations
    RETURNING tenant_id,id
), membership AS (
    INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role)
    SELECT tenant_id,principal.id,candidate.id,'primary_owner' FROM candidate CROSS JOIN principal RETURNING tenant_id,principal_id
) INSERT INTO hive.tenant_sessions(digest,tenant_id,principal_id,expires_at)
  SELECT decode(repeat('ab',32),'hex'),tenant_id,principal_id,statement_timestamp()+interval '1 hour' FROM membership;
INSERT INTO hive.oidc_flows(state_digest,browser_digest,nonce,verifier,purpose,tenant_id,expires_at)
  SELECT decode(repeat('bc',32),'hex'),decode(repeat('ca',32),'hex'),'nonce','verifier','tenant',tenant_id,statement_timestamp()+interval '10 minutes'
  FROM hive.locations WHERE code='existing'"
cp migrations/0{18..23}_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 23
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_sessions WHERE auth_method='google' AND expires_at>statement_timestamp()" 1
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.oidc_flows WHERE event_id IS NOT NULL AND expires_at>statement_timestamp()" 1
expect_sql hive_online_upgrade 'SELECT count(*) FROM hive.online_login_history' 0
printf 'PASS: history upgrade from 017 preserves Owner sessions and assigns stable IDs to outstanding flows\n'

# Existing active and inactive Owner rows carry no dedicated Google reference before Expand.
sql hive_online_upgrade "WITH tenant AS (
    INSERT INTO hive.tenants(display_name,status) VALUES ('m5 inactive tenant','active') RETURNING id
), principal AS (
    INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','m5-inactive-owner') RETURNING id
), candidate AS (
    INSERT INTO hive.owner_candidates(tenant_id,contact_email)
    SELECT id,'m5-inactive@example.test' FROM tenant RETURNING tenant_id,id
) INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role,active)
    SELECT tenant_id,principal.id,candidate.id,'primary_owner',false FROM candidate CROSS JOIN principal"
cp migrations/024_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 24
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_memberships WHERE role='primary_owner' AND owner_google_principal_id IS NULL" 2

seed_google_owner() {
    local name=$1
    sql hive_online_upgrade "WITH t AS (
        INSERT INTO hive.tenants(display_name,status) VALUES ('$name','active') RETURNING id
    ), p AS (
        INSERT INTO hive.google_principals(issuer,subject) VALUES ('https://accounts.google.com','$name') RETURNING id
    )
    INSERT INTO hive.owner_candidates(tenant_id,contact_email) SELECT t.id,'$name@example.test' FROM t,p"
}
sql hive_online_upgrade "CREATE ROLE hive_m5_legacy_writer NOLOGIN;
    GRANT USAGE ON SCHEMA hive TO hive_m5_legacy_writer;
    GRANT INSERT ON hive.tenant_memberships TO hive_m5_legacy_writer;
    GRANT SELECT ON hive.owner_candidates,hive.google_principals TO hive_m5_legacy_writer"

legacy_owner_insert() {
    local name=$1
    seed_google_owner "$name"
    sql hive_online_upgrade "SET ROLE hive_m5_legacy_writer;
        INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role)
        SELECT c.tenant_id,g.id,c.id,'primary_owner' FROM hive.owner_candidates c CROSS JOIN hive.google_principals g
        WHERE c.contact_email='$name@example.test' AND g.subject='$name';
        RESET ROLE"
}
legacy_owner_insert m5-legacy-expanded
cp migrations/025_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 25
legacy_owner_insert m5-legacy-validated
seed_google_owner m5-current-writer
sql hive_online_upgrade "SET ROLE hive_m5_legacy_writer;
    INSERT INTO hive.tenant_memberships(tenant_id,principal_id,owner_id,role,owner_google_principal_id)
    SELECT c.tenant_id,g.id,c.id,'primary_owner',g.id FROM hive.owner_candidates c CROSS JOIN hive.google_principals g
    WHERE c.contact_email='m5-current-writer@example.test' AND g.subject='m5-current-writer';
    RESET ROLE"
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_memberships WHERE role='primary_owner' AND owner_google_principal_id=principal_id" 5
expect_sql hive_online_upgrade "SELECT count(*) FROM pg_constraint WHERE conrelid='hive.tenant_memberships'::regclass AND convalidated
    AND conname IN ('tenant_memberships_principal_id_fkey','membership_identity_principal_fk','membership_owner_google_shape','membership_owner_google_fk')" 4
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_memberships WHERE role='primary_owner' AND NOT active AND owner_google_principal_id=principal_id" 1
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_sessions WHERE auth_method='google'" 1
sql hive_online_upgrade "DO \$\$ BEGIN
    BEGIN
        UPDATE hive.tenant_memberships SET owner_google_principal_id=gen_random_uuid() WHERE role='primary_owner';
        RAISE EXCEPTION 'mismatched Owner Google reference was accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
END \$\$"
# Model both rollback cases on isolated fixture tables. The actual membership FK stays installed.
sql hive_online_upgrade "BEGIN;
    CREATE TEMP TABLE m5_google (id uuid PRIMARY KEY);
    INSERT INTO m5_google SELECT id FROM hive.google_principals;
    CREATE TEMP TABLE m5_rollback (id uuid PRIMARY KEY);
    INSERT INTO m5_rollback SELECT principal_id FROM hive.tenant_memberships;
    ALTER TABLE m5_rollback ADD CONSTRAINT restored_fk FOREIGN KEY (id) REFERENCES m5_google NOT VALID;
    ALTER TABLE m5_rollback VALIDATE CONSTRAINT restored_fk;
    ALTER TABLE m5_rollback DROP CONSTRAINT restored_fk;
    INSERT INTO m5_rollback VALUES (gen_random_uuid());
    ALTER TABLE m5_rollback ADD CONSTRAINT restored_fk FOREIGN KEY (id) REFERENCES m5_google NOT VALID;
    DO \$\$ BEGIN
        BEGIN
            ALTER TABLE m5_rollback VALIDATE CONSTRAINT restored_fk;
            RAISE EXCEPTION 'rollback accepted neutral-only fixture';
        EXCEPTION WHEN foreign_key_violation THEN NULL;
        END;
    END \$\$;
    ROLLBACK"
printf 'PASS: Owner Google backfill, mixed-version writers, validated constraints and rollback boundaries\n'

cp migrations/0{26..27}_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 27
expect_sql hive_online_upgrade 'SELECT count(*) FROM hive.tenant_receipt_settings' 0
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_sessions WHERE auth_method='google'" 1
printf 'PASS: receipt settings upgrade preserves existing Owner sessions and starts without overrides\n'

cp migrations/028_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 28
expect_sql hive_online_upgrade 'SELECT count(*) FROM hive.device_bootstrap_receipts' 0
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_sessions WHERE auth_method='google'" 1
printf 'PASS: upgrade 027 to device bootstrap preserves existing sessions\n'

cp migrations/029_*.sql "$scratch/pairing_baseline/"
migrate hive_online_upgrade "$scratch/pairing_baseline"
expect_sql hive_online_upgrade "$version" 29
migrate hive_online_upgrade migrations
expect_sql hive_online_upgrade "$version" "$latest"
legacy_fk="SELECT count(*) FROM pg_constraint WHERE conrelid='hive.tenant_memberships'::regclass
    AND conname='tenant_memberships_principal_id_fkey'"
expect_sql hive_online_upgrade "$legacy_fk" 0
expect_sql hive_test "$legacy_fk" 0
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_memberships WHERE role='primary_owner' AND owner_google_principal_id=principal_id" 5
expect_sql hive_online_upgrade "SELECT count(*) FROM hive.tenant_sessions WHERE auth_method='google'" 1
printf 'PASS: fresh and upgrade through 029 remove the old FK and preserve Owner anchors and sessions\n'

sql hive_test 'CREATE TABLE public.hive_disposable_test ()'
HIVE_DISPOSABLE_TEST_DATABASE="postgres://postgres:hive_disposable_test@$port/hive_test?sslmode=disable&connect_timeout=5" \
    go test -race -count=1 ./internal/transport -run '^Test(Provisioning|TenantRBAC|Entitlement|Membership|Locations|Logout|Devices|LoginHistory|LocationGrants|OperationalContext|StaffPIN|OperatorPIN|ReceiptSettings|AccessControlAudit)Postgres$'
