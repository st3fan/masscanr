# Shared project and port selection for the local Compose scripts.
select_compose_target() {
    intent=$1
    if [ -n "$intent" ]; then
        case "$intent" in
            0*|*[!0-9]*)
                printf 'Intent number must be a positive decimal integer without leading zeroes.\n' >&2
                return 2
                ;;
        esac
        if [ "${#intent}" -gt 5 ] || [ "$intent" -gt 55535 ]; then
            printf 'Intent number must be at most 55535 so the host port is valid.\n' >&2
            return 2
        fi
        project="masscanr-intent-$intent"
        MASSCANR_PORT=$((10000 + intent))
        MASSCANR_BIND_ADDRESS=127.0.0.1
        MASSCANR_BROWSER_ORIGIN="http://127.0.0.1:$MASSCANR_PORT"
        export MASSCANR_BROWSER_ORIGIN
    else
        project=${MASSCANR_LOCAL_PROJECT:-masscanr-local}
        MASSCANR_PORT=${MASSCANR_PORT:-18080}
        MASSCANR_BIND_ADDRESS=${MASSCANR_BIND_ADDRESS:-127.0.0.1}
    fi
    export MASSCANR_PORT MASSCANR_BIND_ADDRESS
}
