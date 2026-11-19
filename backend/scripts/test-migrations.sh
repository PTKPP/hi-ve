#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# Always provision the target here. No caller-supplied database connection is used.
scratch=$(mktemp -d)
container=
cleanup() {
    if [[ -n "$container" ]]; then docker rm -f "$container" >/dev/null; fi
    rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

container=$(docker run --rm -d -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql     -e POSTGRES_PASSWORD=hive_disposable_test -e POSTGRES_DB=hive_test     postgres:18@sha256:e754b839f447639b7004895f4d8a4bfcb4e9dbe3a1bc035f27f646562a16fd61)
port=$(docker port "$container" 5432/tcp)
for ((attempt = 0; attempt < 60; attempt++)); do
    if docker exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test >/dev/null; then
        break
    fi
    sleep 1
done
docker exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test

sql() {
    docker exec "$container" psql -XAt -v ON_ERROR_STOP=1 -U postgres -d "$1" -c "$2"
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
