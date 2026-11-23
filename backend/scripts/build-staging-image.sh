#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."
output=${1:?Usage: build-staging-image.sh /absolute/output-directory [commit]}
[[ $output == /* ]]
mkdir -p "$output"
revision=$(git rev-parse --verify "${2:-HEAD}^{commit}")
epoch=$(git show -s --format=%ct "$revision")
context=$(mktemp -d)
trap 'rm -rf -- "$context"' EXIT

# The Docker context and the revision label come from the same Git object.
git archive "$revision" backend | tar -xf - -C "$context"
docker buildx build --platform linux/amd64 \
    --build-arg "VCS_REF=$revision" --build-arg "SOURCE_DATE_EPOCH=$epoch" \
    --tag "hi-ve-api:$revision" --provenance=mode=min \
    --metadata-file "$output/build-metadata.json" \
    --output "type=oci,dest=$output/hive-api.oci.tar,rewrite-timestamp=true" "$context/backend"

index=$(jq -er '."containerimage.digest"' "$output/build-metadata.json")
blob() { tar -xOf "$output/hive-api.oci.tar" "blobs/sha256/${1#sha256:}"; }
manifest=$(blob "$index" | jq -er '.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64") | .digest')
config=$(blob "$manifest" | jq -er .config.digest)
blob "$config" |
    jq -e --arg rev "$revision" '.config.User == "65532:65532" and .config.Labels["org.opencontainers.image.revision"] == $rev' >/dev/null
jq -n --arg commit "$revision" --arg manifest "$manifest" --arg config "$config" \
    --arg index "$index" \
    '{commit:$commit,platform:"linux/amd64",index_digest:$index,manifest_digest:$manifest,config_digest:$config}' >"$output/release.json"
cat "$output/release.json"
