#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
export HIVE_API_IMAGE="registry.example.invalid/hi-ve/api@sha256:$(printf '%064d' 0)"
export HIVE_STAGING_ENV_FILE="$PWD/backend/deploy/staging/backend.env.example"
export HIVE_STAGING_PG_CA_FILE=/fixture/hive/pg-ca.pem
docker compose --env-file /dev/null -f backend/deploy/staging/compose.yaml config --format json |
    jq -e '(.services | keys == ["api"]) and (.services.api |
        (.image | endswith("@sha256:" + ("0"*64))) and .user == "65532:65532" and .read_only and
        (.ports | length == 1 and .[0].host_ip == "127.0.0.1" and .[0].target == 8080) and
        (.volumes | length == 1 and .[0].read_only) and .cap_drop == ["ALL"] and
        .environment.HIVE_RUNTIME == "staging" and .healthcheck.test == ["CMD", "/hive-api", "healthcheck"])' >/dev/null
for app in hi-ve-app superadmin; do
    diff -u - "apps/$app/wrangler.staging.example.toml" <<TOML
name = "$app-staging"
compatibility_date = "2026-11-23"
workers_dev = false
preview_urls = false

[assets]
directory = "./dist"
not_found_handling = "404-page"
TOML
done
printf 'PASS: staging Compose is loopback-only and Cloudflare Workers templates use static assets\n'
