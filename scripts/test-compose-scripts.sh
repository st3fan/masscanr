#!/bin/sh
# Exercise project selection without starting Docker or the application.
set -eu

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

cat > "$test_dir/docker" <<'MOCK'
#!/bin/sh
if [ "$1" = ps ]; then
    printf '%s\n' "${MOCK_DOCKER_PS:-}"
    exit 0
fi
if [ "${MOCK_DOCKER_FAIL_UP:-}" = 1 ]; then
    exit 1
fi
printf 'args=%s\n' "$*"
printf 'port=%s\n' "$MASSCANR_PORT"
printf 'bind=%s\n' "$MASSCANR_BIND_ADDRESS"
printf 'origin=%s\n' "${MASSCANR_BROWSER_ORIGIN:-}"
MOCK
chmod +x "$test_dir/docker"
cat > "$test_dir/gh" <<'MOCK'
#!/bin/sh
printf '%s\n' "${MOCK_PR_STATE:-OPEN}"
MOCK
chmod +x "$test_dir/gh"

run() {
    env -i PATH="$test_dir:$PATH" MASSCANR_POSTGRES_PASSWORD=test-password "$@"
}

assert_contains() {
    case "$1" in
        *"$2"*) ;;
        *) printf 'Expected output to contain: %s\n' "$2" >&2; exit 1 ;;
    esac
}

default_start=$(run ./scripts/start.sh)
assert_contains "$default_start" 'args=compose -p masscanr-local up --build'
assert_contains "$default_start" 'port=18080'
assert_contains "$default_start" 'bind=127.0.0.1'
assert_contains "$default_start" 'origin=http://127.0.0.1:18080'

custom_default=$(env -i PATH="$test_dir:$PATH" MASSCANR_POSTGRES_PASSWORD=test-password MASSCANR_PORT=19000 MASSCANR_BIND_ADDRESS=127.0.0.2 MASSCANR_LOCAL_PROJECT=custom ./scripts/start.sh)
assert_contains "$custom_default" 'args=compose -p custom up --build'
assert_contains "$custom_default" 'port=19000'
assert_contains "$custom_default" 'bind=127.0.0.2'
assert_contains "$custom_default" 'origin=http://127.0.0.2:19000'

intent_start=$(env -i PATH="$test_dir:$PATH" MASSCANR_POSTGRES_PASSWORD=test-password MASSCANR_PORT=19999 MASSCANR_BIND_ADDRESS=0.0.0.0 MASSCANR_LOCAL_PROJECT=other MASSCANR_BROWSER_ORIGIN=http://other ./scripts/start.sh 187)
assert_contains "$intent_start" 'args=compose -p masscanr-intent-187 up --build'
assert_contains "$intent_start" 'port=10187'
assert_contains "$intent_start" 'bind=127.0.0.1'
assert_contains "$intent_start" 'origin=http://127.0.0.1:10187'

detached_start=$(run ./scripts/start.sh --detach 187)
assert_contains "$detached_start" 'args=compose -p masscanr-intent-187 up --build --detach --wait --wait-timeout 180'
assert_contains "$detached_start" 'Ready at http://127.0.0.1:10187/'
if failed_start=$(env -i PATH="$test_dir:$PATH" MASSCANR_POSTGRES_PASSWORD=test-password MOCK_DOCKER_FAIL_UP=1 ./scripts/start.sh --detach 187 2>&1); then
    printf 'Failed detached start was reported as successful.\n' >&2
    exit 1
fi
case "$failed_start" in
    *'Ready at'*) printf 'Failed detached start printed a ready URL.\n' >&2; exit 1 ;;
esac

default_stop=$(run ./scripts/stop.sh)
assert_contains "$default_stop" 'args=compose -p masscanr-local down'
assert_contains "$default_stop" 'port=18080'

intent_stop=$(run ./scripts/stop.sh 187)
assert_contains "$intent_stop" 'args=compose -p masscanr-intent-187 down'
assert_contains "$intent_stop" 'port=10187'

intent_destroy=$(run ./scripts/destroy.sh 187)
assert_contains "$intent_destroy" 'args=compose -p masscanr-intent-187 down --volumes --remove-orphans --rmi local'
assert_contains "$intent_destroy" 'port=10187'

default_destroy=$(run ./scripts/destroy.sh)
assert_contains "$default_destroy" 'args=compose -p masscanr-local down --volumes --remove-orphans --rmi local'

if run ./scripts/destroy.sh 55536 >/dev/null 2>&1; then
    printf 'Out-of-range Intent was accepted.\n' >&2
    exit 1
fi
if run ./scripts/destroy.sh 0187 >/dev/null 2>&1; then
    printf 'Intent with leading zeroes was accepted.\n' >&2
    exit 1
fi

running_previews='masscanr-intent-187|Up 2 minutes (healthy)
masscanr-intent-188|Up 5 seconds (starting)
unrelated|Up 1 hour'
all_status=$(env -i PATH="$test_dir:$PATH" MOCK_DOCKER_PS="$running_previews" ./scripts/status.sh)
assert_contains "$all_status" 'Intent 187: http://127.0.0.1:10187/ - Up 2 minutes (healthy)'
assert_contains "$all_status" 'Intent 188: http://127.0.0.1:10188/ - Up 5 seconds (starting)'
case "$all_status" in
    *unrelated*) printf 'Unrelated project appeared in Intent status.\n' >&2; exit 1 ;;
esac
one_status=$(env -i PATH="$test_dir:$PATH" MOCK_DOCKER_PS="$running_previews" ./scripts/status.sh 187)
assert_contains "$one_status" 'Intent 187: http://127.0.0.1:10187/'
case "$one_status" in
    *'Intent 188:'*) printf 'Unrequested Intent appeared in status.\n' >&2; exit 1 ;;
esac
empty_status=$(run ./scripts/status.sh)
assert_contains "$empty_status" 'No Intent previews are running.'

merged_cleanup=$(env -i PATH="$test_dir:$PATH" MOCK_PR_STATE=MERGED ./scripts/cleanup-merged.sh 187 216)
assert_contains "$merged_cleanup" 'PR #216 is merged; stopping Intent 187 preview and retaining its volumes.'
assert_contains "$merged_cleanup" 'args=compose -p masscanr-intent-187 down'

if open_cleanup=$(env -i PATH="$test_dir:$PATH" MOCK_PR_STATE=OPEN ./scripts/cleanup-merged.sh 187 216 2>&1); then
    printf 'Open PR triggered preview cleanup.\n' >&2
    exit 1
fi
assert_contains "$open_cleanup" 'keeping Intent 187 preview running.'
case "$open_cleanup" in
    *'args=compose'*) printf 'Open PR invoked Docker cleanup.\n' >&2; exit 1 ;;
esac
if closed_cleanup=$(env -i PATH="$test_dir:$PATH" MOCK_PR_STATE=CLOSED ./scripts/cleanup-merged.sh 187 216 2>&1); then
    printf 'Unmerged closed PR triggered preview cleanup.\n' >&2
    exit 1
fi
case "$closed_cleanup" in
    *'args=compose'*) printf 'Unmerged closed PR invoked Docker cleanup.\n' >&2; exit 1 ;;
esac

printf 'Compose script target selection passed.\n'
