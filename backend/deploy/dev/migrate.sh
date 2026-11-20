#!/usr/bin/env bash
set -euo pipefail

dev_dir=$(cd "$(dirname "$0")" && pwd)
backend_dir=$(cd "$dev_dir/../.." && pwd)
action=${1:-status}
if [[ $# -gt 1 || ( "$action" != status && "$action" != migrate ) ]]; then
    printf 'Penggunaan: %s [status|migrate]\n' "$0" >&2
    exit 2
fi
# The persistent Compose path is approved only through migration 028.
# Give Tern a sealed copy of that prefix so it cannot see Contract 029.
development_schema_limit=28
development_migrations=$(mktemp -d)
trap 'rm -rf -- "$development_migrations"' EXIT
for ((version=1;version<=development_schema_limit;version++)); do
    printf -v prefix '%03d' "$version"
    files=("$backend_dir/migrations/${prefix}_"*.sql)
    if [[ ${#files[@]} -ne 1 || ! -f "${files[0]}" ]]; then
        printf 'Preflight gagal: migrasi development %s harus tersedia tepat satu kali.\n' "$prefix" >&2
        exit 1
    fi
    cp -- "${files[0]}" "$development_migrations/"
done
printf 'Migrator Compose development dibatasi hingga schema %s.\n' "$development_schema_limit" >&2
docker_local() {
    env -u DOCKER_HOST -u DOCKER_CONTEXT docker --context default "$@"
}
compose() {
    docker_local compose --project-name hi-ve-dev --project-directory "$dev_dir" --file "$dev_dir/compose.yaml" "$@"
}
if [[ $(docker_local context inspect default --format '{{(index .Endpoints "docker").Host}}') != unix://* ]]; then
    printf 'Migrasi development memerlukan socket Docker lokal.\n' >&2
    exit 1
fi
if [[ $(compose port db 5432) != 127.0.0.1:55432 ]]; then
    printf 'Binding PostgreSQL Compose development tidak sesuai.\n' >&2
    exit 1
fi
compose exec -T db pg_isready -q -h 127.0.0.1 -U hive_dev -d hive_dev
password=$(compose exec -T db printenv POSTGRES_PASSWORD)
if [[ ! "$password" =~ ^[a-fA-F0-9]{64}$ ]]; then
    printf 'Isi POSTGRES_PASSWORD development dengan 64 digit hex unik.\n' >&2
    exit 1
fi
# Use a fresh environment so service files, DSNs, and PG* values cannot override the target.
# Pass the password in the child environment without adding it to argv or printing it.
env -i PATH="$PATH" bash -c 'IFS= read -r PGPASSWORD; export PGPASSWORD; exec tern "$@"' hive-tern "$action" \
    --config "$backend_dir/migrations/tern.conf" --migrations "$development_migrations" \
    --conn-string "host=127.0.0.1 port=55432 user=hive_dev dbname=hive_dev sslmode=disable connect_timeout=5 lock_timeout=5000 statement_timeout=30000" <<<"$password"
