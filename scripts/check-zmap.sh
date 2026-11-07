#!/bin/sh
# Run native compatibility checks with no scanner network access or cloud VMs.
set -eu

cd "$(dirname "$0")/.."
workspace=$(mktemp -d /tmp/masscanr-zmap.XXXXXX)
image="masscanr-zmap-check:${workspace##*/}"
cleanup() {
    docker image rm "$image" >/dev/null 2>&1 || true
    rm -f "$workspace/native.test" "$workspace/Dockerfile"
    rmdir "$workspace"
}
trap cleanup EXIT

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$workspace/native.test" ./internal/integration
cp test/zmap/Dockerfile "$workspace/Dockerfile"
# Optional arguments affect package downloads at build time only. Runtime
# scanner checks always have networking and capabilities disabled below.
docker build "$@" --platform linux/amd64 --tag "$image" "$workspace"
docker run --rm --pull never --platform linux/amd64 --network none --cap-drop ALL "$image"
