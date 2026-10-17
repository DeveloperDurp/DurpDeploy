#!/usr/bin/env bash
set -euo pipefail
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
version=0.76.0
case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) arch=64bit; digest=e24b1c51293c1b15e4ba8d45bbc7891ba053f90278ec2698f3562c993e145fe7 ;;
  Linux/aarch64|Linux/arm64) arch=ARM64; digest=aab21e677a7b7071fc79f1711480840754c18a1b9d9cefa7ccd06af63a0e8f4c ;;
  *) printf 'Trivy installer supports Linux AMD64 and ARM64 only\n' >&2; exit 2 ;;
esac
destination=${TRIVY_TOOL_DIR:-$root/bin/security}
private=$(mktemp -d)
trap 'rm -rf "$private"' EXIT
curl --fail --silent --show-error --location --retry 3 --max-time 180 \
  "https://github.com/aquasecurity/trivy/releases/download/v$version/trivy_${version}_Linux-$arch.tar.gz" \
  -o "$private/trivy.tar.gz"
printf '%s  %s\n' "$digest" "$private/trivy.tar.gz" | sha256sum --check --status
mkdir -p "$destination"
tar -xzf "$private/trivy.tar.gz" -C "$private"
install -m 755 "$private/trivy" "$destination/trivy"
# Retain the upstream license and dependency notices with the task-local tool.
cp "$private/LICENSE" "$destination/trivy-LICENSE"
cp "$root/.security/trivy-NOTICE" "$destination/trivy-NOTICE"
