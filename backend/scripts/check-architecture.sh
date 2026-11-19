#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

# The Platform registry is the only production domain package today.
# Private implementation packages belong under their owner's nested internal/.
# Go's loader enforces that visibility and rejects import cycles.
dependencies=$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./internal/platform)
unexpected=$(printf '%s\n' "$dependencies" | sed '/^$/d; /^hi-ve\/backend\/internal\/platform$/d')
if [[ -n "$unexpected" ]]; then
    printf 'Unexpected Platform registry dependencies:\n%s\n' "$unexpected" >&2
    exit 1
fi
printf 'PASS: Platform registry depends only on the Go standard library\n'
