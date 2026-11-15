#!/bin/sh
# Explicit shorthand for project-scoped destructive cleanup.
set -eu

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
case "${1:-}" in
    --help|-h) exec "$script_dir/stop.sh" --help ;;
esac
exec "$script_dir/stop.sh" --prune "$@"
