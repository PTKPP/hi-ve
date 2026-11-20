#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# A fresh local container is the only permitted target. No caller DSN, Docker
# endpoint, Compose project, volume, port or database name is accepted.
if [[ $# -ne 0 ]]; then
    printf 'Rehearsal does not accept a database target.\n' >&2
    exit 2
fi
docker_local() { env -u DOCKER_CONTEXT -u DOCKER_HOST docker --context default "$@"; }
[[ $(docker_local context inspect default --format '{{(index .Endpoints "docker").Host}}') == unix://* ]]
scratch=$(mktemp -d)
container=
cleanup() {
    if [[ -n "$container" ]]; then docker_local stop "$container" >/dev/null || true; fi
    rm -rf -- "$scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

rollback_sha=3e421e11c18f5207d4c68d2345731a349518df54
legacy_sha=19f643882a0e66493fd11052572766acf80b3e13
candidate_sha=$(git rev-parse HEAD)
build_release() {
    local name=$1 revision=$2
    mkdir "$scratch/$name"
    git -C .. archive "$revision" backend | tar -x -C "$scratch/$name"
    (cd "$scratch/$name/backend" && go build -trimpath -o "$scratch/$name-api" ./cmd/api)
    printf '%s SHA: %s\n' "$name" "$revision"
    sha256sum "$scratch/$name-api"
}
build_release rollback "$rollback_sha"
build_release legacy "$legacy_sha"
go build -trimpath -o "$scratch/candidate-api" ./cmd/api
printf 'candidate source HEAD: %s\n' "$candidate_sha"
sha256sum "$scratch/candidate-api"
if ! git diff --quiet HEAD -- cmd internal go.mod go.sum ':!**/*_test.go'; then
    printf 'Candidate build includes local runtime source changes.\n'
fi

container=$(docker_local run --rm -d -p 127.0.0.1::5432 --tmpfs /var/lib/postgresql \
    -e POSTGRES_PASSWORD=hive_disposable_test -e POSTGRES_DB=hive_test \
    postgres:18@sha256:e754b839f447639b7004895f4d8a4bfcb4e9dbe3a1bc035f27f646562a16fd61)
for ((i=0;i<60;i++)); do
    if docker_local exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test >/dev/null; then break; fi
    sleep 1
done
docker_local exec "$container" pg_isready -h 127.0.0.1 -U postgres -d hive_test
port=$(docker_local port "$container" 5432/tcp)
[[ "$port" =~ ^127\.0\.0\.1:[0-9]+$ ]]
dsn="postgres://postgres:hive_disposable_test@$port/hive_test?sslmode=disable&connect_timeout=5&statement_timeout=15000"
migrations=(migrations/*.sql)
[[ ${#migrations[@]} -eq 29 ]]
env -i PATH="$PATH" tern migrate --config migrations/tern.conf --conn-string "$dsn" --migrations migrations --destination 28 >"$scratch/baseline.log" 2>&1 || {
    tail -30 "$scratch/baseline.log"
    exit 1
}

docker_local exec -i "$container" psql -Xq -v ON_ERROR_STOP=1 -U postgres -d hive_test \
    < scripts/fixtures/m5/runtime-role.sql

HIVE_DISPOSABLE_TEST_DATABASE="$dsn" HIVE_M5_BINARY_DIR="$scratch" \
    go test -race -v -count=1 ./internal/transport -run '^TestM5ContractRehearsal$' || {
    docker_local logs --tail 50 "$container" >&2
    exit 1
}
