#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker_local() { env -u DOCKER_HOST -u DOCKER_CONTEXT docker --context default "$@"; }
[[ $(docker_local context inspect default --format '{{(index .Endpoints "docker").Host}}') == unix://* ]]
scratch=$(mktemp -d)
container=
cleanup() {
    if [[ -n "$container" ]]; then docker_local stop "$container" >/dev/null; fi
    rm -rf -- "$scratch"
}
trap cleanup EXIT

# All keys, credentials, WAL and recovered rows are disposable test fixtures.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=staging-db-test \
    -addext subjectAltName=IP:127.0.0.1 -keyout "$scratch/server.key" -out "$scratch/ca.pem" >/dev/null 2>&1
container=$(docker_local run --rm -d -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql \
    -e POSTGRES_PASSWORD=fixture_admin_password \
    postgres:18@sha256:e754b839f447639b7004895f4d8a4bfcb4e9dbe3a1bc035f27f646562a16fd61 \
    -c archive_mode=on -c 'archive_command=cp "%p" "/var/lib/postgresql/wal/%f"')
port=$(docker_local port "$container" 5432/tcp)
for ((attempt=0; attempt<40; attempt++)); do
    if docker_local exec "$container" pg_isready -h 127.0.0.1 -U postgres >/dev/null; then break; fi
    sleep 1
done
sql() { docker_local exec -u postgres -i "$container" psql -XAt -v ON_ERROR_STOP=1 -U postgres "$@"; }

docker_local cp "$scratch/server.key" "$container:/tmp/hive-test.key"
docker_local cp "$scratch/ca.pem" "$container:/tmp/hive-test.pem"
docker_local exec "$container" install -d -o postgres -m 0700 /var/lib/postgresql/wal /var/lib/postgresql/tls
docker_local exec "$container" install -o postgres -m 0600 /tmp/hive-test.key /var/lib/postgresql/tls/server.key
docker_local exec "$container" install -o postgres -m 0644 /tmp/hive-test.pem /var/lib/postgresql/tls/server.crt
sql -c "ALTER SYSTEM SET ssl_cert_file='/var/lib/postgresql/tls/server.crt'" \
    -c "ALTER SYSTEM SET ssl_key_file='/var/lib/postgresql/tls/server.key'" -c "ALTER SYSTEM SET ssl='on'" -c 'SELECT pg_reload_conf()'
sql <deploy/staging/postgres/bootstrap.sql
sql -c "ALTER ROLE hive_migrator PASSWORD 'fixture_migrator_password'" \
    -c "ALTER ROLE hive_app PASSWORD 'fixture_app_password'" -c "ALTER ROLE hive_backup PASSWORD 'fixture_backup_password'"
sed 's/192\.0\.2\.[13]0\/32/0.0.0.0\/0/g; s/192\.0\.2\.40\/32/127.0.0.1\/32/g' deploy/staging/postgres/pg_hba.conf.example |
    docker_local exec -u postgres -i "$container" sh -c 'cat > "$PGDATA/pg_hba.conf"'
sql -c 'SELECT pg_reload_conf()'
for ((attempt=0; attempt<20; attempt++)); do
    if [[ $(sql -c "SELECT setting FROM pg_settings WHERE name='ssl'") == on ]]; then break; fi
    sleep 1
done
if docker_local exec -e PGPASSWORD=fixture_app_password "$container" psql -XAt -v ON_ERROR_STOP=1 \
    'host=127.0.0.1 dbname=hive_staging user=hive_app sslmode=disable' -c 'SELECT 1' >"$scratch/plaintext.log" 2>&1; then
    printf 'Expected pg_hba to reject a plaintext runtime connection\n' >&2
    exit 1
fi
[[ $(<"$scratch/plaintext.log") == *"pg_hba.conf rejects connection"* ]]

# Run migrations under the owning role through a verified TLS connection.
migrate_dsn="postgresql://hive_migrator:fixture_migrator_password@$port/hive_staging?sslmode=verify-full&sslrootcert=$scratch/ca.pem&connect_timeout=5"
env -i PATH="$PATH" tern migrate --config migrations/tern.conf --conn-string "$migrate_dsn" --migrations migrations
{ printf 'SET ROLE hive_migrator;\n'; cat deploy/staging/postgres/runtime-grants.sql; } | sql hive_staging
sql hive_staging <<'SQL'
CREATE TABLE public.staging_disposable_marker (id integer);
GRANT SELECT ON public.staging_disposable_marker TO hive_app;
SQL
HIVE_STAGING_TEST_URL="postgresql://hive_app:fixture_app_password@$port/hive_staging?sslmode=verify-full&sslrootcert=$scratch/ca.pem" \
    go test -race -count=1 ./cmd/api -run '^TestStagingDisposableDatabase$'
sql hive_staging <scripts/fixtures/staging-role-smoke.sql

# Take a physical backup as the restricted replication role, then replay only
# through a named restore point in an isolated data directory and port.
docker_local exec -u postgres -e PGPASSWORD=fixture_backup_password "$container" \
    pg_basebackup -d 'host=127.0.0.1 user=hive_backup sslmode=verify-full sslrootcert=/var/lib/postgresql/tls/server.crt' \
    -D /var/lib/postgresql/pitr -X stream --checkpoint=fast
sql hive_staging -c 'INSERT INTO public.staging_disposable_marker VALUES (1)'
segment=$(sql -c "SELECT pg_walfile_name(pg_create_restore_point('hive_staging_rehearsal'))")
sql hive_staging -c 'INSERT INTO public.staging_disposable_marker VALUES (2)'
sql -c 'SELECT pg_switch_wal()'
for ((attempt=0; attempt<40; attempt++)); do
    if docker_local exec "$container" test -s "/var/lib/postgresql/wal/$segment"; then break; fi
    sleep 1
done
docker_local exec "$container" test -s "/var/lib/postgresql/wal/$segment"
docker_local exec -u postgres -i "$container" sh -c '
    cat >> /var/lib/postgresql/pitr/postgresql.auto.conf
    touch /var/lib/postgresql/pitr/recovery.signal
    pg_ctl -D /var/lib/postgresql/pitr -o "-p 5544 -c listen_addresses= -c archive_mode=off" -w start
' <<'CONF'
restore_command = 'cp /var/lib/postgresql/wal/%f %p'
recovery_target_name = 'hive_staging_rehearsal'
recovery_target_action = 'promote'
CONF
for ((attempt=0; attempt<30; attempt++)); do
    if [[ $(sql -p 5544 hive_staging -c 'SELECT NOT pg_is_in_recovery()' 2>/dev/null) == t ]]; then break; fi
    sleep 1
done
[[ $(sql -p 5544 hive_staging -c 'SELECT NOT pg_is_in_recovery()') == t ]]
[[ $(sql -p 5544 hive_staging -c 'SELECT id FROM public.staging_disposable_marker ORDER BY id') == 1 ]]
[[ $(sql -p 5544 hive_staging -c 'SELECT version FROM public.hive_schema_version') == 55 ]]
printf 'PASS: PostgreSQL staging roles, verified TLS, and isolated WAL point-in-time recovery\n'
