#!/usr/bin/env bash
set -euo pipefail
export GOTOOLCHAIN=go1.26.10
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
destination=${SECURITY_TOOL_DIR:-$root/bin/security}
mkdir -p "$destination"
destination=$(CDPATH= cd -- "$destination" && pwd)

install_tool() {
  local tool=$1 module=$2 version=$3 package=$4 candidate
  for candidate in "$destination/$tool" "/opt/ci-tools/security/$tool"; do
    if [[ -x $candidate ]] &&
      [[ $(go version -m "$candidate" | awk '$1 == "mod" {print $2 "@" $3}') == "$module@$version" ]] &&
      [[ $(go version -m "$candidate" | sed -n '1p') == *": $GOTOOLCHAIN" ]]; then
      if [[ $candidate != "$destination/$tool" ]]; then
        install -m 755 "$candidate" "$destination/$tool"
      fi
      return
    fi
  done
  GOBIN="$destination" go install "$module$package@$version"
}

# Go's checksum database verifies these existing, pinned module versions.
install_tool govulncheck golang.org/x/vuln v1.8.0 /cmd/govulncheck
install_tool gosec github.com/securego/gosec/v2 v2.29.0 /cmd/gosec
install_tool gitleaks github.com/zricethezav/gitleaks/v8 v8.30.1 ''
