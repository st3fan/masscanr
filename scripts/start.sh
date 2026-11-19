#!/bin/sh
# Build and start the isolated local Compose stack.
set -eu

usage() {
    printf 'Usage: scripts/start.sh [--detach] [INTENT]\nStarts the local stack in the foreground by default. --detach waits for healthy services and leaves them running. An Intent uses port 10000 + INTENT; the local default uses port 18080.\n'
}
detach=false
intent=
for arg do
    case "$arg" in
        --detach|-d)
            if [ "$detach" = true ]; then usage >&2; exit 2; fi
            detach=true
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

if [ "$detach" = true ]; then
    printf 'Starting Compose project %s at http://%s:%s/ (detached).\n' "$project" "$MASSCANR_BIND_ADDRESS" "$MASSCANR_PORT"
    docker compose -p "$project" up --build --detach --wait --wait-timeout 180
    printf 'Ready at http://%s:%s/\n' "$MASSCANR_BIND_ADDRESS" "$MASSCANR_PORT"
else
    printf 'Starting Compose project %s at http://%s:%s/ (foreground).\n' "$project" "$MASSCANR_BIND_ADDRESS" "$MASSCANR_PORT"
    exec docker compose -p "$project" up --build
fi
