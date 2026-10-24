#!/usr/bin/env bash
# Compile shared variants once. Every consumer still executes its own tests.
set -euo pipefail
make templ-generate swagger-ui-copy
directory=$(mktemp -d)
trap 'rm -rf -- "$directory"' EXIT
go test -c -tags=e2e,packagebrowser -race -o "$directory/browser.test" ./internal/handler/api
go test -c -tags=e2e -race -o "$directory/api-race.test" ./internal/handler/api
go test -c -tags=e2e -covermode=atomic -coverpkg=./... -o "$directory/api-coverage.test" ./internal/handler/api
python3 scripts/ci_go_cache.py export
