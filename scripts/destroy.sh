#!/bin/sh
# Explicit shorthand for project-scoped destructive cleanup.
set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
case "${1:-}" in
    --help|-h)
        printf 'Usage: scripts/destroy.sh [INTENT]\nRemoves the selected Compose project, its volumes, orphan containers, and locally built images.\n'
        exit 0
        ;;
esac
exec "$script_dir/stop.sh" --prune "$@"
