set -euo pipefail

: "${SERVICE_NAME:?Set SERVICE_NAME}"

if ! [[ "$SERVICE_NAME" =~ ^[a-zA-Z0-9_][a-zA-Z0-9_@.:-]*$ ]]; then
    echo 'Invalid service unit name' >&2
    exit 1
fi

systemctl --no-ask-password restart -- "$SERVICE_NAME"
systemctl --no-ask-password is-active --quiet -- "$SERVICE_NAME"
echo 'Service is active'
