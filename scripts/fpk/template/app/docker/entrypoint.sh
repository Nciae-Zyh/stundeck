#!/bin/sh
set -eu

umask 077

fail() {
    printf 'StunDeck FPK startup: %s\n' "$1" >&2
    exit 1
}

config_dir=${STUNDECK_FPK_CONFIG_DIR:-/etc/stundeck}
data_dir=${STUNDECK_DATA_DIR:-/var/lib/stundeck/data}
port_file="${config_dir}/stundeck.port"
env_file="${config_dir}/stundeck.env"

case "$config_dir" in
    /*) ;;
    *) fail "the configuration directory must be absolute" ;;
esac
case "$data_dir" in
    /*) ;;
    *) fail "the data directory must be absolute" ;;
esac

validate_port() {
    case "$1" in
        ''|*[!0-9]*) fail "the control-panel port must be numeric" ;;
    esac
    [ "$1" -ge 1024 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null \
        || fail "the control-panel port must be between 1024 and 65535"
}

port_in_use() {
    candidate_hex=$(printf '%04X' "$1")
    awk -v target="$candidate_hex" '
        NR > 1 {
            split($2, address, ":")
            if (toupper(address[2]) == target && $4 == "0A") {
                found = 1
            }
        }
        END { exit(found ? 0 : 1) }
    ' /proc/net/tcp /proc/net/tcp6 2>/dev/null
}

random_number() {
    value=''
    if [ -r /dev/urandom ]; then
        value=$(od -An -N4 -tu4 /dev/urandom 2>/dev/null | tr -d '[:space:]')
    fi
    case "$value" in
        ''|*[!0-9]*) value=$$ ;;
    esac
    printf '%s\n' "$value"
}

choose_port() {
    attempt=1
    while [ "$attempt" -le 128 ]; do
        value=$(random_number)
        candidate=$((20000 + value % 40000))
        if ! port_in_use "$candidate"; then
            printf '%s\n' "$candidate"
            return 0
        fi
        attempt=$((attempt + 1))
    done
    fail "no free random high port was found after 128 attempts"
}

mkdir -p "$config_dir" "$data_dir"
chmod 0700 "$config_dir" "$data_dir" 2>/dev/null || true

if [ -s "$port_file" ]; then
    IFS= read -r port < "$port_file" || fail "the persisted port could not be read"
    validate_port "$port"
    port_in_use "$port" && fail "the persisted control-panel port ${port} is occupied"
else
    requested_port=${STUNDECK_FPK_REQUESTED_PORT:-}
    if [ -n "$requested_port" ]; then
        validate_port "$requested_port"
        port_in_use "$requested_port" && fail "the requested control-panel port ${requested_port} is occupied"
        port=$requested_port
    else
        port=$(choose_port)
    fi

    port_tmp="${config_dir}/.stundeck.port.$$"
    trap 'rm -f -- "${port_tmp:-}" "${env_tmp:-}"' EXIT HUP INT TERM
    printf '%s\n' "$port" > "$port_tmp"
    chmod 0644 "$port_tmp"
    mv -f -- "$port_tmp" "$port_file"
fi

persisted_secure_cookies=''
persisted_timezone=''
if [ -s "$env_file" ]; then
    persisted_secure_cookies=$(sed -n 's/^STUNDECK_SECURE_COOKIES=\(.*\)$/\1/p' "$env_file" | head -n 1)
    persisted_timezone=$(sed -n 's/^TZ=\(.*\)$/\1/p' "$env_file" | head -n 1)
fi

secure_cookie_input=${persisted_secure_cookies:-${STUNDECK_SECURE_COOKIES:-false}}
case "$secure_cookie_input" in
    true|TRUE|True|1|yes|YES|on|ON) secure_cookies=true ;;
    false|FALSE|False|0|no|NO|off|OFF|'') secure_cookies=false ;;
    *) fail "the secure-cookie setting is invalid" ;;
esac

timezone=${persisted_timezone:-${TZ:-Asia/Shanghai}}
printf '%s' "$timezone" | grep -Eq '^[A-Za-z0-9_+./-]{1,64}$' \
    || fail "the timezone is invalid"

env_tmp="${config_dir}/.stundeck.env.$$"
trap 'rm -f -- "${port_tmp:-}" "${env_tmp:-}"' EXIT HUP INT TERM
{
    printf 'STUNDECK_FPK_PORT=%s\n' "$port"
    printf 'STUNDECK_LISTEN=0.0.0.0:%s\n' "$port"
    printf 'STUNDECK_SECURE_COOKIES=%s\n' "$secure_cookies"
    printf 'STUNDECK_STUN_SERVER=turn.cloudflare.com:3478\n'
    printf 'STUNDECK_UDP_STUN_SERVER=turn.cloudflare.com:53\n'
    printf 'STUNDECK_KEEPALIVE_SERVER=www.cloudflare.com:80\n'
    printf 'TZ=%s\n' "$timezone"
    printf 'HTTP_PROXY=\nHTTPS_PROXY=\nALL_PROXY=\n'
    printf 'http_proxy=\nhttps_proxy=\nall_proxy=\n'
} > "$env_tmp"
chmod 0600 "$env_tmp"
mv -f -- "$env_tmp" "$env_file"
trap - EXIT HUP INT TERM

export STUNDECK_LISTEN="0.0.0.0:${port}"
export STUNDECK_SECURE_COOKIES="$secure_cookies"
export STUNDECK_STUN_SERVER=turn.cloudflare.com:3478
export STUNDECK_UDP_STUN_SERVER=turn.cloudflare.com:53
export STUNDECK_KEEPALIVE_SERVER=www.cloudflare.com:80
export TZ="$timezone"
HTTP_PROXY=''
HTTPS_PROXY=''
ALL_PROXY=''
http_proxy=''
https_proxy=''
all_proxy=''
export HTTP_PROXY HTTPS_PROXY ALL_PROXY
export http_proxy https_proxy all_proxy

exec /usr/local/bin/stundeck
