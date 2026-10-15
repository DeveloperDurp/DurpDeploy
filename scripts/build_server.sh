#!/usr/bin/env bash
# Pass normal go build flags, such as -o PATH or -trimpath, as arguments.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/build_metadata.sh"
ldflags="-s -w -X durpdeploy/internal/buildinfo.version=$BUILD_VERSION"
ldflags+=" -X durpdeploy/internal/buildinfo.commit=$BUILD_COMMIT"
ldflags+=" -X durpdeploy/internal/buildinfo.buildNumber=$BUILD_NUMBER"
ldflags+=" -X durpdeploy/internal/buildinfo.modified=$BUILD_MODIFIED"
exec go build -ldflags "$ldflags" "$@" ./cmd/server
