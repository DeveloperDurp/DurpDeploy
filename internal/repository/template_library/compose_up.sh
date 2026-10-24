set -euo pipefail

: "${COMPOSE_FILE:?Set COMPOSE_FILE}"
: "${COMPOSE_PROJECT:?Set COMPOSE_PROJECT}"
: "${APP_IMAGE:?Set APP_IMAGE}"

if [[ "$COMPOSE_FILE" != /* || ! -f "$COMPOSE_FILE" ]]; then
    echo 'Use an existing absolute Compose file path' >&2
    exit 1
fi

if ! [[ "$COMPOSE_PROJECT" =~ ^[a-z0-9][a-z0-9_-]*$ ]]; then
    echo 'Invalid Compose project name' >&2
    exit 1
fi

docker compose --file "$COMPOSE_FILE" --project-name "$COMPOSE_PROJECT" up --detach
