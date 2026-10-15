#!/usr/bin/env bash
# Exercise the real stamped executable with no runtime configuration.
set -euo pipefail
directory=$(mktemp -d)
trap 'rm -rf "$directory"' EXIT
commit=$(git rev-parse HEAD)
number=$(git rev-list --count HEAD)
for version in v1.2.3+build.4 "v0.0.0-dev.${number}+g${commit:0:12}"; do
	BUILD_VERSION=$version BUILD_COMMIT=$commit BUILD_NUMBER=$number BUILD_MODIFIED=false \
		bash scripts/build_server.sh -o "$directory/durpdeploy"
	for argument in version --version -v; do
		output=$(cd "$directory" && env -i ./durpdeploy "$argument")
		[[ "$output" == "durpdeploy $version (build $number, commit $commit)" ]]
		printf 'PASS: %s: %s\n' "$argument" "$output"
	done
	[[ ! -e "$directory/durpdeploy.db" ]]
done
go build -o "$directory/unstamped" ./cmd/server
[[ "$(env -i "$directory/unstamped" version)" == 'durpdeploy dev (build unknown, commit '* ]]
echo 'PASS: unstamped executable reports a development identity'
