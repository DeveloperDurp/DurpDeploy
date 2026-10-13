#!/usr/bin/env bash
set -euo pipefail

version=${1:?usage: install_golangci_lint.sh version}
root=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
target="$root/bin/lint/golangci-lint"
if [[ -x "$target" ]] && "$target" --version | grep -Fq "version $version "; then
  exit 0
fi

platform="$(go env GOOS)-$(go env GOARCH)"
case "$version-$platform" in
  2.11.3-linux-amd64) digest=87bb8cddbcc825d5778b64e8a91b46c0526b247f4e2f2904dea74ec7450475d1 ;;
  2.11.3-linux-arm64) digest=ee3d95f301359e7d578e6d99c8ad5aeadbabc5a13009a30b2b0df11c8058afe9 ;;
  2.11.3-darwin-amd64) digest=f93bda1f2cc981fd1326464020494be62f387bbf262706e1b3b644e5afacc440 ;;
  2.11.3-darwin-arm64) digest=30ee39979c516b9d1adca289a3f93429d130c4c0fda5e57d637850894221f6cc ;;
  *) printf 'Unsupported golangci-lint version or platform: %s-%s\n' "$version" "$platform" >&2; exit 1 ;;
esac

temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
archive="golangci-lint-$version-$platform.tar.gz"
curl --fail --silent --show-error --location \
  "https://github.com/golangci/golangci-lint/releases/download/v$version/$archive" \
  --output "$temporary/$archive"
if command -v sha256sum >/dev/null; then
  actual=$(sha256sum "$temporary/$archive" | cut -d ' ' -f 1)
else
  actual=$(shasum -a 256 "$temporary/$archive" | cut -d ' ' -f 1)
fi
[[ "$actual" == "$digest" ]] || { printf 'golangci-lint checksum mismatch\n' >&2; exit 1; }
tar -xzf "$temporary/$archive" -C "$temporary"
mkdir -p "$(dirname -- "$target")"
# Keep the upstream license and notices beside the tool, outside bundled app assets.
cp "$temporary/golangci-lint-$version-$platform/LICENSE" "$(dirname -- "$target")/LICENSE"
install -m 755 "$temporary/golangci-lint-$version-$platform/golangci-lint" "$target"
