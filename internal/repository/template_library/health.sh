set -euo pipefail

: "${HEALTH_URL:?Set HEALTH_URL}"

http_status=$(
    curl --disable --fail --silent --show-error \
        --output /dev/null --write-out '%{http_code}' \
        --proto '=http,https' \
        --connect-timeout 5 --max-time 30 \
        --retry 5 --retry-connrefused --retry-delay 2 --retry-max-time 90 \
        --url "$HEALTH_URL"
)

if [[ "$http_status" != 2[0-9][0-9] ]]; then
    echo 'HTTP health check requires a 2xx response' >&2
    exit 1
fi

echo 'HTTP health check passed'
