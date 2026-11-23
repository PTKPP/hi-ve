#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# The canonical Platform registry has no production dependency outside the standard library.
# Private implementation packages belong under their owner's nested internal/.
# Go's loader enforces that visibility and rejects import cycles.
dependencies=$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/platform)
unexpected=$(printf '%s\n' "$dependencies" | sed '/^$/d; /^hi-ve\/backend\/internal\/platform$/d')
if [[ -n "$unexpected" ]]; then
    printf 'Unexpected Platform registry dependencies:\n%s\n' "$unexpected" >&2
    exit 1
fi
printf 'PASS: Platform registry depends only on the Go standard library\n'

# Check actual cross-package edges. New consumers must use the owning package's
# public contract. Go enforces visibility for any nested internal package.
go list -f '{{ $p := .ImportPath }}{{range .Imports}}{{printf "%s %s\n" $p .}}{{end}}' ./... |
awk '
    $2 !~ /^hi-ve\/backend\// { next }
    $1 == "hi-ve/backend/cmd/audit-retention" && $2 == "hi-ve/backend/internal/audit" { next }
    # Phase 08 owns activation of Offline in the standard binary.
    $1 == "hi-ve/backend/cmd/api" &&
        $2 ~ /^hi-ve\/backend\/internal\/(audit|catalog|devices|identity|locations|settings|platform\/(entitlements|provisioning)|transport)$/ { next }
    $1 == "hi-ve/backend/internal/transport" &&
        $2 ~ /^hi-ve\/backend\/internal\/(audit|catalog|devices|identity|inventory|locations|offline|sales|settings|platform\/(entitlements|provisioning))$/ { next }
    $1 == "hi-ve/backend/internal/platform/provisioning" &&
        $2 ~ /^hi-ve\/backend\/internal\/(identity|audit)$/ { next }
    $1 == "hi-ve/backend/internal/platform/entitlements" &&
        $2 ~ /^hi-ve\/backend\/internal\/(identity|audit|platform)$/ { next }
    # Offline calls public domain contracts. Inventory and Locations provide
    # typed denials, while Sales owns the actual posting path.
    $1 == "hi-ve/backend/internal/offline" &&
        $2 ~ /^hi-ve\/backend\/internal\/(audit|catalog|devices|identity|inventory|locations|platform\/entitlements|sales)$/ { next }
    $1 == "hi-ve/backend/internal/sales" && $2 ~ /^hi-ve\/backend\/internal\/(catalog|inventory|locations|settings)$/ { next }
    $1 == "hi-ve/backend/internal/inventory" && $2 ~ /^hi-ve\/backend\/internal\/(catalog|locations)$/ { next }
    $1 == "hi-ve/backend/internal/devices" && $2 == "hi-ve/backend/internal/identity" { next }
    $1 ~ /^hi-ve\/backend\/internal\/(devices|locations|settings)$/ && $2 == "hi-ve/backend/internal/audit" { next }
    $1 == "hi-ve/backend/internal/identity" && $2 == "hi-ve/backend/internal/audit" { next }
    { print "Unexpected module dependency:", $1, "->", $2 > "/dev/stderr"; failed=1 }
    END { exit failed }
'
