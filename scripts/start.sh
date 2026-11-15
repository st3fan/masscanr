#!/bin/sh
# Build and attach to the isolated local Compose stack.
set -eu

usage() {
    printf 'Usage: scripts/start.sh\nStarts the local stack in the foreground on port 18080 by default.\n'
}
case "${1:-}" in
    '') ;;
    --help|-h) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
project=${MASSCANR_LOCAL_PROJECT:-masscanr-local}
MASSCANR_PORT=${MASSCANR_PORT:-18080}
MASSCANR_BIND_ADDRESS=${MASSCANR_BIND_ADDRESS:-127.0.0.1}
export MASSCANR_PORT MASSCANR_BIND_ADDRESS

if [ ! -e .env ] && [ -z "${MASSCANR_POSTGRES_PASSWORD:-}" ]; then
    command -v openssl >/dev/null 2>&1 || { printf 'openssl is required to create .env\n' >&2; exit 1; }
    password=$(openssl rand -hex 24)
    (umask 077; set -C; printf 'MASSCANR_POSTGRES_PASSWORD=%s\n' "$password" > .env) || {
        printf 'Could not create .env; check the existing file before retrying.\n' >&2
        exit 1
    }
    printf 'Created a private .env with a local database password.\n'
fi

printf 'Starting Compose project %s at http://%s:%s/ (foreground).\n' "$project" "$MASSCANR_BIND_ADDRESS" "$MASSCANR_PORT"
exec docker compose -p "$project" up --build
