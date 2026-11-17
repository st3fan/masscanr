#!/bin/sh
# Verify native Fedora scanner behavior without network or capabilities at runtime.
set -eu

cd "$(dirname "$0")/.."
workspace=$(mktemp -d /tmp/masscanr-fedora.XXXXXX)
image="masscanr-fedora-check:${workspace##*/}"
cleanup() {
    docker image rm "$image" >/dev/null 2>&1 || true
    rm -f "$workspace/native.test" "$workspace/Dockerfile"
    rmdir "$workspace"
}
trap cleanup EXIT

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o "$workspace/native.test" ./internal/integration
cp test/fedora/Dockerfile "$workspace/Dockerfile"
docker build "$@" --platform linux/amd64 --tag "$image" "$workspace"
docker run --rm --pull never --platform linux/amd64 --network none --cap-drop ALL "$image"
