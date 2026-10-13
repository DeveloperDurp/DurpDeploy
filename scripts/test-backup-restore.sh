#!/usr/bin/env bash
# Run the isolated recovery drill with the same checks used by PR CI.
set -euo pipefail

cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux ]]; then
    echo 'ERROR: the backup restore drill requires Linux.' >&2
    exit 1
fi
make templ-generate swagger-ui-copy
bash scripts/go_test.sh -tags=e2e,backuprestore -race -count=1 \
    -timeout=8m -v -run '^TestBackupRestoreAPIWebE2E$' ./internal/handler/api
bash scripts/test-backup-restore-cleanup.sh
