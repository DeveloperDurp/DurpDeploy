#!/usr/bin/env bash
# Run the isolated recovery drill with the same checks used by PR CI.
set -euo pipefail

cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux ]]; then
    echo 'ERROR: the backup restore drill requires Linux.' >&2
    exit 1
fi
if [[ $(uname -m) != x86_64 ]]; then
    echo 'ERROR: the backup restore drill requires Linux amd64.' >&2
    exit 1
fi
case ${DURPDEPLOY_CONTAINER_RUNTIME:-} in
    docker|podman) ;;
    *) echo 'ERROR: set DURPDEPLOY_CONTAINER_RUNTIME to docker or podman.' >&2; exit 1 ;;
esac
if [[ -n ${DURPDEPLOY_CONTAINER_URL:-} ]]; then
    export DOCKER_HOST=$DURPDEPLOY_CONTAINER_URL
fi
make templ-generate swagger-ui-copy
bash scripts/go_test.sh -tags=e2e,backuprestore -race -count=1 \
    -timeout=8m -v -run '^TestBackupRestoreAPIWebE2E$' ./internal/handler/api
bash scripts/test-backup-restore-cleanup.sh
