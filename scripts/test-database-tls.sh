#!/usr/bin/env bash
# Verify native database TLS and the supported warning-only exceptions.
set -euo pipefail

cd "$(dirname "$0")/.."
case ${DURPDEPLOY_TLS_BACKEND:-} in
    postgres|mssql) ;;
    *) echo 'ERROR: set DURPDEPLOY_TLS_BACKEND to postgres or mssql.' >&2; exit 1 ;;
esac
if [[ $(uname -s) != Linux ]]; then
    echo 'ERROR: database TLS checks require Linux.' >&2
    exit 1
fi
if [[ $DURPDEPLOY_TLS_BACKEND == mssql && $(uname -m) != x86_64 ]]; then
    echo 'ERROR: SQL Server TLS checks require Linux amd64.' >&2
    exit 1
fi
make templ-generate swagger-ui-copy
bash scripts/go_test.sh -tags=e2e,dbtls -race -count=1 -timeout=10m \
    -v -run '^TestDatabaseTLSAPIEndToEnd$' ./internal/handler/api
