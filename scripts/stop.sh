#!/bin/sh
# Stop only the isolated local Compose project; --prune removes its storage.
set -eu

usage() {
    printf 'Usage: scripts/stop.sh [--prune] [INTENT]\nWithout --prune, keep the selected project volumes. --prune removes its containers, volumes, orphan containers, and locally built images.\n'
}
prune=false
intent=
for arg do
    case "$arg" in
        --prune)
            if [ "$prune" = true ]; then usage >&2; exit 2; fi
            prune=true
            ;;
        --help|-h)
            if [ "$#" -ne 1 ]; then usage >&2; exit 2; fi
            usage
            exit 0
            ;;
        *)
            if [ -n "$intent" ]; then usage >&2; exit 2; fi
            intent=$arg
            ;;
    esac
done

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
. ./scripts/compose-target.sh
select_compose_target "$intent"
# Compose still interpolates the service environment when running `down`.
MASSCANR_POSTGRES_PASSWORD=${MASSCANR_POSTGRES_PASSWORD:-unused-for-stop}
export MASSCANR_POSTGRES_PASSWORD

if [ "$prune" = true ]; then
    printf 'Destroying Compose project %s and its project-scoped volumes and images.\n' "$project"
    exec docker compose -p "$project" down --volumes --remove-orphans --rmi local
fi
printf 'Stopping Compose project %s; retaining its volumes.\n' "$project"
exec docker compose -p "$project" down
