#!/bin/sh
# Build and attach to the isolated local Compose stack.
set -eu

usage() {
    printf 'Usage: scripts/start.sh [INTENT]\nStarts the local stack in the foreground on port 18080 by default. An Intent uses port 10000 + INTENT.\n'
}
case "${1:-}" in
    '') ;;
    --help|-h) usage; exit 0 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
. ./scripts/compose-target.sh
select_compose_target "${1:-}"
if [ -z "${MASSCANR_BROWSER_ORIGIN:-}" ]; then
    if [ "$MASSCANR_BIND_ADDRESS" = 0.0.0.0 ] || [ "$MASSCANR_BIND_ADDRESS" = :: ]; then
        printf 'Set MASSCANR_BROWSER_ORIGIN to the private browser-facing origin for a wildcard bind.\n' >&2
        exit 2
    fi
    MASSCANR_BROWSER_ORIGIN="http://$MASSCANR_BIND_ADDRESS:$MASSCANR_PORT"
fi
export MASSCANR_BROWSER_ORIGIN

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
