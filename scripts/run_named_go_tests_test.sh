#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
fixture=$(mktemp -d "$repo_root/.named-go-tests.XXXXXX")
trap 'rm -rf "$fixture"' EXIT
mkdir -p "$fixture/one" "$fixture/two"

cat >"$fixture/one/named_test.go" <<'EOF'
package one

import "testing"

func TestPass(t *testing.T) {}

func TestParent(t *testing.T) {
	t.Run("Leaf", func(t *testing.T) {})
}

func TestSkipped(t *testing.T) {
	t.Skip("fixture")
}
EOF

cat >"$fixture/two/named_test.go" <<'EOF'
package two

import "testing"

func TestPass(t *testing.T) {}
EOF

runner="$repo_root/scripts/run_named_go_tests.sh"
package_one="./${fixture#"$repo_root/"}/one"
package_two="./${fixture#"$repo_root/"}/two"

expect_pass() {
	if ! "$runner" "$@" >/dev/null 2>&1; then
		printf 'expected pass: %q ' "$@" >&2
		printf '\n' >&2
		exit 1
	fi
}

expect_fail() {
	if "$runner" "$@" >/dev/null 2>&1; then
		printf 'expected failure: %q ' "$@" >&2
		printf '\n' >&2
		exit 1
	fi
}

expect_pass \
	--packages "$package_one" \
	--tests 'TestPass|TestParent' \
	--require 'TestPass|TestParent/Leaf'
expect_fail \
	--packages "$package_one" \
	--tests 'TestPass' \
	--require 'TestMissing'
expect_fail \
	--packages "$package_one" \
	--tests 'TestParent' \
	--require 'TestParent/MissingLeaf'
expect_fail \
	--packages "$package_one" \
	--tests 'TestSkipped' \
	--require 'TestSkipped'
expect_fail \
	--packages "$package_one $package_two" \
	--tests 'TestPass' \
	--require 'TestPass'

require_file="$fixture/required.txt"
: >"$require_file"
expect_fail \
	--packages "$package_one" \
	--tests 'TestPass' \
	--require-file "$require_file"
printf 'TestPass\n' >"$require_file"
expect_pass \
	--packages "$package_one" \
	--tests 'TestPass' \
	--require-file "$require_file"
expect_fail \
	--packages "$package_one" \
	--tests 'TestPass' \
	--require 'TestPass' \
	--require-file "$require_file"

printf 'run_named_go_tests adversarial contract: PASS\n'
