#!/bin/sh
# Stop an Intent preview only after its final PR is confirmed merged.
set -eu

usage() {
    printf 'Usage: scripts/cleanup-merged.sh INTENT PR\nChecks that PR is merged, then stops the Intent preview while keeping its volumes. Run this after the final PR for the Intent merges.\n'
}
case "${1:-}" in
    --help|-h) usage; exit 0 ;;
esac
if [ "$#" -ne 2 ]; then usage >&2; exit 2; fi

script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd "$script_dir/.." && pwd)
cd "$repo_root"
. ./scripts/compose-target.sh
select_compose_target "$1"
case "$2" in
    ''|0*|*[!0-9]*)
        printf 'PR must be a positive decimal number without leading zeroes.\n' >&2
        exit 2
        ;;
esac

pr_state=$(gh pr view "$2" --repo st3fan/masscanr --json state --jq '.state')
if [ "$pr_state" != MERGED ]; then
    printf 'PR #%s is %s; keeping Intent %s preview running.\n' "$2" "$pr_state" "$1" >&2
    exit 1
fi

printf 'PR #%s is merged; stopping Intent %s preview and retaining its volumes.\n' "$2" "$1"
exec "$script_dir/stop.sh" "$1"
