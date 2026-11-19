#!/bin/sh
# List running Intent previews and their predictable localhost URLs.
set -eu

usage() {
    printf 'Usage: scripts/status.sh [INTENT]\nLists running Intent previews, or shows one Intent when its number is given.\n'
}
case "${1:-}" in
    --help|-h) usage; exit 0 ;;
esac
if [ "$#" -gt 1 ]; then usage >&2; exit 2; fi

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
. ./scripts/compose-target.sh
requested_intent=${1:-}
if [ -n "$requested_intent" ]; then
    select_compose_target "$requested_intent"
fi

rows=$(docker ps --filter label=com.docker.compose.service=masscanr --format '{{.Label "com.docker.compose.project"}}|{{.Status}}')
found=false
while IFS='|' read -r running_project running_status; do
    case "$running_project" in
        masscanr-intent-*) ;;
        *) continue ;;
    esac
    running_intent=${running_project#masscanr-intent-}
    case "$running_intent" in
        ''|0*|*[!0-9]*) continue ;;
    esac
    if [ "${#running_intent}" -gt 5 ] || [ "$running_intent" -gt 55535 ]; then
        continue
    fi
    if [ -n "$requested_intent" ] && [ "$running_intent" != "$requested_intent" ]; then
        continue
    fi
    found=true
    printf 'Intent %s: http://127.0.0.1:%s/ - %s\n' "$running_intent" "$((10000 + running_intent))" "$running_status"
done <<EOF
$rows
EOF

if [ "$found" = false ]; then
    if [ -n "$requested_intent" ]; then
        printf 'Intent %s has no running preview.\n' "$requested_intent"
    else
        printf 'No Intent previews are running.\n'
    fi
fi
