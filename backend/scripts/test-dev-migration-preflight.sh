#!/usr/bin/env bash
set -euo pipefail
backend_dir=$(cd "$(dirname "$0")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf -- "$scratch"' EXIT
mkdir -p "$scratch/backend/deploy/dev" "$scratch/backend/migrations" "$scratch/bin"
cp "$backend_dir/deploy/dev/migrate.sh" "$scratch/backend/deploy/dev/"
cp "$backend_dir/migrations/"*.sql "$scratch/backend/migrations/"
cp "$backend_dir/migrations/tern.conf" "$scratch/backend/migrations/"

# Simulate the Compose metadata and inspect Tern's actual arguments and migration
# directory, without connecting to any Docker daemon or PostgreSQL.
cat > "$scratch/bin/docker" <<'MOCK'
#!/usr/bin/env bash
set -eu
case "$*" in
    *"context inspect default"*) printf 'unix:///fixture.sock\n' ;;
    *"port db 5432") printf '127.0.0.1:55432\n' ;;
    *"exec -T db pg_isready"*) ;;
    *"printenv POSTGRES_PASSWORD") printf '%064d\n' 0 ;;
    *) exit 3 ;;
esac
MOCK
cat > "$scratch/bin/tern" <<'MOCK'
#!/usr/bin/env bash
set -eu
while [[ $# -gt 0 && "$1" != --migrations ]]; do shift; done
[[ $# -ge 2 ]]
files=("$2/"*.sql)
[[ ${#files[@]} -eq 28 && -f "$2/028_device_challenge_bootstrap_replay.sql" ]]
[[ ! -e "$2/029_m5_contract.sql" ]]
printf 'verified development migration input: 028\n'
MOCK
chmod +x "$scratch/bin/docker" "$scratch/bin/tern"
entry="$scratch/backend/deploy/dev/migrate.sh"
for action in migrate status; do
    [[ $(PATH="$scratch/bin:$PATH" bash "$entry" "$action") == 'verified development migration input: 028' ]]
done

mv "$scratch/backend/migrations/028_device_challenge_bootstrap_replay.sql" "$scratch/"
if PATH="$scratch/bin:$PATH" bash "$entry" migrate >"$scratch/failed.log" 2>&1; then
    printf 'Preflight accepted an incomplete migration prefix.\n' >&2
    exit 1
fi
grep -q 'Preflight gagal: migrasi development 028' "$scratch/failed.log"
printf 'PASS: Compose development excludes 029 and rejects an incomplete prefix\n'
