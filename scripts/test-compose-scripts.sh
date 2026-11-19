#!/bin/sh
# Exercise project selection without starting Docker or the application.
set -eu

repo_root=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
cd "$repo_root"
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

cat > "$test_dir/docker" <<'MOCK'
#!/bin/sh
printf 'args=%s\n' "$*"
printf 'port=%s\n' "$MASSCANR_PORT"
printf 'bind=%s\n' "$MASSCANR_BIND_ADDRESS"
printf 'origin=%s\n' "${MASSCANR_BROWSER_ORIGIN:-}"
MOCK
chmod +x "$test_dir/docker"

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

printf 'Compose script target selection passed.\n'
