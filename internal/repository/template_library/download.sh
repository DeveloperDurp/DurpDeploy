set -euo pipefail

: "${DURPDEPLOY_STAGE_DIR:?Managed staging is required}"
: "${ARTIFACT_URL:?Set ARTIFACT_URL}"

if [[ "$ARTIFACT_URL" != https://* ]]; then
    echo 'Artifact URL must use HTTPS' >&2
    exit 1
fi

artifact_token=${ARTIFACT_TOKEN:-}
if ! [[ "$artifact_token" =~ ^[a-zA-Z0-9._~+/-]*=*$ ]]; then
    echo 'Invalid bearer token' >&2
    exit 1
fi

temporary_file=$(mktemp "$DURPDEPLOY_STAGE_DIR/artifact.XXXXXX")
trap 'rm -f -- "$temporary_file"' EXIT

# Pass the bearer header through stdin to keep it out of curl's arguments.
http_status=$(
    {
        if [[ -n "$artifact_token" ]]; then
            printf 'header = "Authorization: Bearer %s"\n' "$artifact_token"
        fi
    } | curl --disable --config - \
        --fail --silent --show-error --location \
        --proto '=https' --proto-redir '=https' \
        --connect-timeout 10 --max-time 240 \
        --output "$temporary_file" \
        --write-out '%{http_code}' \
        --url "$ARTIFACT_URL"
)

if [[ "$http_status" != 2[0-9][0-9] ]]; then
    echo 'Artifact download requires a 2xx response' >&2
    exit 1
fi

mv -fT -- "$temporary_file" "$DURPDEPLOY_STAGE_DIR/artifact"
echo 'Artifact downloaded to managed staging'
