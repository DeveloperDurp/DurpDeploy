#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
    echo "usage: $0 <output-directory>" >&2
    exit 2
fi

output_dir=$1
mkdir -p "$output_dir"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT

go_version=$(go env GOVERSION)
printf 'module capturetest\n\ngo %s\n' "${go_version#go}" >"$fixture/go.mod"
cat >"$fixture/capture_test.go" <<'EOF'
package capturetest

import "testing"

func TestCapture(t *testing.T) {}
EOF

bash ./scripts/capture-test.sh "$output_dir/success.txt" \
    bash -c 'echo success'
grep -Fq success "$output_dir/success.txt"

set +e
bash ./scripts/capture-test.sh "$output_dir/failure.txt" \
    bash -c 'echo failure; exit 7'
status=$?
set -e
if [ "$status" -ne 7 ]; then
    echo "wrapped failure status = $status, want 7" >&2
    exit 1
fi

set +e
CAPTURE_TEST_GENERATED=1 bash ./scripts/capture-test.sh \
    "$output_dir/no-match.txt" go -C "$fixture" test -v -run Missing
status=$?
set -e
if [ "$status" -eq 0 ]; then
    echo "no-match go test was accepted" >&2
    exit 1
fi

CAPTURE_TEST_GENERATED=1 bash ./scripts/capture-test.sh \
    "$output_dir/go-success.txt" go -C "$fixture" test -v -run TestCapture

printf 'capture-test selftest passed\n' | tee "$output_dir/selftest.txt"
