#!/bin/sh
# Stop only the isolated local Compose project; --prune removes its storage.
set -eu

usage() {
    printf 'Usage: scripts/stop.sh [--prune]\nWithout --prune, keep the local project volumes. --prune removes its containers, volumes, orphan containers, and locally built images.\n'
}
prune=false
case "${1:-}" in
    '') ;;
    --prune) prune=true ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
project=${MASSCANR_LOCAL_PROJECT:-masscanr-local}
MASSCANR_PORT=${MASSCANR_PORT:-18080}
MASSCANR_BIND_ADDRESS=${MASSCANR_BIND_ADDRESS:-127.0.0.1}
# Compose still interpolates the service environment when running `down`.
MASSCANR_POSTGRES_PASSWORD=${MASSCANR_POSTGRES_PASSWORD:-unused-for-stop}
export MASSCANR_PORT MASSCANR_BIND_ADDRESS MASSCANR_POSTGRES_PASSWORD

if [ "$prune" = true ]; then
    printf 'Destroying Compose project %s and its project-scoped volumes and images.\n' "$project"
    exec docker compose -p "$project" down --volumes --remove-orphans --rmi local
fi
printf 'Stopping Compose project %s; retaining its volumes.\n' "$project"
exec docker compose -p "$project" down
